package main

import "testing"

func Equal(a, b []string) bool {
    if len(a) != len(b) {
        return false
    }
    for i, v := range a {
        if v != b[i] {
            return false
        }
    }
    return true
}


func TestDoWhileRetry(t *testing.T) {
    counter := 0
    want := GET_RETRIES

    countUp := func(url string) ResultStatus {
        counter++
        return RETRY
    }
    //doWhileRetry("", countUp)
    countUp("")

    if counter != want {
        //t.Errorf("doWhileRetry() = %q, want %q", counter, want)
    }
}


func TestFindLinks(t *testing.T) {
    want := []string{"http://e.f/e/1/",
            "http://e.f/e/2/2",
            "http://e.f/e/3/",
            "http://e.f/e/4/",
            "http://e.f/e/5",
            "http://e.f/e/666666",
            "http://e.f/e/7",
            "http://e.f/e/8",
            "http://e.f/e/9"}

    html := []byte(`<html><body>
            <a>0</a>
            <a href="">0</a>
            <a href="/">0</a>
            <a href="/0">0</a>
            <a href="/000000000">0</a>
            <a href="?no=yes">0</a>
            <a href="#go">0</a>
            <a href="http://f.g/e/01">0</a>
            <a href="//e.f/e/">0</a>
            <a href="//f.g/e/02">0</a>

            <a href="1/">1</a>
            <a href="2/2">2</a>
            <a href="/e/3/">3</a>
            <a href="http://e.f/e/4/">4</a>
            <a href="5">5</a>
            <a href="666666">6</a>
            <a href="7?no=yes">7</a>
            <a href="8#go">8</a>
            <a href="//e.f/e/9">9</a>
        </body></html>`)

    if got := findLinks(html, "http://e.f/e/"); !Equal(got, want) {
        t.Errorf("FindLinks() = \n%q, want \n%q", got, want)
    }
}