package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryPullable(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.URL.Query().Get("scope") != "repository:o/base:pull" || r.URL.Query().Get("service") != "reg" {
				http.Error(w, "bad scope", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"token":"t0k"}`))
		case r.Header.Get("Authorization") != "Bearer t0k":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:o/base:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodHead && r.URL.Path == "/v2/o/base/manifests/sha256:abc":
			if !strings.Contains(r.Header.Get("Accept"), "oci.image.index") {
				w.WriteHeader(http.StatusNotAcceptable)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	r := &Registry{Client: srv.Client(), Scheme: "http"}
	if err := r.Pullable(context.Background(), host+"/o/base@sha256:abc"); err != nil {
		t.Fatal(err)
	}
	if err := r.Pullable(context.Background(), host+"/o/base:v9"); err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("missing tag: %v", err)
	}
}

func TestSplitRef(t *testing.T) {
	for ref, want := range map[string][3]string{
		"ghcr.io/o/base@sha256:1":    {"ghcr.io", "o/base", "sha256:1"},
		"ghcr.io/o/base:v1":          {"ghcr.io", "o/base", "v1"},
		"localhost:5000/base":        {"localhost:5000", "base", "latest"},
		"ghcr.io/o/base:v1@sha256:2": {"ghcr.io", "o/base", "sha256:2"},
	} {
		h, r, ref2, err := splitRef(ref)
		if err != nil || [3]string{h, r, ref2} != want {
			t.Errorf("%s: %s %s %s %v", ref, h, r, ref2, err)
		}
	}
}
