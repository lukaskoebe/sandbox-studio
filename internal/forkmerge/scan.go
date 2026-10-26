//go:build linux

package forkmerge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The trees a merge covers.
const (
	TreeWorkspace = "workspace"
	TreeHome      = "home"
)

// Limits.
const (
	maxHashed    = 64 << 20  // larger files are compared by size and mtime
	maxBlob      = 1 << 20   // text files up to this size keep their base content
	maxBlobs     = 512 << 20 // the base's content in all
	maxEntries   = 500_000   // files in one scan
	manifestVers = 1
)

// stagingDir is where an archive is unpacked, under the workspace. Its .gitignore of "*"
// keeps git from seeing it.
const stagingDir = ".studio-merge"

// Config names the trees and the git binary.
type Config struct {
	Workspace string // /workspace
	Home      string // /home/agent
	Git       string // "git" when empty
}

func (c Config) root(tree string) string {
	if tree == TreeHome {
		return c.Home
	}
	return c.Workspace
}

// abs is the file a manifest path names: "workspace/src/a.go" is /workspace/src/a.go.
func (c Config) abs(p string) (string, error) {
	tree, rel, ok := strings.Cut(p, "/")
	if !ok || (tree != TreeWorkspace && tree != TreeHome) || !validRel(rel) {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return filepath.Join(c.root(tree), filepath.FromSlash(rel)), nil
}

func (c Config) state() string   { return filepath.Join(c.Home, ".local/state/studio/merge") }
func (c Config) staging() string { return filepath.Join(c.Workspace, stagingDir) }

// validRel reports whether rel is a clean relative slash path inside its tree.
func validRel(rel string) bool {
	return rel != "" && rel != "." && path.Clean(rel) == rel && !path.IsAbs(rel) &&
		rel != ".." && !strings.HasPrefix(rel, "../") && !strings.ContainsRune(rel, 0)
}

// Entry is a file or symlink of a manifest.
type Entry struct {
	Path    string `json:"p"`           // tree and relative path: "workspace/src/a.go"
	Type    byte   `json:"t"`           // 'f' file, 'l' symlink
	Mode    uint32 `json:"m"`           // permission bits
	Size    int64  `json:"s,omitempty"` // of a file
	MTime   int64  `json:"n,omitempty"` // nanoseconds
	Hash    string `json:"h,omitempty"` // SHA-256 of a file's content; empty above maxHashed
	Target  string `json:"l,omitempty"` // of a symlink
	Repo    string `json:"r,omitempty"` // the git work tree the file belongs to
	Session bool   `json:"x,omitempty"` // a harness session file: union by file
}

// Repo is a git repository: its HEAD and its branches.
type Repo struct {
	Path     string            `json:"path"`
	Head     string            `json:"head"` // refs/heads/<branch>, or a commit when detached
	Branches map[string]string `json:"branches"`
}

// Manifest is a scan of the trees.
type Manifest struct {
	Version  int       `json:"version"`
	ForkID   string    `json:"forkId,omitempty"`
	Taken    time.Time `json:"taken"`
	Entries  []Entry   `json:"entries"`
	Repos    []Repo    `json:"repos"`
	Installs []string  `json:"installs"`
	index    map[string]*Entry
}

func (m *Manifest) entry(p string) *Entry {
	if m == nil {
		return nil
	}
	if m.index == nil {
		m.index = make(map[string]*Entry, len(m.Entries))
		for i := range m.Entries {
			m.index[m.Entries[i].Path] = &m.Entries[i]
		}
	}
	return m.index[p]
}

func (m *Manifest) repo(p string) *Repo {
	if m == nil {
		return nil
	}
	for i := range m.Repos {
		if m.Repos[i].Path == p {
			return &m.Repos[i]
		}
	}
	return nil
}

// --- what is merged ------------------------------------------------------------------------

// homeSkipped are paths of the home that are never merged: caches, Studio's own state and
// the files Studio writes into every sandbox's home.
var homeSkipped = []string{
	".cache", ".npm/_cacache", "go/pkg", ".cargo/registry", ".cargo/git", ".rustup/downloads",
	".rustup/tmp", ".local/share/mise/downloads", ".local/share/pnpm/store", ".bun/install/cache",
	".local/state/studio",
	".claude.json", ".claude/settings.json", ".claude/.credentials.json",
	".codex/auth.json", ".codex/config.toml", ".codex/hooks.json",
	".config/opencode/opencode.json", ".config/opencode/plugins/studio-memory.js",
	".config/git/config", "memory",
}

// installRoots hold tool installs, each the directory (or file) depth levels below its root.
var installRoots = []struct {
	root  string
	depth int
}{
	{".local/share/mise/installs", 2},
	{".cargo/bin", 1},
	{"go/bin", 1},
	{".rustup/toolchains", 1},
}

// sessionRoots hold harness sessions, merged as a union of files.
var sessionRoots = []string{".claude/projects", ".codex/sessions"}

func under(rel, root string) bool { return rel == root || strings.HasPrefix(rel, root+"/") }

// --- scanning ------------------------------------------------------------------------------

type scanner struct {
	cfg     Config
	prev    *Manifest // reuses hashes of unchanged files
	out     *Manifest
	entries int
}

// Scan walks the trees. Files whose size, mtime and mode match prev keep prev's hash.
func Scan(cfg Config, prev *Manifest) (*Manifest, error) {
	s := &scanner{cfg: cfg, prev: prev, out: &Manifest{Version: manifestVers, Taken: time.Now().UTC(), Repos: []Repo{}, Installs: []string{}}}
	for _, tree := range []string{TreeWorkspace, TreeHome} {
		if err := s.dir(tree, ""); err != nil {
			return nil, err
		}
	}
	return s.out, nil
}

// dir scans the directory rel of tree.
func (s *scanner) dir(tree, rel string) error {
	abs := filepath.Join(s.cfg.root(tree), filepath.FromSlash(rel))
	// A home that is a repository itself (of dotfiles) is scanned as plain files.
	if rel != "" || tree == TreeWorkspace {
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			return s.repo(tree, rel)
		}
	}
	list, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return nil
		}
		return err
	}
	for _, d := range list {
		name := d.Name()
		child := path.Join(rel, name)
		if name == ".git" || rel == "" && (name == "lost+found" || tree == TreeWorkspace && name == stagingDir) {
			continue
		}
		if tree == TreeHome && slices.ContainsFunc(homeSkipped, func(p string) bool { return under(child, p) }) {
			continue
		}
		if tree == TreeHome {
			if done, err := s.install(child, d); done || err != nil {
				return err
			}
		}
		switch {
		case d.IsDir():
			if err := s.dir(tree, child); err != nil {
				return err
			}
		case d.Type()&fs.ModeSymlink != 0, d.Type().IsRegular():
			session := tree == TreeHome && slices.ContainsFunc(sessionRoots, func(p string) bool { return under(child, p) })
			if err := s.file(tree+"/"+child, "", session); err != nil {
				return err
			}
		}
	}
	return nil
}

// install records child if it is a tool install, and reports whether it was one or holds
// them (an install root, which is not scanned as files).
func (s *scanner) install(child string, d fs.DirEntry) (bool, error) {
	for _, r := range installRoots {
		if !under(child, r.root) {
			if strings.HasPrefix(r.root, child+"/") {
				return false, nil // a parent of the root: go on scanning
			}
			continue
		}
		depth := 0
		if child != r.root {
			depth = strings.Count(strings.TrimPrefix(child, r.root+"/"), "/") + 1
		}
		if depth == r.depth {
			s.out.Installs = append(s.out.Installs, TreeHome+"/"+child)
			return true, nil
		}
		if d.IsDir() {
			// A level above the installs: list them, but scan nothing in it as files.
			list, err := os.ReadDir(filepath.Join(s.cfg.Home, filepath.FromSlash(child)))
			if err != nil {
				return true, nil
			}
			for _, e := range list {
				if _, err := s.install(child+"/"+e.Name(), e); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}
	return false, nil
}

// repo scans the git work tree rel of tree: its files as git lists them, its HEAD and branches.
func (s *scanner) repo(tree, rel string) error {
	dir := filepath.Join(s.cfg.root(tree), filepath.FromSlash(rel))
	repoPath := tree
	if rel != "" {
		repoPath += "/" + rel
	}
	r := Repo{Path: repoPath, Branches: map[string]string{}}
	if out, err := s.cfg.git(dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(string(out))
	} else if out, err := s.cfg.git(dir, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(string(out))
	}
	out, err := s.cfg.git(dir, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads")
	if err != nil {
		return fmt.Errorf("%s: %w", repoPath, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if sha, ref, ok := strings.Cut(line, " "); ok {
			r.Branches[ref] = sha
		}
	}
	s.out.Repos = append(s.out.Repos, r)
	names, err := s.cfg.gitList(dir, "ls-files", "-co", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("%s: %w", repoPath, err)
	}
	slices.Sort(names)
	for _, n := range names {
		if sub, ok := strings.CutSuffix(n, "/"); ok && validRel(sub) {
			// git lists a repository inside the work tree as one untracked directory.
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(sub), ".git")); err == nil {
				if err := s.repo(tree, path.Join(rel, sub)); err != nil {
					return err
				}
			}
			continue
		}
		if !validRel(n) {
			continue
		}
		p := repoPath + "/" + n
		if under(p, TreeWorkspace+"/"+stagingDir) {
			continue
		}
		if tree == TreeHome && slices.ContainsFunc(homeSkipped, func(h string) bool { return under(strings.TrimPrefix(p, TreeHome+"/"), h) }) {
			continue
		}
		if err := s.file(p, repoPath, false); err != nil {
			return err
		}
	}
	return nil
}

// file records the file or symlink p.
func (s *scanner) file(p, repo string, session bool) error {
	abs, err := s.cfg.abs(p)
	if err != nil {
		return nil
	}
	e, err := stat(abs, p, s.prev.entry(p))
	if err != nil || e == nil {
		return err
	}
	e.Repo, e.Session = repo, session
	s.entries++
	if s.entries > maxEntries {
		return fmt.Errorf("more than %d files to merge", maxEntries)
	}
	s.out.Entries = append(s.out.Entries, *e)
	return nil
}

// stat describes the file or symlink at abs as entry p, or nil for anything else or a path
// that is gone. A file matching prev in size, mtime and mode keeps prev's hash.
func stat(abs, p string, prev *Entry) (*Entry, error) {
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e := &Entry{Path: p, Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		e.Type = 'l'
		if e.Target, err = os.Readlink(abs); err != nil {
			return nil, nil
		}
	case info.Mode().IsRegular():
		e.Type, e.Size, e.MTime = 'f', info.Size(), info.ModTime().UnixNano()
		if prev != nil && prev.Type == 'f' && prev.Size == e.Size && prev.MTime == e.MTime && prev.Mode == e.Mode {
			e.Hash = prev.Hash
		} else if e.Size <= maxHashed {
			if e.Hash, err = hashFile(abs); err != nil {
				return nil, nil
			}
		}
	default:
		return nil, nil
	}
	return e, nil
}

func hashFile(abs string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// same reports whether a and b have the same content: type, content or target, and the
// executable bit. Files too large to hash compare by size and mtime.
func same(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type != b.Type || a.Mode&0o111 != b.Mode&0o111 {
		return false
	}
	if a.Type == 'l' {
		return a.Target == b.Target
	}
	if a.Hash == "" || b.Hash == "" {
		return a.Hash == b.Hash && a.Size == b.Size && a.MTime == b.MTime
	}
	return a.Hash == b.Hash
}

// isText reports whether b looks like text, as git decides: no NUL in its first 8000 bytes.
func isText(b []byte) bool { return !bytes.Contains(b[:min(len(b), 8000)], []byte{0}) }

// --- git -----------------------------------------------------------------------------------

func (c Config) gitCmd(dir string, args ...string) *exec.Cmd {
	bin := c.Git
	if bin == "" {
		bin = "git"
	}
	full := append([]string{"-c", "core.fsmonitor=false", "-c", "safe.directory=*", "-c", "core.hooksPath=/dev/null",
		"-c", "core.quotePath=false"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1")
	return cmd
}

// git runs git in dir and returns its stdout; a failure carries its stderr.
func (c Config) git(dir string, args ...string) ([]byte, error) {
	cmd := c.gitCmd(dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	timer := time.AfterFunc(10*time.Minute, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer timer.Stop()
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out, nil
}

// gitList runs a git listing with -z output and splits it.
func (c Config) gitList(dir string, args ...string) ([]string, error) {
	out, err := c.git(dir, args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(string(out), "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}
