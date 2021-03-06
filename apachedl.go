package main

import (
    "bufio"
    "flag"
    "fmt"
    "io"
    "io/ioutil"
    "net/http"
    "net/url"
    "os"
    "regexp"
    "runtime"
    "strconv"
    "strings"
    "time"

    "github.com/d-schmidt/soup"
)

const SLASH = 47
const GET_RETRIES = 5

var name, pw, dirUrl, target string
var skipExisting bool
var client *http.Client


type ResultStatus int
const (
    SUCCESS ResultStatus = iota
    RETRY
    SKIP
    ERROR
    START
    WORKING
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
    for i := 1; i <= GET_RETRIES; i++ {
        if downloadFunc(url) != RETRY {
            break
        }
        time.Sleep(time.Duration(i * 5) * time.Second)
    }
}


func toMiB(bytes int64) float64 {
    return float64(bytes) / 1024 / 1024
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
        fmt.Println("Downloading directory html", url)

        req, _ := http.NewRequest("GET", url, nil)
        req.SetBasicAuth(name, pw)
        resp, err := client.Do(req)

        ch <- &HttpResponse{resp, err}
    }(dirUrl)

    for {
        select {
        case r := <-ch:
            fmt.Println("Directory html download done")
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

    fmt.Printf("Saving to file '%s'\n", fileName)
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


func updateStatus(out *os.File, resultChannel chan *Result, stopChannel chan bool) {
    for {
        select {
            case <-stopChannel:
                return
            case <-time.After(10 * time.Second):
                out.Sync()
                info, _ := out.Stat()
                resultChannel <- &Result{WORKING, info.Size()}
        }
    }
}


func httpGetFile(fileUrl string, resultChannel chan *Result, stopChannel chan bool) {

    parts := strings.Split(fileUrl, "/")
    fileName, _ := url.QueryUnescape(parts[len(parts) - 1])
    fileName = fixPath(cleanName(fileName))
    isNewFile := pathExists(fileName)

    var out *os.File
    out, success := openFile(fileName)
    if !success {
        resultChannel <- &Result{SKIP, 0}
        return
    }
    defer out.Close()

    if !isNewFile {
        if status := isFileAppendPossible(out, fileUrl); status != SUCCESS {
            resultChannel <- &Result{status, 0}
            return
        }
    }

    info, _ := out.Stat()
    req, _ := http.NewRequest("GET", fileUrl, nil)
    req.SetBasicAuth(name, pw)
    if info.Size() > 0 {
        fmt.Printf("\nContinue existing file at %6.1f MiB\n", toMiB(info.Size()))
        req.Header.Add("Range", fmt.Sprintf("bytes=%d-", info.Size()))
    }

    resp, err := client.Do(req)
    if err != nil {
        fmt.Printf("\nConnection error for url: %v %s\n", err, fileUrl)
        resultChannel <- &Result{RETRY, 0}
        return
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 300 {
        fmt.Printf("\nBad GET response for url: %s %s\n", resp.Status, fileUrl)
        if resp.StatusCode >= 500 {
            resultChannel <- &Result{RETRY, 0}
        } else {
            resultChannel <- &Result{ERROR, 0}
        }
        return
    }

    cLength, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
    resultChannel <- &Result{START, int64(cLength) + info.Size()}

    go updateStatus(out, resultChannel, stopChannel)
    n, err := io.Copy(out, resp.Body)
    if err != nil {
        fmt.Printf("\nDownload error for url: %v %s\n", err, fileUrl)
        resultChannel <- &Result{RETRY, n}
        return
    }

    resultChannel <- &Result{SUCCESS, n}
}


func asyncHttpGetFile(fileUrl string) ResultStatus {
    resultChannel := make(chan *Result)
    stopChannel := make(chan bool)
    defer func() { stopChannel <- true }()

    go httpGetFile(fileUrl, resultChannel, stopChannel)

    var size int64
    start := time.Now()
    var last int64

    for result := range resultChannel {
        done := toMiB(result.bytes - last)
        last = result.bytes

        switch result.status {
        case START:
            size = result.bytes
            fmt.Printf("%s - %6.1f MiB file size\n", start.Format(time.Stamp), done)
        case WORKING:

            elapsed := time.Now().Sub(start).Seconds()
            start = time.Now()
            mbps := done / elapsed

            fmt.Printf("%s - %6.1f MiB done (%3d%% %.2f MBps)\n",
                time.Now().Format(time.Stamp),
                toMiB(result.bytes),
                result.bytes * 100 / size,
                mbps)

        default:
            return result.status
        }
    }

    return ERROR
}


func findLinks(html []byte, dirUrl string) []string {
    doc := soup.HTMLParse(string(html))
    links := doc.FindAll("a")

    // regex: optional host + path as group + optional parameters
    urlPattern := regexp.MustCompile(`^(?:(?:https?:)?//([^/]+))?([^?#]+).*$`)
    dirHost := urlPattern.FindStringSubmatch(dirUrl)[1]
    dirPath := urlPattern.FindStringSubmatch(dirUrl)[2]
    var result []string
    fmt.Println("Extracting links from url:", dirUrl, "host+path", dirHost, dirPath)

    for _, link := range links {
        href := link.Attrs()["href"]
        if len(href) > 0 {
            match := urlPattern.FindStringSubmatch(href)

            // if match and host is empty or dirHost
            if len(match) > 1 && (len(match[1]) == 0 ||  match[1] == dirHost) {
                path := match[2]

                // handle root url paths starting with '/'
                if path[0] == SLASH {
                    if len(path) > len(dirPath) && path[:len(dirPath)] == dirPath {
                        // if start of path matches current dir, cut it of
                        path = path[len(dirPath):]
                    }  else {
                        // else ignore; we don't want to go up, only deeper
                        path = ""
                    }
                }

                if len(path) >= 2 && path[:2] == ".." {
                    path = ""
                }

                // TODO remove starting with /
                if (len(path) > 0) {
                    fullLink := dirUrl + path
                    fmt.Println("Using link:", fullLink)
                    result = append(result, fullLink)
                }
            }
        }
    }

    return result
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
    links := findLinks(body, dirUrl)

    if len(links) > 0 {
        // get last directory part of path (i.e. b of http://example.com/a/b/)
        parts := strings.Split(dirUrl, "/")
        dirName, err := url.QueryUnescape(parts[len(parts) - 2])
        dirName = cleanName(dirName)

        if !pathExists(dirName) {
            fmt.Println("Creating local directory:", dirName)
            err = os.Mkdir("./" + dirName, os.ModeDir | 0775)
            if err != nil { panic(err) }
        }

        err = os.Chdir(dirName)
        if err != nil { panic(err) }
        defer chDirUp()

        for i, link := range links {
            fmt.Printf("\nlink %2d/%2d: %s\n", i+1, len(links), link)

            if link[len(link) - 1] != SLASH {
                doWhileRetry(link, asyncHttpGetFile)
            } else {
                doWhileRetry(link, recursiveLoadDir)
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