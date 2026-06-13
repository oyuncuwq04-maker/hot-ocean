package main

import (
	"io/ioutil"
	"regexp"
	"strings"
)

func main() {
	b, _ := ioutil.ReadFile("checker.go")
	content := string(b)

	// Add Proxy and LogCb to Checker struct
	content = strings.Replace(content, 
		"type Checker struct {\n\tUUID     string\n\tDebug    bool\n\tKeywords []string\n\tMode     int\n}", 
		"type Checker struct {\n\tUUID     string\n\tDebug    bool\n\tKeywords []string\n\tMode     int\n\tProxy    string\n\tLogCb    func(string)\n}", 1)

	// Replace doAuth client initialization
	oldAuth := `jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	noRedir := &http.Client{
		Timeout: 20 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}`
	newAuth := `jar, _ := cookiejar.New(nil)
	transport := &http.Transport{}
	if c.Proxy != "" {
		if pURL, err := url.Parse(c.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(pURL)
		}
	}
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar, Transport: transport}
	
	noRedirTransport := &http.Transport{}
	if c.Proxy != "" {
		if pURL, err := url.Parse(c.Proxy); err == nil {
			noRedirTransport.Proxy = http.ProxyURL(pURL)
		}
	}
	noRedir := &http.Client{
		Timeout: 20 * time.Second,
		Jar:     jar,
		Transport: noRedirTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}`
	content = strings.Replace(content, oldAuth, newAuth, 1)

	// Replace c.dbg to use LogCb if defined
	oldDbg := `func (c *Checker) dbg(msg string) {
	if c.Debug {
		fmt.Printf("%s[DBG]%s %s\n", cYel, cRst, msg)
	}
}`
	newDbg := `func (c *Checker) dbg(msg string) {
	if c.Debug {
		if c.LogCb != nil {
			c.LogCb("[DBG] " + msg)
		} else {
			fmt.Printf("%s[DBG]%s %s\n", cYel, cRst, msg)
		}
	}
}`
	content = strings.Replace(content, oldDbg, newDbg, 1)

	// Remove main function and UI helpers
	re := regexp.MustCompile(`(?s)// ── TERMINAL HIT DISPLAY.*`)
	content = re.ReplaceAllString(content, "")

	ioutil.WriteFile("checker.go", []byte(content), 0644)
}
