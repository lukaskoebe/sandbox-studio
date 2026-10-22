//go:build linux

package forkmerge

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// The states a sandbox keeps under cfg.state().
const (
	StateBase  = "base"  // a fork's state when it was made, which its changes are found against
	StateStart = "start" // a scratch sandbox's state before the merge, which Accept replays against
)

// RecordBase scans the trees and keeps the manifest as state name, with forkID. The base also
// keeps the content of its text files outside git work trees, up to maxBlob each and maxBlobs in
// all, for three-way merges. A previous state of that name is replaced.
func RecordBase(cfg Config, name, forkID string) (*Manifest, error) {
	if name != StateBase && name != StateStart {
		return nil, fmt.Errorf("unknown state %q", name)
	}
	m, err := Scan(cfg, nil)
	if err != nil {
		return nil, err
	}
	m.ForkID = forkID
	dir := filepath.Join(cfg.state(), name)
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(tmp, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if name == StateBase {
		if err := keepBlobs(cfg, m, filepath.Join(tmp, "blobs")); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
	}
	if err := writeManifest(filepath.Join(tmp, "manifest.json.gz"), m); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	return m, nil
}

// keepBlobs copies the base's text files outside work trees into blobs, by hash.
func keepBlobs(cfg Config, m *Manifest, blobs string) error {
	var total int64
	for _, e := range m.Entries {
		if e.Type != 'f' || e.Repo != "" || e.Session || e.Hash == "" || e.Size > maxBlob || total+e.Size > maxBlobs {
			continue
		}
		dst := filepath.Join(blobs, e.Hash)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		abs, err := cfg.abs(e.Path)
		if err != nil {
			continue
		}
		b, err := readSmall(abs, maxBlob)
		if err != nil || !isText(b) || hashBytes(b) != e.Hash {
			continue // changed or unreadable since the scan: no content for it
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
		total += int64(len(b))
	}
	return nil
}

// LoadBase reads state name, or nil if there is none or, when forkID isn't empty, it was
// recorded for another fork.
func LoadBase(cfg Config, name, forkID string) (*Manifest, error) {
	f, err := os.Open(filepath.Join(cfg.state(), name, "manifest.json.gz"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.NewDecoder(zr).Decode(&m); err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if m.Version != manifestVers || forkID != "" && m.ForkID != forkID {
		return nil, nil
	}
	return &m, nil
}

// blob is the path of the base's content of hash, or "" if the base doesn't keep it.
func (c Config) blob(hash string) string {
	if hash == "" {
		return ""
	}
	p := filepath.Join(c.state(), StateBase, "blobs", hash)
	if _, err := os.Lstat(p); err != nil {
		return ""
	}
	return p
}

func writeManifest(path string, m *Manifest) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	err = json.NewEncoder(zw).Encode(m)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readSmall reads the file at abs if it is at most limit bytes.
func readSmall(abs string, limit int64) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", abs, limit)
	}
	return b, nil
}
