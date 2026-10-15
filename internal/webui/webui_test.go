package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPA(t *testing.T) {
	app := fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := spa(app)
	for _, tc := range []struct{ path, want, cache string }{
		{"/", "index", "no-cache"},
		{"/teammates/ada", "index", "no-cache"},
		{"/assets/app.js", "console.log", "immutable"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if !strings.Contains(rec.Body.String(), tc.want) || !strings.Contains(rec.Header().Get("Cache-Control"), tc.cache) {
			t.Errorf("%s: body %q cache %q", tc.path, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	}
}

func TestManifestType(t *testing.T) {
	h := spa(fstest.MapFS{
		"index.html":           {Data: []byte("<html>index</html>")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Sandbox Studio"}`)},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if got := rec.Header().Get("Content-Type"); got != "application/manifest+json" || !strings.Contains(rec.Body.String(), "Sandbox Studio") {
		t.Errorf("manifest: type %q body %q", got, rec.Body.String())
	}
}
