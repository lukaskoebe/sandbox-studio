package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hybridgroup/yzma/pkg/download"
)

// Pinned embedding assets: the llama.cpp build of spike S9 (yzma v1.29.1's default,
// llama-cpp-builder v0.6.0, whose manifest pins upstream build b11429) and embeddinggemma
// at a fixed Hugging Face revision. Bumping either means new URLs and checksums here; a
// new model revision also changes ModelName, which re-embeds everything in the background.
const (
	LlamaTag      = "b11429"
	ModelRevision = "0f741b5a6585bd53aeb15cd1372c56f2a0f65e12"
	ModelFile     = "embeddinggemma-300M-Q8_0.gguf"
	ModelName     = "embeddinggemma-300m-q8_0@0f741b5"
)

// Asset is a file to download and the SHA-256 it must have.
type Asset struct {
	URL    string
	SHA256 string
}

// ModelAsset is the GGUF (334 MB).
var ModelAsset = Asset{
	URL:    "https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF/resolve/" + ModelRevision + "/" + ModelFile,
	SHA256: "b5ce9d77a3fc4b3b39ccb5643c36777911cc4eb46a66962eadfa3f5f60490d63",
}

const llamaRelease = "https://github.com/ggml-org/llama.cpp/releases/download/" + LlamaTag + "/llama-" + LlamaTag

// LlamaAssets are the prebuilt llama.cpp libraries by GOOS/GOARCH: CPU builds, which on
// Apple silicon include Metal. Digests come from the llama-cpp-builder v0.6.0 manifest.
var LlamaAssets = map[string]Asset{
	"linux/amd64":   {llamaRelease + "-bin-ubuntu-x64.tar.gz", "f6d25dde8f51133143d1453da4fd5f73b145127177612a283bf7995957af3392"},
	"linux/arm64":   {"https://github.com/hybridgroup/llama-cpp-builder/releases/download/v0.6.0/llama-v0.6.0-bin-ubuntu-cpu-arm64.tar.gz", "296e1cac3c68912c8d77bbaea9a51b2c19aecf67173b7d2d1ff52d6ab4c5d8de"},
	"darwin/arm64":  {llamaRelease + "-bin-macos-arm64.tar.gz", "740288ec6887be94280a5dfa25b5e23a78285cab104519e6c7e218904ee82459"},
	"darwin/amd64":  {llamaRelease + "-bin-macos-x64.tar.gz", "29ac3ea02be6bd143e824973f2cc5fa74bc4094393a9eaab0ff6814f19dd8522"},
	"windows/amd64": {llamaRelease + "-bin-win-cpu-x64.zip", "1283323272b04cd07905816a597a0da810918102de958f4ff6f7bbaa70ed2efe"},
	"windows/arm64": {llamaRelease + "-bin-win-cpu-arm64.zip", "ee0f631a9e58b146ff50714099d9cb498906af3143a773b580093a9214a8d1e5"},
}

// Assets downloads the embedding model and llama.cpp into Dir, verifying checksums.
type Assets struct {
	Dir    string
	Model  Asset
	Libs   map[string]Asset
	GOOS   string // defaults to runtime.GOOS
	GOARCH string // defaults to runtime.GOARCH
	Client *http.Client
}

// DefaultAssets are the pinned assets, kept under dir (Studio's data dir).
func DefaultAssets(dir string) Assets {
	return Assets{Dir: dir, Model: ModelAsset, Libs: LlamaAssets}
}

// ErrChecksum means a download did not match its pinned SHA-256.
var ErrChecksum = errors.New("checksum mismatch")

// Ensure downloads what is missing and returns the llama.cpp library directory and the
// model path. Completed downloads are recognized by a recorded checksum, so a restart
// neither re-downloads nor re-hashes them.
func (a Assets) Ensure(ctx context.Context) (libDir, modelPath string, err error) {
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return "", "", err
	}
	if libDir, err = a.ensureLibs(ctx); err != nil {
		return "", "", fmt.Errorf("llama.cpp libraries: %w", err)
	}
	modelPath = filepath.Join(a.Dir, filepath.Base(strings.SplitN(a.Model.URL, "?", 2)[0]))
	if err := a.ensureFile(ctx, a.Model, modelPath); err != nil {
		return "", "", fmt.Errorf("embedding model: %w", err)
	}
	return libDir, modelPath, nil
}

func (a Assets) target() (string, string) {
	goos, goarch := a.GOOS, a.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// pinned resolves exactly one asset with its digest, so yzma installs what we pinned.
type pinned Asset

func (p pinned) Resolve(download.Target) ([]string, error) { return []string{p.URL}, nil }
func (p pinned) ResolveAssets(download.Target) ([]download.Asset, error) {
	return []download.Asset{{URL: p.URL, SHA256: p.SHA256}}, nil
}

func (a Assets) ensureLibs(ctx context.Context) (string, error) {
	goos, goarch := a.target()
	asset, ok := a.Libs[goos+"/"+goarch]
	if !ok {
		return "", fmt.Errorf("no prebuilt llama.cpp for %s/%s", goos, goarch)
	}
	dir := filepath.Join(a.Dir, "llama-"+LlamaTag)
	if rec, err := download.ReadInstallRecord(dir); err == nil && len(rec.Assets) == 1 && strings.EqualFold(rec.Assets[0].SHA256, asset.SHA256) {
		return dir, nil
	}
	arch, err := download.ParseArch(goarch)
	if err != nil {
		return "", err
	}
	opsys, err := download.ParseOS(goos)
	if err != nil {
		return "", err
	}
	// Install into a staging directory, so an interrupted download is never mistaken for
	// a complete one.
	staging := dir + ".part"
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return "", err
	}
	target := download.Target{Arch: arch, OS: opsys, Processor: download.CPU, Version: LlamaTag, UpstreamVersion: LlamaTag}
	if err := download.Install(ctx, target, staging, nil, pinned(asset), download.WithVerify(download.VerifyRequired)); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.Rename(staging, dir)
}

func (a Assets) ensureFile(ctx context.Context, asset Asset, path string) error {
	marker := path + ".sha256"
	if b, err := os.ReadFile(marker); err == nil && strings.EqualFold(strings.TrimSpace(string(b)), asset.SHA256) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", asset.URL, resp.Status)
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, asset.SHA256) {
		os.Remove(part)
		return fmt.Errorf("%w: %s has sha256 %s, want %s", ErrChecksum, asset.URL, got, asset.SHA256)
	}
	if err := os.Rename(part, path); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(asset.SHA256+"\n"), 0o600)
}
