package main

import (
    "fmt"
    "net/http"
    "time"
    "io"
    "io/ioutil"
    "os"
    "net/url"
    "encoding/xml"
    "strings"
    "runtime"
    "flag"
    "bufio"
)

var name, pw, dirUrl, target string
var client *http.Client


type HttpResponse struct {
    url      string
    response *http.Response
    err      error
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

        req, err := http.NewRequest("GET", url + "?F=0", nil)
        req.SetBasicAuth(name, pw)
        resp, err := client.Do(req)

        ch <- &HttpResponse{url, resp, err}
    }(dirUrl)

    for {
        select {
        case r := <-ch:
            fmt.Printf("\ndirectory page download done, error: %v\n", r.err)
            return r
        case <-time.After(100 * time.Millisecond):
            fmt.Printf(".")
        }
    }
    return &HttpResponse{}
}


func asyncHttpGetFile(fileUrl string) bool {
    ch := make(chan int64)

    go func(fileUrl string) {
        parts := strings.Split(fileUrl, "/")
        fileName, _ := url.QueryUnescape(parts[len(parts) - 1])
        fileName = cleanName(fileName)

        // check if file exists or skip
        if _, err := os.Stat(fileName); err == nil {
            fmt.Printf("\n%s exists already; skipping\n", fileName)
            ch <- 0
        } else {
            fmt.Printf("\nLoading file '%s'\n", fileName)
            out, err := os.Create(fixPath(fileName))
            defer out.Close()
            if err != nil { panic(err) }

            req, err := http.NewRequest("GET", fileUrl, nil)
            req.SetBasicAuth(name, pw)
            resp, err := client.Do(req)
            defer resp.Body.Close()
            if err != nil { panic(err) }

            n, err := io.Copy(out, resp.Body)
            if err != nil { panic(err) }

            ch <- n
        }
    }(fileUrl)

    for {
        select {
        case r := <-ch:
            fmt.Printf("%d bytes loaded\n", r)
            return true
        case <-time.After(3 * time.Second):
            fmt.Printf(".")
        }
    }

    return true
}


type Page struct {
    ATags []Link `xml:"body>ul>li>a"`
}

type Link struct {
    Href string `xml:"href,attr"`
}


func recursiveLoadDir(dirUrl string) bool {
    result := asyncHttpGetDir(dirUrl)
    defer result.response.Body.Close()

    if result.response.StatusCode != 200 || result.err != nil {
        fmt.Printf("could not download dir, get status: %s\n", result.response.Status)
        return false
    }

    body, _ := ioutil.ReadAll(result.response.Body)

    var q Page

    if xmlerr := xml.Unmarshal(body, &q); xmlerr != nil {
        fmt.Printf("XMLERROR %s\n", xmlerr)
        panic(xmlerr)
    }

    if len(q.ATags) > 1 {
        parts := strings.Split(dirUrl, "/")
        dirName, err := url.QueryUnescape(parts[len(parts) - 2])
        dirName = cleanName(dirName)

        if _, err := os.Stat(dirName); os.IsNotExist(err) {
            fmt.Printf("\ncreate dir: '%s'\n", dirName)
            err = os.Mkdir("./" + dirName, os.ModeDir | 0775)
            if err != nil { panic(err) }
        }

        err = os.Chdir(dirName)
        if err != nil { panic(err) }

        for _, game := range q.ATags[1:] {
            fmt.Printf("\nlink found on page: %s", game.Href)

            if game.Href[len(game.Href) - 1] != 47 {
                asyncHttpGetFile(dirUrl + game.Href)
            } else {
                recursiveLoadDir(dirUrl + game.Href)
            }
        }

        err = os.Chdir("..")
        if err != nil { panic(err) }
    }

    return true
}


func main() {
    var proxy string
    flag.StringVar(&name, "name", "", "username")
    flag.StringVar(&pw, "pw", "", "password")
    flag.StringVar(&dirUrl, "link", "", "directory page link")
    flag.StringVar(&target, "target", "", "directory target dir")
    flag.StringVar(&proxy, "proxy", "", "proxy in format 'http://10.0.0.1:1234'")
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

    for _, durl := range dirUrls {
        if durl[len(durl) - 1] != 47 {
            asyncHttpGetFile(durl)
        } else {
            recursiveLoadDir(durl)
        }
    }
    fmt.Printf("the end")
}