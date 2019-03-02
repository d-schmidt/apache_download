package main

import (
    "bufio"
    "encoding/xml"
    "flag"
    "fmt"
    "io"
    "io/ioutil"
    "net/http"
    "net/url"
    "os"
    "runtime"
    "strconv"
    "strings"
    "time"
)

const SLASH = 47

var name, pw, dirUrl, target string
var skipExisting bool
var client *http.Client


type ResultStatus int
const (
    SUCCESS ResultStatus = iota
    RETRY
    SKIP
    ERROR
)
type Result struct {
    status   ResultStatus
    bytes    int64
}

type HttpResponse struct {
    response *http.Response
    err      error
}


type DownloadFunc func(url string) ResultStatus
func doWhileRetry(url string, downloadFunc DownloadFunc) {
    for i := 1; i <= 5; i++ {
        if downloadFunc(url) != RETRY {
            break
        }
        time.Sleep(time.Duration(i * 5) * time.Second)
    }
}


func fixPath(path string) string {
    pwd, err := os.Getwd()
    if err != nil { panic(err) }

    result := pwd + string(os.PathSeparator) + path

    // fix to long pathes for windows
    if len(result) > 259 && runtime.GOOS == "windows" {
        result = "\\\\?\\" + result
    }
    return result
}


func cleanName(name string) string {

    result := ""

    for _, nameChar := range name {
        if strings.ContainsRune("<>:/|?*\"\\", nameChar) {
            result = result + "-"
        } else {
            result = result + fmt.Sprintf("%c", nameChar)
        }
    }

    return result
}


func asyncHttpGetDir(dirUrl string) *HttpResponse {
    ch := make(chan *HttpResponse)

    go func(url string) {
        fmt.Printf("\nDownloading directory %s\n", url)

        req, _ := http.NewRequest("GET", url + "?F=0", nil)
        req.SetBasicAuth(name, pw)
        resp, err := client.Do(req)

        ch <- &HttpResponse{resp, err}
    }(dirUrl)

    for {
        select {
        case r := <-ch:
            fmt.Printf("\ndirectory page download done: %s %s\n", r.err, dirUrl)
            return r
        case <-time.After(100 * time.Millisecond):
            fmt.Printf(".")
        }
    }

    return &HttpResponse{}
}


func pathExists(path string) bool {
    _, err := os.Stat(path)
    if err != nil && !os.IsNotExist(err) {
        panic(err)
    }
    return err == nil
}


func openFile(fileName string) (*os.File, bool) {
    if skipExisting && pathExists(fileName) {
        fmt.Printf("\n%s exists already; skipping\n", fileName)
        return nil, false
    }

    fmt.Printf("\nSaving to file '%s'\n", fileName)
    out, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
    if err != nil { panic(err) }
    return out, true
}


func isFileAppendPossible(out *os.File, fileUrl string) ResultStatus {
    info, err := out.Stat()
    if err != nil { panic(err) }

    if info.Size() > 0 {
        req, _ := http.NewRequest("HEAD", fileUrl, nil)
        req.SetBasicAuth(name, pw)

        resp, err := client.Do(req)
        if err != nil {
            fmt.Printf("\nConnection error for url: %v %s\n", err, fileUrl)
            return RETRY
        }
        resp.Body.Close()

        if resp.StatusCode != 200 {
            fmt.Printf("\nBad HEAD response for url: %s %s\n", resp.Status, fileUrl)
            if resp.StatusCode >= 500 {
                return RETRY
            }
            return ERROR
        }

        cLength, err := strconv.Atoi(resp.Header.Get("Content-Length"))
        if err != nil {
            fmt.Printf("\nContent-Length header error\n")
            return ERROR

        }
        if int64(cLength) >= info.Size() {
            fmt.Printf("\nFile is already complete or Content-Length header error\n")
            return SKIP
        }
        if "bytes" != resp.Header.Get("Accept-Ranges") {
            fmt.Printf("\nServer does not accept bytes ranges to download partial file\n")
            return ERROR
        }
    }

    return SUCCESS
}

func asyncHttpGetFile(fileUrl string) ResultStatus {
    ch := make(chan *Result)

    go func(fileUrl string) {
        parts := strings.Split(fileUrl, "/")
        fileName, _ := url.QueryUnescape(parts[len(parts) - 1])
        fileName = fixPath(cleanName(fileName))
        isNewFile := pathExists(fileName)
        var out *os.File
        out, success := openFile(fileName)
        if !success {
            ch <- &Result{SKIP, 0}
            return
        }
        defer out.Close()

        if !isNewFile {
            if status := isFileAppendPossible(out, fileUrl); status != SUCCESS {
                ch <- &Result{status, 0}
                return
            }
        }

        info, _ := out.Stat()
        req, _ := http.NewRequest("GET", fileUrl, nil)
        req.SetBasicAuth(name, pw)
        req.Header.Add("Range", fmt.Sprintf("bytes=%d-", info.Size()))

        resp, err := client.Do(req)
        if err != nil {
            fmt.Printf("\nConnection error for url: %v %s\n", err, fileUrl)
            ch <- &Result{RETRY, 0}
            return
        }
        defer resp.Body.Close()

        if resp.StatusCode >= 300 {
            fmt.Printf("\nBad GET response for url: %s %s\n", resp.Status, fileUrl)
            if resp.StatusCode >= 500 {
                ch <- &Result{RETRY, 0}
            } else {
                ch <- &Result{ERROR, 0}
            }
            return
        }

        cLength, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
        fmt.Printf("\nDownload size: %d\n", cLength)

        n, err := io.Copy(out, resp.Body)
        if err != nil {
            fmt.Printf("\nDownload error for url: %v %s\n", err, fileUrl)
            ch <- &Result{RETRY, n}
            return
        }

        ch <- &Result{SUCCESS, n}
    }(fileUrl)

    for {
        select {
        case r := <-ch:
            fmt.Printf("%d bytes loaded (Status %d)\n", r.bytes, r.status)
            return r.status
        case <-time.After(5 * time.Second):
            fmt.Printf(".")
        }
    }

    return ERROR
}


type Page struct {
    ATags []Link `xml:"body>ul>li>a"`
}

type Link struct {
    Href string `xml:"href,attr"`
}


func chDirUp() {
    err := os.Chdir("..")
    if err != nil { panic(err) }
}


func recursiveLoadDir(dirUrl string) ResultStatus {
    result := asyncHttpGetDir(dirUrl)
    if result.err != nil {
        fmt.Printf("\nConnection error for directory url: %v %s\n", result.err, dirUrl)
        return RETRY
    }
    defer result.response.Body.Close()

    if result.response.StatusCode != 200 {
        fmt.Printf("\nBad GET response for url: %s %s\n", result.response.Status, dirUrl)
        if result.response.StatusCode >= 500 {
            return RETRY
        }
        return ERROR
    }

    body, _ := ioutil.ReadAll(result.response.Body)

    var page Page
    if xmlerr := xml.Unmarshal(body, &page); xmlerr != nil {
        fmt.Printf("XMLERROR while reading directory html: %v\n", xmlerr)
        return ERROR
    }

    if len(page.ATags) > 1 {
        parts := strings.Split(dirUrl, "/")
        dirName, err := url.QueryUnescape(parts[len(parts) - 2])
        dirName = cleanName(dirName)

        if !pathExists(dirName) {
            fmt.Printf("\ncreate dir: '%s'\n", dirName)
            err = os.Mkdir("./" + dirName, os.ModeDir | 0775)
            if err != nil { panic(err) }
        }

        err = os.Chdir(dirName)
        if err != nil { panic(err) }
        defer chDirUp()

        for _, game := range page.ATags[1:] {
            fmt.Printf("\nlink found on page: %s", game.Href)

            if game.Href[len(game.Href) - 1] != SLASH {
                doWhileRetry(dirUrl + game.Href, asyncHttpGetFile)
            } else {
                doWhileRetry(dirUrl + game.Href, recursiveLoadDir)
            }
        }
    }

    return SUCCESS
}


func main() {
    var proxy string
    flag.StringVar(&name, "name", "", "username")
    flag.StringVar(&pw, "pw", "", "password")
    flag.StringVar(&dirUrl, "link", "", "directory page link")
    flag.StringVar(&target, "target", "", "directory target dir")
    flag.StringVar(&proxy, "proxy", "", "proxy in format 'http://10.0.0.1:1234'")
    flag.BoolVar(&skipExisting, "skip", false, "skip existing files completely [default: false]")
    flag.Parse()

    if len(proxy) > 0 {
        proxyUrl, _ := url.Parse(proxy)
        client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyUrl)}}
    } else {
        client = &http.Client{}
    }

    var dirUrls []string

    scanner := bufio.NewScanner(os.Stdin)
    if len(name) == 0 {
        fmt.Print("Enter name: ")
        scanner.Scan()
        name = scanner.Text()
    }
    if len(pw) == 0 {
        fmt.Print("Enter password: ")
        scanner.Scan()
        pw = scanner.Text()
    }
    if len(dirUrl) == 0 {
        dirUrls = make([]string, 0, 1)
        fmt.Print("Enter link: ")
        scanner.Scan()
        dirUrl = scanner.Text()
        if len(dirUrl) > 0 {
            dirUrls = append(dirUrls, dirUrl)
            for len(dirUrl) > 0 {
                fmt.Print("Enter another link (or leave empty): ")
                scanner.Scan()
                dirUrl = scanner.Text()
                if len(dirUrl) > 0 {
                    dirUrls = append(dirUrls, dirUrl)
                }
            }
        }
    } else {
        dirUrls = []string{dirUrl}
    }

    fmt.Printf("dirUrls %s\n", dirUrls)
    if len(dirUrls) == 0 {
        fmt.Println("you need to enter urls or use start params:")
        flag.PrintDefaults()
        return
    }

    if len(target) > 0 {
        err := os.Chdir(target)
        if err != nil { panic(err) }
    }

    for _, dirUrl := range dirUrls {
        if dirUrl[len(dirUrl) - 1] != SLASH {
            doWhileRetry(dirUrl, asyncHttpGetFile)
        } else {
            doWhileRetry(dirUrl, recursiveLoadDir)
        }
    }
    fmt.Printf("the end")
}