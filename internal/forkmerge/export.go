//go:build linux

package forkmerge

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// maxMerge is the largest text file merged three-way.
const maxMerge = 8 << 20

// export collects an archive's header and where its content comes from.
type export struct {
	cfg     Config
	h       *Header
	bases   map[string]func(a *archiveWriter) error // base/<hash>
	bundles map[string]string                       // archive name → bundle file
	tmp     string
}

func newExport(cfg Config, forkID string) *export {
	return &export{
		cfg:     cfg,
		h:       &Header{Version: archiveVersion, ForkID: forkID, Changes: []Change{}, Repos: []RepoChange{}, Installs: []string{}},
		bases:   map[string]func(a *archiveWriter) error{},
		bundles: map[string]string{},
		tmp:     filepath.Join(cfg.state(), "tmp"),
	}
}

// ExportChanges writes the archive of what changed in a fork since its base (merge-export).
// Files of git work trees are compared against their repository's HEAD; their commits travel
// as bundles. forkID must match the base's, or the fork counts as having none.
func ExportChanges(cfg Config, forkID string, w io.Writer) error {
	base, err := LoadBase(cfg, StateBase, forkID)
	if err != nil {
		return err
	}
	now, err := Scan(cfg, base)
	if err != nil {
		return err
	}
	x := newExport(cfg, forkID)
	defer x.cleanup()
	x.h.NoBase = base == nil

	// A repository in the base and in the fork merges by its HEAD; without a base every
	// repository does. Files of other repositories merge against the base like any other.
	byHead := map[string]bool{}
	for _, r := range now.Repos {
		if base == nil || base.repo(r.Path) != nil {
			byHead[r.Path] = true
		}
	}
	for _, r := range now.Repos {
		if byHead[r.Path] {
			if err := x.repoFiles(r, now); err != nil {
				return err
			}
		}
	}
	for _, c := range diffManifests(base, now) {
		if c.Base != nil && byHead[c.Base.Repo] || c.New != nil && byHead[c.New.Repo] {
			continue
		}
		if base == nil && c.New == nil {
			continue // without a base, nothing counts as deleted
		}
		if c.Base != nil && c.New != nil && c.Base.Type == 'f' && c.New.Type == 'f' {
			if blob := cfg.blob(c.Base.Hash); blob != "" {
				c.BaseBlob = true
				size := c.Base.Size
				x.bases[c.Base.Hash] = func(a *archiveWriter) error {
					return a.file("base/"+c.Base.Hash, blob, size, 0o600)
				}
			}
		}
		x.h.Changes = append(x.h.Changes, c)
	}
	if err := x.repos(base, now); err != nil {
		return err
	}
	x.installs(base, now)
	return x.write(w)
}

// ExportReplay writes the archive of what changed in a scratch sandbox since its start
// (merge-replay-export): every path, compared file by file.
func ExportReplay(cfg Config, w io.Writer) error {
	start, err := LoadBase(cfg, StateStart, "")
	if err != nil {
		return err
	}
	if start == nil {
		return errors.New("this sandbox has no merge start")
	}
	now, err := Scan(cfg, start)
	if err != nil {
		return err
	}
	x := newExport(cfg, start.ForkID)
	defer x.cleanup()
	x.h.Changes = diffManifests(start, now)
	if err := x.repos(start, now); err != nil {
		return err
	}
	x.installs(start, now)
	return x.write(w)
}

// diffManifests lists the paths whose content differs between base and now, sorted.
func diffManifests(base, now *Manifest) []Change {
	var out []Change
	for i := range now.Entries {
		e := &now.Entries[i]
		if b := base.entry(e.Path); !same(b, e) {
			out = append(out, Change{Path: e.Path, Base: b, New: e})
		}
	}
	if base != nil {
		for i := range base.Entries {
			b := &base.Entries[i]
			if now.entry(b.Path) == nil {
				out = append(out, Change{Path: b.Path, Base: b})
			}
		}
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// repoFiles adds the uncommitted changes of the work tree r: its files against its HEAD.
func (x *export) repoFiles(r Repo, now *Manifest) error {
	dir, err := x.cfg.repoDir(r.Path)
	if err != nil {
		return err
	}
	head := map[string]treeEntry{}
	var names []string
	if _, err := x.cfg.git(dir, "rev-parse", "-q", "--verify", "HEAD^{commit}"); err == nil {
		if head, err = x.cfg.lsTree(dir); err != nil {
			return fmt.Errorf("%s: %w", r.Path, err)
		}
		if names, err = x.cfg.gitList(dir, "diff", "--no-renames", "--name-only", "--no-ext-diff", "--no-textconv", "-z", "HEAD", "--"); err != nil {
			return fmt.Errorf("%s: %w", r.Path, err)
		}
		untracked, err := x.cfg.gitList(dir, "ls-files", "-o", "--exclude-standard", "-z")
		if err != nil {
			return fmt.Errorf("%s: %w", r.Path, err)
		}
		names = append(names, untracked...)
	} else {
		// No commit yet: every file is new.
		for _, e := range now.Entries {
			if e.Repo == r.Path {
				names = append(names, strings.TrimPrefix(e.Path, r.Path+"/"))
			}
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	for _, rel := range names {
		if !validRel(rel) {
			continue
		}
		p := r.Path + "/" + rel
		if tree, hrel, _ := strings.Cut(p, "/"); tree == TreeHome && slices.ContainsFunc(homeSkipped, func(h string) bool { return under(hrel, h) }) {
			continue
		}
		var base *Entry
		var text bool
		if t, ok := head[rel]; ok {
			if t.mode == "160000" {
				continue // a submodule
			}
			if base, text, err = x.cfg.headEntry(dir, p, t); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		var prev *Entry
		if e := now.entry(p); e != nil {
			prev = e
		}
		cur, err := stat(abs, p, prev)
		if err != nil {
			return err
		}
		if cur != nil {
			cur.Repo = r.Path
		}
		if base == nil && cur == nil || same(base, cur) {
			continue
		}
		c := Change{Path: p, Base: base, New: cur}
		if base != nil && base.Type == 'f' && text && base.Size <= maxMerge && cur != nil && cur.Type == 'f' {
			c.BaseBlob = true
			t, size, hash := head[rel], base.Size, base.Hash
			x.bases[hash] = func(a *archiveWriter) error { return x.cfg.writeBlob(a, dir, "base/"+hash, t.oid, size) }
		}
		x.h.Changes = append(x.h.Changes, c)
	}
	return nil
}

// repos adds the branches that are new or moved since base, and bundles their new commits.
func (x *export) repos(base, now *Manifest) error {
	for _, r := range now.Repos {
		b := base.repo(r.Path)
		rc := RepoChange{Path: r.Path, New: b == nil, Head: r.Head}
		var old []string
		if b != nil {
			rc.OldHead = b.Head
			for _, sha := range b.Branches {
				old = append(old, sha)
			}
			if !strings.HasPrefix(b.Head, "refs/") && b.Head != "" {
				old = append(old, b.Head)
			}
		}
		refs := slices.Sorted(maps.Keys(r.Branches))
		for _, ref := range refs {
			sha := r.Branches[ref]
			var was string
			if b != nil {
				was = b.Branches[ref]
			}
			if was != sha {
				rc.Branches = append(rc.Branches, BranchChange{Ref: ref, Old: was, New: sha})
			}
		}
		if len(rc.Branches) == 0 && rc.Head == rc.OldHead && !rc.New {
			continue
		}
		if rc.Branches == nil {
			rc.Branches = []BranchChange{}
		}
		dir, err := x.cfg.repoDir(r.Path)
		if err != nil {
			return err
		}
		// Commits the receiving repository has: the base's tips that are still here.
		var have []string
		for _, sha := range slices.Compact(slices.Sorted(slices.Values(old))) {
			if _, err := x.cfg.git(dir, "cat-file", "-e", sha+"^{commit}"); err == nil {
				have = append(have, sha)
			}
		}
		var bundled []string
		for _, bc := range rc.Branches {
			args := append([]string{"rev-list", "--count", bc.New, "--not"}, have...)
			out, err := x.cfg.git(dir, append(args, "--")...)
			if err != nil {
				return fmt.Errorf("%s: %w", r.Path, err)
			}
			if strings.TrimSpace(string(out)) != "0" {
				bundled = append(bundled, bc.Ref)
			}
		}
		if len(bundled) > 0 {
			if err := os.MkdirAll(x.tmp, 0o700); err != nil {
				return err
			}
			name := fmt.Sprintf("bundles/%d.bundle", len(x.bundles))
			file := filepath.Join(x.tmp, fmt.Sprintf("%d.bundle", len(x.bundles)))
			args := append([]string{"bundle", "create", "-q", file}, bundled...)
			if len(have) > 0 {
				args = append(append(args, "--not"), have...)
			}
			if _, err := x.cfg.git(dir, args...); err != nil {
				return fmt.Errorf("%s: %w", r.Path, err)
			}
			rc.Bundle = name
			x.bundles[name] = file
		}
		x.h.Repos = append(x.h.Repos, rc)
	}
	return nil
}

// installs adds the tool installs base doesn't have.
func (x *export) installs(base, now *Manifest) {
	var had []string
	if base != nil {
		had = base.Installs
	}
	for _, p := range now.Installs {
		if !slices.Contains(had, p) {
			x.h.Installs = append(x.h.Installs, p)
		}
	}
}

// write writes the archive.
func (x *export) write(w io.Writer) error {
	a := newArchiveWriter(w)
	if err := a.header(x.h); err != nil {
		return err
	}
	for _, hash := range slices.Sorted(maps.Keys(x.bases)) {
		if err := x.bases[hash](a); err != nil {
			return fmt.Errorf("base of %s: %w", hash, err)
		}
	}
	for _, c := range x.h.Changes {
		if c.New == nil || c.New.Type != 'f' {
			continue
		}
		abs, err := x.cfg.abs(c.Path)
		if err != nil {
			return err
		}
		if err := a.file("new/"+c.Path, abs, c.New.Size, fs.FileMode(c.New.Mode)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Gone since the scan: an empty file, whose hash Apply finds wrong.
				if err := a.bytes("new/"+c.Path, nil); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%s: %w", c.Path, err)
		}
	}
	for _, p := range x.h.Installs {
		abs, err := x.cfg.abs(p)
		if err != nil {
			return err
		}
		if err := a.tree("installs/"+p, abs); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(x.bundles)) {
		file := x.bundles[name]
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		if err := a.file(name, file, info.Size(), 0o600); err != nil {
			return err
		}
	}
	return a.Close()
}

func (x *export) cleanup() { _ = os.RemoveAll(x.tmp) }

// --- git objects ---------------------------------------------------------------------------

// repoDir is the directory of the repository at manifest path p.
func (c Config) repoDir(p string) (string, error) {
	if p == TreeWorkspace {
		return c.Workspace, nil
	}
	return c.abs(p)
}

type treeEntry struct {
	mode, oid string
	size      int64
}

// lsTree lists the files of HEAD.
func (c Config) lsTree(dir string) (map[string]treeEntry, error) {
	cmd := c.gitCmd(dir, "ls-tree", "-r", "-l", "-z", "--full-tree", "HEAD")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	entries := map[string]treeEntry{}
	br := bufio.NewReader(out)
	for {
		rec, err := br.ReadString(0)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return nil, err
		}
		// <mode> SP <type> SP <oid> SP+ <size> TAB <path> NUL
		meta, name, ok := strings.Cut(strings.TrimSuffix(rec, "\x00"), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 {
			continue
		}
		size, _ := strconv.ParseInt(fields[3], 10, 64)
		entries[name] = treeEntry{mode: fields[0], oid: fields[2], size: size}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	return entries, nil
}

// headEntry describes the HEAD version t of p, and whether it is text.
func (c Config) headEntry(dir, p string, t treeEntry) (*Entry, bool, error) {
	cmd := c.gitCmd(dir, "cat-file", "blob", t.oid)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	h := sha256.New()
	head := make([]byte, 8000)
	n, _ := io.ReadFull(out, head)
	h.Write(head[:n])
	_, err = io.Copy(h, out)
	if werr := cmd.Wait(); err == nil {
		err = werr
	}
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file %s: %w", t.oid, err)
	}
	e := &Entry{Path: p, Type: 'f', Mode: 0o644, Size: t.size, Hash: hex.EncodeToString(h.Sum(nil))}
	switch t.mode {
	case "100755":
		e.Mode = 0o755
	case "120000":
		e = &Entry{Path: p, Type: 'l', Mode: 0o777, Target: string(head[:n])}
	}
	return e, isText(head[:n]), nil
}

// writeBlob adds the blob oid, of size bytes, to the archive as name.
func (c Config) writeBlob(a *archiveWriter, dir, name, oid string, size int64) error {
	cmd := c.gitCmd(dir, "cat-file", "blob", oid)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := a.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, Typeflag: tar.TypeReg}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	_, err = io.CopyN(a.tw, out, size)
	if werr := cmd.Wait(); err == nil {
		err = werr
	}
	return err
}
