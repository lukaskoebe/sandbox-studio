package preview

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"

	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// The patch that lets a module service worker in (see webauth.WorkerPatchPath) goes into
// every page the preview navigates to, as the first thing in its head, so it runs before the
// app's scripts register their worker.
var workerPatchTag = []byte(`<script src="` + webauth.WorkerPatchPath + `?v=2"></script>`)

// maxPatchSearch is how much of a page Studio reads to find where the patch goes.
const maxPatchSearch = 8 << 10

// isNavigation reports a browser loading a page, in a tab or a frame.
func isNavigation(r *http.Request) bool {
	return r != nil && r.Method == http.MethodGet && r.Header.Get("Sec-Fetch-Mode") == "navigate"
}

// patchPage streams a page with workerPatchTag inserted, when the response is a whole HTML
// document; anything else passes unchanged.
func patchPage(resp *http.Response) {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return
	}
	if typ, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || typ != "text/html" {
		return
	}
	body := bufio.NewReaderSize(resp.Body, maxPatchSearch)
	head, _ := body.Peek(maxPatchSearch)
	at := patchAt(head)
	if at < 0 {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{body, resp.Body}
		return
	}
	prefix := make([]byte, 0, at+len(workerPatchTag))
	prefix = append(prefix, head[:at]...)
	prefix = append(prefix, workerPatchTag...)
	_, _ = body.Discard(at)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), body), resp.Body}
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
}

// documentTags find, in order, the tag the patch goes after: the head, else the html element,
// else the doctype. A fragment without any of them, such as an htmx swap, is left alone.
var documentTags = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`),
	regexp.MustCompile(`(?i)<html(?:\s[^>]*)?>`),
	regexp.MustCompile(`(?i)<!doctype[^>]*>`),
}

func patchAt(head []byte) int {
	for _, tag := range documentTags {
		if loc := tag.FindIndex(head); loc != nil {
			return loc[1]
		}
	}
	return -1
}

// staticImport finds the specifiers of a module's static imports and re-exports:
// `import x from "…"`, `import "…"`, `export {x} from "…"`, minified or not. A specifier the
// browser resolves to this preview (a path, or a relative one) gets the worker's capability.
var staticImport = regexp.MustCompile(`(\bfrom\s*|\bimport\s*)(["'])((?:\.{0,2}/)[^"'\s]*)(["'])`)

// withWorkerToken appends the capability to the same-origin static imports of a module worker
// script. Text that only looks like an import, in a string or a comment, gets it too, which
// changes nothing there.
func withWorkerToken(script []byte, token string) []byte {
	return staticImport.ReplaceAllFunc(script, func(m []byte) []byte {
		parts := staticImport.FindSubmatch(m)
		spec := parts[3]
		if parts[2][0] != parts[4][0] || bytes.HasPrefix(spec, []byte("//")) {
			return m
		}
		hash := len(spec)
		if i := bytes.IndexByte(spec, '#'); i >= 0 {
			hash = i
		}
		sep := "?"
		if bytes.IndexByte(spec[:hash], '?') >= 0 {
			sep = "&"
		}
		out := make([]byte, 0, len(m)+len(token)+16)
		out = append(out, parts[1]...)
		out = append(out, parts[2]...)
		out = append(out, spec[:hash]...)
		out = fmt.Appendf(out, "%s%s=%s", sep, webauth.WorkerParam, token)
		out = append(out, spec[hash:]...)
		return append(out, parts[4]...)
	})
}
