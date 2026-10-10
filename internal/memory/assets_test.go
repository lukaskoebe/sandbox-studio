package memory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fakeLlamaTarGz is shaped like a llama.cpp release: one top-level directory.
func fakeLlamaTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "llama-b11429/", Typeflag: tar.TypeDir, Mode: 0o755})
	body := []byte("not really a library")
	tw.WriteHeader(&tar.Header{Name: "llama-b11429/libllama.so", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	tw.Write(body)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	return buf.Bytes()
}

type assetServer struct {
	*httptest.Server
	files map[string][]byte
	hits  map[string]*atomic.Int32
}

func newAssetServer(t *testing.T, files map[string][]byte) *assetServer {
	s := &assetServer{files: files, hits: map[string]*atomic.Int32{}}
	for name := range files {
		s.hits[name] = &atomic.Int32{}
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		b, ok := s.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet { // go-getter also sends HEAD requests
			s.hits[name].Add(1)
		}
		w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAssetsEnsure(t *testing.T) {
	model := []byte("GGUF fake model bytes")
	libs := fakeLlamaTarGz(t)
	srv := newAssetServer(t, map[string][]byte{"model.gguf": model, "llama-b11429-bin-ubuntu-x64.tar.gz": libs})
	dir := t.TempDir()
	a := Assets{
		Dir:    dir,
		Model:  Asset{URL: srv.URL + "/model.gguf", SHA256: sum(model)},
		Libs:   map[string]Asset{"linux/amd64": {URL: srv.URL + "/llama-b11429-bin-ubuntu-x64.tar.gz", SHA256: sum(libs)}},
		GOOS:   "linux",
		GOARCH: "amd64",
	}
	libDir, modelPath, err := a.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if libDir != filepath.Join(dir, "llama-"+LlamaTag) || modelPath != filepath.Join(dir, "model.gguf") {
		t.Fatalf("paths %s %s", libDir, modelPath)
	}
	if b, err := os.ReadFile(filepath.Join(libDir, "libllama.so")); err != nil || string(b) != "not really a library" {
		t.Fatalf("lib %q %v", b, err)
	}
	if b, _ := os.ReadFile(modelPath); !bytes.Equal(b, model) {
		t.Fatal("model content")
	}
	if _, err := os.Stat(libDir + ".part"); !os.IsNotExist(err) {
		t.Fatal("staging dir left behind")
	}
	if _, err := os.Stat(filepath.Join(libDir, "llama-b11429-bin-ubuntu-x64.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("archive left behind")
	}

	// A second run downloads nothing.
	if _, _, err := a.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, n := range srv.hits {
		if n.Load() != 1 {
			t.Fatalf("%s fetched %d times", name, n.Load())
		}
	}

	// A new pin re-downloads.
	model2 := []byte("GGUF newer model")
	srv.files["model.gguf"] = model2
	a.Model.SHA256 = sum(model2)
	if _, _, err := a.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(modelPath); !bytes.Equal(b, model2) {
		t.Fatal("model not replaced")
	}
}

func TestAssetsChecksumMismatch(t *testing.T) {
	libs := fakeLlamaTarGz(t)
	srv := newAssetServer(t, map[string][]byte{"model.gguf": []byte("tampered"), "libs.tar.gz": libs})
	dir := t.TempDir()
	a := Assets{
		Dir:    dir,
		Model:  Asset{URL: srv.URL + "/model.gguf", SHA256: sum([]byte("original"))},
		Libs:   map[string]Asset{"linux/arm64": {URL: srv.URL + "/libs.tar.gz", SHA256: sum(libs)}},
		GOOS:   "linux",
		GOARCH: "arm64",
	}
	_, _, err := a.Ensure(context.Background())
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("model: %v", err)
	}
	for _, f := range []string{"model.gguf", "model.gguf.part", "model.gguf.sha256"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a failed download", f)
		}
	}

	// Tampered libraries are rejected by yzma and leave nothing installed.
	dir = t.TempDir()
	a.Dir = dir
	a.Libs["linux/arm64"] = Asset{URL: srv.URL + "/libs.tar.gz", SHA256: sum([]byte("other"))}
	if _, _, err := a.Ensure(context.Background()); err == nil {
		t.Fatal("tampered libraries accepted")
	}
	for _, d := range []string{"llama-" + LlamaTag, "llama-" + LlamaTag + ".part"} {
		if _, err := os.Stat(filepath.Join(dir, d)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a failed install", d)
		}
	}

	a.GOOS = "plan9"
	if _, _, err := a.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("unsupported platform: %v", err)
	}
}

func TestPinnedAssetsCoverPlatforms(t *testing.T) {
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/arm64", "darwin/amd64", "windows/amd64", "windows/arm64"} {
		a, ok := LlamaAssets[p]
		if !ok || len(a.SHA256) != 64 || !strings.HasPrefix(a.URL, "https://") {
			t.Errorf("%s: %+v", p, a)
		}
	}
	if len(ModelAsset.SHA256) != 64 || !strings.Contains(ModelAsset.URL, ModelRevision) {
		t.Errorf("model %+v", ModelAsset)
	}
}
