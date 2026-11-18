package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"plain text\n\tindented":         "plain text\n\tindented",
		"\x1b[31mred\x1b[0m":             "red",
		"copy \x1b]52;c;ZXZpbA==\x07 me": "copy  me",
		"title \x1b]2;owned\x1b\\ done":  "title  done",
		"bell\x07":                       "bell␇",
		"lone \x1b":                      "lone ",
		"crlf\r\nline":                   "crlf\nline",
		"50%\r100%":                      "50%\n100%",
		"del\x7f":                        "del␡",
		"nul\x00":                        "nul␀",
		"c1 \u009b31m":                   "c1 �31m",
		"bad \xff utf8":                  "bad � utf8",
		"käse → ✓":                       "käse → ✓",
		"\x1b[2J\x1b[Hcleared":           "cleared",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
		if needsCleaning(in) != (in != want) {
			t.Errorf("needsCleaning(%q) = %v", in, needsCleaning(in))
		}
	}
}

func TestScrubCleansEveryString(t *testing.T) {
	type inner struct {
		Name string
		Data []byte
	}
	var v struct {
		Title  string
		Ptr    *inner
		List   []inner
		Map    map[string]string
		Any    any
		hidden string
	}
	v.Title = "a\x1b[1mb"
	v.Ptr = &inner{Name: "x\x07", Data: []byte("\x1b[1m")}
	v.List = []inner{{Name: "\x1b]0;t\x07n"}}
	v.Map = map[string]string{"k\x1b": "v\x1b[0m"}
	v.Any = map[string]any{"s": "y\x1b[0m", "n": []any{"z\x00"}}
	v.hidden = "\x1b"
	scrub(&v)
	if v.Title != "ab" || v.Ptr.Name != "x␇" || string(v.Ptr.Data) != "\x1b[1m" || v.List[0].Name != "n" {
		t.Errorf("fields: %q %q %q %q", v.Title, v.Ptr.Name, v.Ptr.Data, v.List[0].Name)
	}
	if v.Map["k"] != "v" || len(v.Map) != 1 {
		t.Errorf("map: %q", v.Map)
	}
	m := v.Any.(map[string]any)
	if m["s"] != "y" || m["n"].([]any)[0] != "z␀" {
		t.Errorf("any: %q", m)
	}
	if v.hidden != "\x1b" {
		t.Error("an unexported field changed")
	}
}

// TestClientCleansAComputersText checks that what a computer wrote reaches the TUI clean:
// a file name with an escape in a git status is drawn as its text.
func TestClientCleansAComputersText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/git") {
			_, _ = w.Write([]byte(`{"repos":[{"path":"web","branch":"main\u001b]52;c;ZXZpbA==\u0007","changes":[{"path":"evil\u001b[2J.txt","area":"untracked","status":"added","added":0,"removed":0}]}]}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"no \u001b[31mway"}`))
	}))
	defer srv.Close()
	c := &Client{addr: strings.TrimPrefix(srv.URL, "http://"), http: srv.Client()}
	st, err := c.GitStatus(context.Background(), "e1", "sb1")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Repos[0]; got.Branch != "main" || got.Changes[0].Path != "evil.txt" {
		t.Errorf("status = %+v", got)
	}
	_, err = c.ApprovalsWith(context.Background(), "e1", "approved")
	if e, ok := err.(*APIError); !ok || e.Detail != "no way" {
		t.Errorf("error = %v", err)
	}
}
