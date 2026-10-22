//go:build linux

package forkmerge

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ApplyOptions name the two sides in conflict markers and branch names.
type ApplyOptions struct {
	Fork string // the fork's name
	Ours string // the sandbox merged into
}

// Apply merges the archive of a fork's changes into the trees (merge-apply). Conflicts are
// left for the user, as the report says; it is also kept as .studio-merge/report.json.
func Apply(cfg Config, opts ApplyOptions, r io.Reader) (*Report, error) {
	h, tr, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	in, err := cfg.prepareStaging()
	if err != nil {
		return nil, err
	}
	installs, err := cfg.extractArchive(tr, h, in)
	if err != nil {
		return nil, err
	}
	rep := &Report{Fork: opts.Fork, NoBase: h.NoBase, Files: []FileResult{}, Branches: []BranchResult{}, Installs: []string{}}
	for _, rc := range h.Repos {
		rep.Branches = append(rep.Branches, cfg.applyRepo(rc, in, opts.Fork)...)
	}
	m := merger{cfg: cfg, in: in, opts: opts}
	for _, c := range ordered(h.Changes) {
		res, ok := m.change(c)
		if !ok {
			rep.Unchanged++
			continue
		}
		rep.Files = append(rep.Files, res)
		if res.Outcome == OutcomeConflict {
			rep.Conflicts++
		}
	}
	rep.Installs = cfg.placeInstalls(installs)
	if err := cfg.saveReport(rep); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(in)
	return rep, nil
}

// Status rechecks the conflicts of the last Apply (merge-status).
func Status(cfg Config) (*Report, error) {
	b, err := os.ReadFile(filepath.Join(cfg.staging(), "report.json"))
	if err != nil {
		return nil, fmt.Errorf("no merge here: %w", err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	rep.Conflicts = 0
	for i := range rep.Files {
		f := &rep.Files[i]
		if f.Outcome != OutcomeConflict {
			continue
		}
		if f.Conflict == ConflictText {
			abs, err := cfg.abs(f.Path)
			if err != nil {
				continue
			}
			content, err := readSmall(abs, maxMerge)
			f.Resolved = errors.Is(err, fs.ErrNotExist) || err == nil && !hasMarkers(content)
		} else {
			_, err := os.Lstat(filepath.Join(cfg.staging(), filepath.FromSlash(f.Artifact)))
			f.Resolved = errors.Is(err, fs.ErrNotExist)
		}
		if !f.Resolved {
			rep.Conflicts++
		}
	}
	if err := cfg.saveReport(&rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

// ReplayConflictError lists what changed in the sandbox since a scratch sandbox was made from
// it, which a replay won't overwrite.
type ReplayConflictError struct{ Paths []string }

func (e *ReplayConflictError) Error() string {
	return fmt.Sprintf("changed here since the merge started: %s", strings.Join(e.Paths, ", "))
}

// Replay applies the archive of a scratch sandbox's changes (merge-replay). It checks every
// path and branch first, and changes nothing if one changed here since the scratch sandbox
// was made: that is a *ReplayConflictError.
func Replay(cfg Config, r io.Reader) error {
	h, tr, err := readHeader(r)
	if err != nil {
		return err
	}
	var conflicts []string
	for _, c := range h.Changes {
		abs, err := cfg.abs(c.Path)
		if err != nil {
			return err
		}
		tree, rel, _ := splitPath(c.Path)
		cur, err := stat(abs, c.Path, nil)
		if err != nil || checkParents(cfg.root(tree), rel) != nil || !same(cur, c.Base) {
			conflicts = append(conflicts, c.Path)
		}
	}
	for _, rc := range h.Repos {
		dir, err := cfg.repoDir(rc.Path)
		if err != nil {
			return err
		}
		_, gitErr := os.Lstat(filepath.Join(dir, ".git"))
		if rc.New {
			if gitErr == nil {
				conflicts = append(conflicts, rc.Path)
			}
			continue
		}
		if gitErr != nil {
			conflicts = append(conflicts, rc.Path)
			continue
		}
		for _, bc := range rc.Branches {
			if cfg.refSHA(dir, bc.Ref) != bc.Old {
				conflicts = append(conflicts, rc.Path+": "+strings.TrimPrefix(bc.Ref, "refs/heads/"))
			}
		}
		if rc.Head != rc.OldHead && cfg.head(dir) != rc.OldHead {
			conflicts = append(conflicts, rc.Path+": HEAD")
		}
	}
	if len(conflicts) > 0 {
		return &ReplayConflictError{Paths: conflicts}
	}
	in, err := cfg.prepareStaging()
	if err != nil {
		return err
	}
	defer os.RemoveAll(cfg.staging())
	installs, err := cfg.extractArchive(tr, h, in)
	if err != nil {
		return err
	}
	for _, c := range h.Changes {
		if c.New != nil && c.New.Type == 'f' && c.New.Hash != "" {
			if hash, err := hashFile(filepath.Join(in, "new", filepath.FromSlash(c.Path))); err != nil || hash != c.New.Hash {
				return fmt.Errorf("%s changed while it was being copied", c.Path)
			}
		}
	}
	for _, rc := range h.Repos {
		if err := cfg.replayRepo(rc, in); err != nil {
			return err
		}
	}
	m := merger{cfg: cfg, in: in}
	for _, c := range ordered(h.Changes) {
		if err := m.take(c); err != nil {
			return fmt.Errorf("%s: %w", c.Path, err)
		}
	}
	cfg.placeInstalls(installs)
	return nil
}

// --- staging -------------------------------------------------------------------------------

// prepareStaging empties the staging directory and returns the directory to unpack into.
func (c Config) prepareStaging() (string, error) {
	dir := c.staging()
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	in := filepath.Join(dir, "in")
	if err := os.MkdirAll(in, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return "", err
	}
	return in, nil
}

func (c Config) saveReport(rep *Report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.staging(), "report.json"), append(b, '\n'), 0o644)
}

// pendingInstall is an install unpacked next to where it goes.
type pendingInstall struct{ path, tmp, abs string }

// extractArchive unpacks an archive's content into in, and the installs this sandbox lacks
// next to where they go.
func (c Config) extractArchive(tr *tar.Reader, h *Header, in string) ([]pendingInstall, error) {
	var installs []pendingInstall
	byName := map[string]*pendingInstall{}
	for _, p := range h.Installs {
		abs, err := c.abs(p)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(abs); err == nil {
			continue // this sandbox has it
		}
		installs = append(installs, pendingInstall{path: p, abs: abs, tmp: filepath.Join(filepath.Dir(abs), ".studio-merge-"+filepath.Base(abs))})
	}
	for i := range installs {
		byName["installs/"+installs[i].path] = &installs[i]
		_ = os.RemoveAll(installs[i].tmp)
	}
	err := extract(tr, func(name string) (root, rel string) {
		if !strings.HasPrefix(name, "installs/") {
			return in, name
		}
		for prefix, pi := range byName {
			if name == prefix || strings.HasPrefix(name, prefix+"/") {
				tree, trel, _ := splitPath(pi.path)
				rel := path.Join(path.Dir(trel), filepath.Base(pi.tmp)) + strings.TrimPrefix(name, prefix)
				return c.root(tree), rel
			}
		}
		return "", ""
	})
	if err != nil {
		for _, pi := range installs {
			_ = os.RemoveAll(pi.tmp)
		}
		return nil, err
	}
	return installs, nil
}

// placeInstalls moves the unpacked installs into place, unless one appeared meanwhile.
func (c Config) placeInstalls(installs []pendingInstall) []string {
	placed := []string{}
	for _, pi := range installs {
		if _, err := os.Lstat(pi.tmp); err != nil {
			continue
		}
		if _, err := os.Lstat(pi.abs); err == nil {
			_ = os.RemoveAll(pi.tmp)
			continue
		}
		if err := os.Rename(pi.tmp, pi.abs); err != nil {
			_ = os.RemoveAll(pi.tmp)
			continue
		}
		placed = append(placed, pi.path)
	}
	return placed
}

// ordered puts deletions first, so a file can replace a directory and the other way round.
func ordered(changes []Change) []Change {
	out := slices.Clone(changes)
	slices.SortStableFunc(out, func(a, b Change) int {
		ad, bd := a.New == nil, b.New == nil
		switch {
		case ad && !bd:
			return -1
		case bd && !ad:
			return 1
		case ad && bd:
			return strings.Compare(b.Path, a.Path) // deepest first
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// --- files ---------------------------------------------------------------------------------

type merger struct {
	cfg  Config
	in   string
	opts ApplyOptions
}

func (m merger) staged(kind, p string) string {
	return filepath.Join(m.in, kind, filepath.FromSlash(p))
}

// change merges one change, or reports false if this sandbox already has the fork's version.
func (m merger) change(c Change) (FileResult, bool) {
	res := FileResult{Path: c.Path}
	abs, _ := m.cfg.abs(c.Path)
	tree, rel, _ := splitPath(c.Path)
	if err := checkParents(m.cfg.root(tree), rel); err != nil {
		return m.conflict(res, ConflictError, err.Error(), c, nil), true
	}
	ours, err := stat(abs, c.Path, nil)
	if err != nil {
		return m.conflict(res, ConflictError, err.Error(), c, nil), true
	}
	if c.New != nil && c.New.Type == 'f' && c.New.Hash != "" {
		if hash, err := hashFile(m.staged("new", c.Path)); err != nil || hash != c.New.Hash {
			return m.conflict(res, ConflictError, "the file changed in the fork while it was being copied", c, ours), true
		}
	}
	switch {
	case same(ours, c.New):
		return res, false
	case same(ours, c.Base):
		if err := m.take(c); err != nil {
			return m.conflict(res, ConflictError, err.Error(), c, ours), true
		}
		res.Outcome = OutcomeTheirs
		if c.New == nil {
			res.Outcome = OutcomeDeleted
		}
		return res, true
	case c.New != nil && c.New.Session || c.Base != nil && c.Base.Session:
		res.Outcome = OutcomeKept
		return res, true
	case c.New == nil:
		return m.conflict(res, ConflictDelete, "the fork deleted it and this sandbox changed it", c, ours), true
	case ours == nil:
		return m.conflict(res, ConflictDelete, "this sandbox deleted it and the fork changed it", c, ours), true
	case ours.Type != c.New.Type:
		return m.conflict(res, ConflictType, "a file on one side and a symlink on the other", c, ours), true
	case ours.Type == 'l':
		return m.conflict(res, ConflictBinary, "both changed where the symlink points", c, ours), true
	}
	return m.merge(res, c, abs, ours), true
}

// merge merges a file both sides changed with git merge-file.
func (m merger) merge(res FileResult, c Change, abs string, ours *Entry) FileResult {
	var base []byte
	if c.Base != nil {
		if c.Base.Type != 'f' || !c.BaseBlob {
			return m.conflict(res, ConflictBinary, "no earlier version was kept to merge with", c, ours)
		}
		b, err := readSmall(m.staged("base", c.Base.Hash), maxMerge)
		if err != nil {
			return m.conflict(res, ConflictBinary, "no earlier version was kept to merge with", c, ours)
		}
		base = b
	}
	oursB, err := readSmall(abs, maxMerge)
	if err != nil || !isText(oursB) || !isText(base) {
		return m.conflict(res, ConflictBinary, "not a text file, or too large to merge", c, ours)
	}
	theirsB, err := readSmall(m.staged("new", c.Path), maxMerge)
	if err != nil || !isText(theirsB) {
		return m.conflict(res, ConflictBinary, "not a text file, or too large to merge", c, ours)
	}
	work := filepath.Join(m.cfg.staging(), "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return m.conflict(res, ConflictError, err.Error(), c, ours)
	}
	files := []string{filepath.Join(work, "ours"), filepath.Join(work, "base"), filepath.Join(work, "theirs")}
	for i, b := range [][]byte{oursB, base, theirsB} {
		if err := os.WriteFile(files[i], b, 0o600); err != nil {
			return m.conflict(res, ConflictError, err.Error(), c, ours)
		}
	}
	cmd := m.cfg.gitCmd(work, "merge-file", "-p", "-L", label(m.opts.Ours, "this sandbox"), "-L", "base", "-L", label(m.opts.Fork, "fork"),
		files[0], files[1], files[2])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	merged, err := cmd.Output()
	var exit *exec.ExitError
	conflicted := errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128
	if err != nil && !conflicted {
		return m.conflict(res, ConflictError, "git merge-file: "+strings.TrimSpace(stderr.String()), c, ours)
	}
	mode := fs.FileMode(ours.Mode)
	if c.Base != nil && ours.Mode&0o111 == c.Base.Mode&0o111 {
		mode = fs.FileMode(ours.Mode&^0o111 | c.New.Mode&0o111)
	}
	if conflicted {
		res = m.conflict(res, ConflictText, "both changed the same lines", c, ours)
	} else {
		res.Outcome = OutcomeMerged
	}
	if err := writeAtomic(abs, merged, mode); err != nil {
		return m.conflict(FileResult{Path: c.Path}, ConflictError, err.Error(), c, ours)
	}
	return res
}

// conflict records a conflict: the base, this sandbox's and the fork's versions are kept under
// conflicts/, and this sandbox's version stays in place.
func (m merger) conflict(res FileResult, kind, detail string, c Change, ours *Entry) FileResult {
	res.Outcome, res.Conflict, res.Detail = OutcomeConflict, kind, detail
	tree, rel, _ := splitPath(c.Path)
	dir := path.Join("conflicts", tree, path.Dir(rel))
	name := path.Base(rel)
	artDir := filepath.Join(m.cfg.staging(), filepath.FromSlash(dir))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		return res
	}
	if c.BaseBlob {
		_ = copyFile(m.staged("base", c.Base.Hash), filepath.Join(artDir, name+".base"), 0o644)
	}
	if ours != nil {
		abs, _ := m.cfg.abs(c.Path)
		_ = copyEntry(abs, ours, filepath.Join(artDir, name+".ours"))
	}
	if c.New == nil {
		res.Artifact = path.Join(dir, name+".deleted")
		_ = os.WriteFile(filepath.Join(artDir, name+".deleted"), []byte("The fork deleted "+c.Path+". Delete it too, or keep it, then delete this file.\n"), 0o644)
	} else {
		res.Artifact = path.Join(dir, name+".theirs")
		_ = copyEntry(m.staged("new", c.Path), c.New, filepath.Join(artDir, name+".theirs"))
	}
	return res
}

// take makes the path what the fork has: its content, or deleted.
func (m merger) take(c Change) error {
	abs, err := m.cfg.abs(c.Path)
	if err != nil {
		return err
	}
	tree, rel, _ := splitPath(c.Path)
	if err := checkParents(m.cfg.root(tree), rel); err != nil {
		return err
	}
	if c.New == nil {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		removeEmptyParents(m.cfg.root(tree), abs)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(abs), ".studio-merge-"+filepath.Base(abs))
	_ = os.Remove(tmp)
	if c.New.Type == 'l' {
		if err := os.Symlink(c.New.Target, tmp); err != nil {
			return err
		}
	} else if err := copyFile(m.staged("new", c.Path), tmp, fs.FileMode(c.New.Mode)); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// removeEmptyParents removes the empty directories above abs, up to root.
func removeEmptyParents(root, abs string) {
	for dir := filepath.Dir(abs); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

func label(name, fallback string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return fallback
}

var markerStart, markerEnd = regexp.MustCompile(`(?m)^<{7} `), regexp.MustCompile(`(?m)^>{7} `)

func hasMarkers(b []byte) bool { return markerStart.Match(b) && markerEnd.Match(b) }

func writeAtomic(abs string, b []byte, mode fs.FileMode) error {
	tmp := filepath.Join(filepath.Dir(abs), ".studio-merge-"+filepath.Base(abs))
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode.Perm()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(dst, mode.Perm())
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// copyEntry copies the file or symlink e, found at src, to dst.
func copyEntry(src string, e *Entry, dst string) error {
	_ = os.Remove(dst)
	if e.Type == 'l' {
		return os.Symlink(e.Target, dst)
	}
	return copyFile(src, dst, fs.FileMode(e.Mode)|0o600)
}

// --- repositories --------------------------------------------------------------------------

// applyRepo brings a fork's branches into the repository: as fork/<fork>/<branch> next to
// this sandbox's own, or under their own names in a repository this sandbox doesn't have.
func (c Config) applyRepo(rc RepoChange, in, fork string) []BranchResult {
	results := make([]BranchResult, 0, len(rc.Branches))
	fail := func(err error) []BranchResult {
		for _, bc := range rc.Branches {
			results = append(results, BranchResult{Repo: rc.Path, Branch: branchName(bc.Ref), Error: err.Error()})
		}
		return results
	}
	dir, err := c.repoDir(rc.Path)
	if err != nil {
		return fail(err)
	}
	bundle := ""
	if rc.Bundle != "" {
		bundle = filepath.Join(in, filepath.FromSlash(rc.Bundle))
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		if err := c.initRepo(dir, rc, bundle); err != nil {
			return fail(err)
		}
		for _, bc := range rc.Branches {
			results = append(results, BranchResult{Repo: rc.Path, Branch: branchName(bc.Ref), Ref: bc.Ref})
		}
		return results
	}
	prefix := "refs/heads/fork/" + refSafe(fork) + "/"
	for _, bc := range rc.Branches {
		res := BranchResult{Repo: rc.Path, Branch: branchName(bc.Ref)}
		target := prefix + res.Branch
		for n := 2; c.refSHA(dir, target) != ""; n++ {
			if c.refSHA(dir, target) == bc.New {
				break
			}
			target = fmt.Sprintf("%s%s-%d", prefix, res.Branch, n)
		}
		if _, err := c.git(dir, "check-ref-format", target); err != nil {
			res.Error = "not a valid branch name here: " + target
		} else if c.refSHA(dir, target) == bc.New {
			res.Ref = target
		} else if c.hasCommit(dir, bc.New) {
			if _, err := c.git(dir, "update-ref", target, bc.New, ""); err != nil {
				res.Error = err.Error()
			} else {
				res.Ref = target
			}
		} else if bundle == "" {
			res.Error = "its commits are missing"
		} else if _, err := c.git(dir, "fetch", "-q", "--no-tags", "--no-write-fetch-head", bundle, bc.Ref+":"+target); err != nil {
			res.Error = err.Error()
		} else {
			res.Ref = target
		}
		results = append(results, res)
	}
	return results
}

// initRepo makes the repository at dir, with the fork's branches and HEAD. Its working files
// come from the file changes; its index matches HEAD.
func (c Config) initRepo(dir string, rc RepoChange, bundle string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := c.git(dir, "init", "-q"); err != nil {
		return err
	}
	if bundle != "" {
		var specs []string
		for _, bc := range rc.Branches {
			specs = append(specs, "+"+bc.Ref+":"+bc.Ref)
		}
		args := append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--update-head-ok", bundle}, specs...)
		if _, err := c.git(dir, args...); err != nil {
			return err
		}
	}
	return c.setHead(dir, rc.Head)
}

// replayRepo sets a scratch sandbox's branches in this repository; Replay checked that they
// are where they were.
func (c Config) replayRepo(rc RepoChange, in string) error {
	dir, err := c.repoDir(rc.Path)
	if err != nil {
		return err
	}
	bundle := ""
	if rc.Bundle != "" {
		bundle = filepath.Join(in, filepath.FromSlash(rc.Bundle))
	}
	if rc.New {
		return c.initRepo(dir, rc, bundle)
	}
	var specs []string
	for _, bc := range rc.Branches {
		if c.hasCommit(dir, bc.New) {
			if _, err := c.git(dir, "update-ref", bc.Ref, bc.New, bc.Old); err != nil {
				return err
			}
		} else {
			specs = append(specs, "+"+bc.Ref+":"+bc.Ref)
		}
	}
	if len(specs) > 0 {
		if bundle == "" {
			return fmt.Errorf("%s: commits missing", rc.Path)
		}
		args := append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--update-head-ok", bundle}, specs...)
		if _, err := c.git(dir, args...); err != nil {
			return err
		}
	}
	head := c.head(dir)
	moved := rc.Head != rc.OldHead || slices.ContainsFunc(rc.Branches, func(bc BranchChange) bool { return bc.Ref == head })
	if rc.Head != rc.OldHead {
		if err := c.setHead(dir, rc.Head); err != nil {
			return err
		}
	} else if moved {
		_, err := c.git(dir, "reset", "-q")
		return err
	}
	return nil
}

// setHead points HEAD at head, a branch or a commit, and makes the index match it.
func (c Config) setHead(dir, head string) error {
	var err error
	switch {
	case strings.HasPrefix(head, "refs/heads/"):
		_, err = c.git(dir, "symbolic-ref", "HEAD", head)
	case head != "":
		_, err = c.git(dir, "update-ref", "--no-deref", "HEAD", head)
	}
	if err != nil {
		return err
	}
	if c.refSHA(dir, "HEAD") != "" {
		_, err = c.git(dir, "reset", "-q")
	}
	return err
}

// refSHA is the commit ref points at, or "".
func (c Config) refSHA(dir, ref string) string {
	out, err := c.git(dir, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// head is HEAD as a manifest records it: its branch, or its commit when detached.
func (c Config) head(dir string) string {
	if out, err := c.git(dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		return strings.TrimSpace(string(out))
	}
	return c.refSHA(dir, "HEAD")
}

func (c Config) hasCommit(dir, sha string) bool {
	_, err := c.git(dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

func branchName(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

// refSafe makes a sandbox name usable in a branch name.
func refSafe(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if s := strings.Trim(b.String(), "-"); s != "" {
		return s
	}
	return "fork"
}
