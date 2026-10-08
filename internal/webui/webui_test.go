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
		{"/personas/ada", "index", "no-cache"},
		{"/assets/app.js", "console.log", "immutable"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if !strings.Contains(rec.Body.String(), tc.want) || !strings.Contains(rec.Header().Get("Cache-Control"), tc.cache) {
			t.Errorf("%s: body %q cache %q", tc.path, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	}
}
