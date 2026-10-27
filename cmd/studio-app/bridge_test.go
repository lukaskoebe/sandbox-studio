package main

import (
	"strings"
	"testing"
)

func TestBridgeTakesStudiosMessagesOnly(t *testing.T) {
	var opened []string
	var notices []notice
	b := &bridge{
		base:   "http://127.0.0.1:7878",
		open:   func(u string) error { opened = append(opened, u); return nil },
		notify: func(n notice) error { notices = append(notices, n); return nil },
	}
	studio := "http://127.0.0.1:7878"
	for _, m := range []struct{ msg, origin string }{
		{"studio:open https://github.com/login", studio},
		{"studio:open http://web-3000.dev.localhost:7878/", studio},
		{"studio:open https://evil.example/", "https://evil.example"},  // not Studio's page
		{"studio:open https://evil.example/", "http://127.0.0.1:7879"}, // another port
		{"studio:open file:///etc/passwd", studio},
		{"studio:open javascript:alert(1)", studio},
		{`studio:notify {"id":"a1","title":"Approve a command","body":"dev: rm -rf build"}`, studio},
		{`studio:notify {"body":"no title"}`, studio},
	} {
		_ = b.handle(m.msg, m.origin)
	}
	if strings.Join(opened, " ") != "https://github.com/login http://web-3000.dev.localhost:7878/" {
		t.Fatalf("opened %v", opened)
	}
	if len(notices) != 1 || notices[0].ID != "a1" || notices[0].Body != "dev: rm -rf build" {
		t.Fatalf("notices %+v", notices)
	}
}

func TestAppLinkKeepsTheLoginFragment(t *testing.T) {
	got, err := appLink("http://127.0.0.1:7878/#studio-login=abc", true)
	if err != nil || got != "http://127.0.0.1:7878/?studio-app=notify#studio-login=abc" {
		t.Fatalf("appLink = %q, %v", got, err)
	}
	if got, _ := appLink("http://127.0.0.1:7878/#studio-login=abc", false); got != "http://127.0.0.1:7878/?studio-app=1#studio-login=abc" {
		t.Fatalf("without notifications: %q", got)
	}
}
