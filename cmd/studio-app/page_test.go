package main

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

// The app's own page is a data URL that the window loads as any other, so the login link
// that follows it replaces it, however soon. It escapes what it says.
func TestPageIsADataURL(t *testing.T) {
	link := page("", "Studio didn't start", "listen tcp <addr>: in use")
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "data" {
		t.Fatalf("%q: %v", link, err)
	}
	head, data, ok := strings.Cut(u.Opaque, ",")
	if !ok || head != "text/html;charset=utf-8;base64" {
		t.Fatalf("header %q", head)
	}
	body, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Studio didn&#39;t start</h1>", "listen tcp &lt;addr&gt;: in use"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
}

// A window without a frame has only what its page draws: the app's own page moves and closes it too.
func TestAFramelessWindowsOwnPageHasItsButtons(t *testing.T) {
	if strings.Contains(pageHTML("", "t", "d"), msgWindow) {
		t.Error("a framed window's page draws window buttons")
	}
	body := pageHTML(frameless, "t", "d")
	for _, want := range []string{msgWindow, "wails:drag", `data-a="close"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
}
