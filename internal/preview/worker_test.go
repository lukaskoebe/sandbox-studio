package preview

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWithWorkerTokenTagsSameOriginImports(t *testing.T) {
	in := `import {a} from "/sw/a.js";import b from './b.js?v=1';import"../c.js#x";export {d} from "./d.js";` +
		`import e from "https://cdn.example/e.js";import f from "//cdn.example/f.js";const s = "from here"`
	want := `import {a} from "/sw/a.js?__studio_sw=T";import b from './b.js?v=1&__studio_sw=T';import"../c.js?__studio_sw=T#x";export {d} from "./d.js?__studio_sw=T";` +
		`import e from "https://cdn.example/e.js";import f from "//cdn.example/f.js";const s = "from here"`
	if got := string(withWorkerToken([]byte(in), "T")); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestPatchPageGoesIntoDocumentsOnly(t *testing.T) {
	tag := string(workerPatchTag)
	for _, tc := range []struct{ typ, in, want string }{
		{"text/html", `<!doctype html><html lang="en"><head><title>x</title>`, `<!doctype html><html lang="en"><head>` + tag + `<title>x</title>`},
		{"text/html; charset=utf-8", `<!DOCTYPE html><header>h</header>`, `<!DOCTYPE html>` + tag + `<header>h</header>`},
		{"text/html", `<HTML><body>`, `<HTML>` + tag + `<body>`},
		{"text/html", `<div hx-swap-oob="true">fragment</div>`, `<div hx-swap-oob="true">fragment</div>`},
		{"application/json", `{"html":"<head>"}`, `{"html":"<head>"}`},
	} {
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {tc.typ}}, Body: io.NopCloser(strings.NewReader(tc.in))}
		patchPage(resp)
		got, _ := io.ReadAll(resp.Body)
		if string(got) != tc.want {
			t.Errorf("%s %q: %q", tc.typ, tc.in, got)
		}
	}
	// A page longer than what is read to find the head streams on whole.
	long := "<!doctype html><head>" + strings.Repeat("x", 3*maxPatchSearch)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}, "Content-Length": {"1"}}, Body: io.NopCloser(strings.NewReader(long))}
	patchPage(resp)
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "<!doctype html><head>"+tag+strings.Repeat("x", 3*maxPatchSearch) || resp.Header.Get("Content-Length") != "" {
		t.Fatalf("long page: %d bytes, length %q", len(got), resp.Header.Get("Content-Length"))
	}
}
