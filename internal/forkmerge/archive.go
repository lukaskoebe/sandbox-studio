//go:build linux

package forkmerge

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// An archive is a gzipped tar: header.json first, then
//
//	base/<hash>          the base content of a changed text file
//	new/<path>           the content of a changed or added file
//	installs/<path>/...  a tool install, whole
//	bundles/<n>.bundle   the new commits of a repository
const archiveVersion = 1

// Header lists what an archive holds.
type Header struct {
	Version  int          `json:"version"`
	ForkID   string       `json:"forkId,omitempty"`
	NoBase   bool         `json:"noBase,omitempty"` // the fork kept no base: every file counts as added
	Changes  []Change     `json:"changes"`
	Repos    []RepoChange `json:"repos"`
	Installs []string     `json:"installs"`
}

// Change is a path that changed: Base is what it was (nil: absent), New what it is now (nil:
// deleted).
type Change struct {
	Path     string `json:"path"`
	Base     *Entry `json:"base,omitempty"`
	New      *Entry `json:"new,omitempty"`
	BaseBlob bool   `json:"baseBlob,omitempty"` // the archive holds base/<Base.Hash>
}

// RepoChange is a repository whose branches changed.
type RepoChange struct {
	Path     string         `json:"path"`
	New      bool           `json:"new,omitempty"`     // the repository isn't in the base
	Head     string         `json:"head,omitempty"`    // its HEAD now
	OldHead  string         `json:"oldHead,omitempty"` // its HEAD at the base
	Bundle   string         `json:"bundle,omitempty"`  // archive name of the bundle, if any commits are new
	Branches []BranchChange `json:"branches"`
}

// BranchChange is a branch that is new (Old empty) or moved.
type BranchChange struct {
	Ref string `json:"ref"` // refs/heads/<branch>
	Old string `json:"old,omitempty"`
	New string `json:"new"`
}

// --- writing -------------------------------------------------------------------------------

type archiveWriter struct {
	zw *gzip.Writer
	tw *tar.Writer
}

func newArchiveWriter(w io.Writer) *archiveWriter {
	zw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	return &archiveWriter{zw: zw, tw: tar.NewWriter(zw)}
}

func (a *archiveWriter) header(h *Header) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return a.bytes("header.json", b)
}

func (a *archiveWriter) bytes(name string, b []byte) error {
	if err := a.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := a.tw.Write(b)
	return err
}

// file adds the file at abs as name. A file that shrank since it was stat'ed is padded and
// one that grew is cut, so the archive stays readable; its hash then tells Apply.
func (a *archiveWriter) file(name, abs string, size int64, mode fs.FileMode) error {
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := a.tw.WriteHeader(&tar.Header{Name: name, Mode: int64(mode.Perm()), Size: size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(a.tw, io.LimitReader(f, size))
	if err != nil {
		return err
	}
	if n < size {
		_, err = io.CopyN(a.tw, zeros{}, size-n)
	}
	return err
}

// tree adds the directory or file at abs, recursively, under name.
func (a *archiveWriter) tree(name, abs string) error {
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(abs, p)
		n := name
		if rel != "." {
			n = path.Join(name, filepath.ToSlash(rel))
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		switch {
		case d.IsDir():
			return a.tw.WriteHeader(&tar.Header{Name: n + "/", Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir, ModTime: info.ModTime()})
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return nil
			}
			return a.tw.WriteHeader(&tar.Header{Name: n, Linkname: target, Typeflag: tar.TypeSymlink, ModTime: info.ModTime()})
		case d.Type().IsRegular():
			return a.file(n, p, info.Size(), info.Mode())
		}
		return nil
	})
}

func (a *archiveWriter) Close() error {
	if err := a.tw.Close(); err != nil {
		return err
	}
	return a.zw.Close()
}

type zeros struct{}

func (zeros) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

// --- reading -------------------------------------------------------------------------------

// readHeader opens an archive and reads its header.
func readHeader(r io.Reader) (*Header, *tar.Reader, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("read archive: %w", err)
	}
	tr := tar.NewReader(zr)
	th, err := tr.Next()
	if err != nil {
		return nil, nil, fmt.Errorf("read archive: %w", err)
	}
	if th.Name != "header.json" || th.Size > 512<<20 {
		return nil, nil, errors.New("read archive: no header")
	}
	var h Header
	if err := json.NewDecoder(tr).Decode(&h); err != nil {
		return nil, nil, fmt.Errorf("read archive header: %w", err)
	}
	if h.Version != archiveVersion {
		return nil, nil, fmt.Errorf("archive version %d, want %d", h.Version, archiveVersion)
	}
	for _, c := range h.Changes {
		if _, _, ok := splitPath(c.Path); !ok {
			return nil, nil, fmt.Errorf("archive: invalid path %q", c.Path)
		}
	}
	for _, p := range h.Installs {
		if _, _, ok := splitPath(p); !ok {
			return nil, nil, fmt.Errorf("archive: invalid install %q", p)
		}
	}
	for _, rc := range h.Repos {
		if _, _, ok := splitPath(rc.Path); !ok && rc.Path != TreeWorkspace {
			return nil, nil, fmt.Errorf("archive: invalid repository %q", rc.Path)
		}
	}
	return &h, tr, nil
}

// extract unpacks the rest of an archive. place says where each entry goes: under root, at
// rel; an empty root leaves it out. Names are checked, no entry is written through a symlink,
// and only installs may hold symlinks.
func extract(tr *tar.Reader, place func(name string) (root, rel string)) error {
	for {
		th, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		name := strings.TrimSuffix(th.Name, "/")
		if !validRel(name) {
			return fmt.Errorf("archive: invalid name %q", th.Name)
		}
		dir, rel := place(name)
		if dir == "" {
			continue
		}
		if !validRel(rel) {
			return fmt.Errorf("archive: invalid name %q", th.Name)
		}
		if err := checkParents(dir, rel); err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		switch th.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(dst, fs.FileMode(th.Mode).Perm()|0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case tar.TypeSymlink:
			if !strings.HasPrefix(name, "installs/") {
				return fmt.Errorf("archive: symlink %q outside an install", name)
			}
			if err := os.Symlink(th.Linkname, dst); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(th.Mode).Perm()|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive: unsupported entry %q", name)
		}
	}
}

// checkParents fails if a directory between root and rel exists and isn't a plain directory.
func checkParents(root, rel string) error {
	dir := root
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s: not a directory", dir)
		}
	}
	return nil
}

// splitPath splits a manifest path into its tree and relative path.
func splitPath(p string) (tree, rel string, ok bool) {
	tree, rel, ok = strings.Cut(p, "/")
	return tree, rel, ok && (tree == TreeWorkspace || tree == TreeHome) && validRel(rel)
}
