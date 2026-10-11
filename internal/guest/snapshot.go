//go:build linux

package guest

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Turn snapshots (docs/design/chat-mode.md section 5): a content-addressed copy of the
// workspace's files taken before and after each chat turn, so a turn's changes can be
// shown and undone.
//
// The store is a directory owned by the agent process with mode 0700, in the agent
// user's home on the root disk: blobs/<2 hex>/<sha256> and manifests/<id>.json. The
// workspace is walked through directory descriptors without following any symlink;
// symlinks are recorded and restored as links, never followed. Every .git directory is
// skipped, and in a git work tree only what `git ls-files -co --exclude-standard` lists
// is kept, so ignored files are neither snapshotted, nor restored, nor removed.

// snapshotDir is the store, relative to the agent user's home.
const snapshotDir = ".local/state/studio/snapshots"

// maxSnapshotEntries bounds one walk of the workspace.
const maxSnapshotEntries = 500_000

// lineBudget bounds what one reply reads to count or diff lines.
const lineBudget = 32 << 20

// snapshots is the store and the workspace it snapshots.
type snapshots struct {
	mu        sync.Mutex
	workspace string
	store     string // the store's absolute path
	owner     [2]int // the agent user: parents of the store, and readable files
	list      gitLister
	cap       int64 // the store's size limit
	maxFile   int64
	maxListed int
}

// gitLister returns what `git ls-files -co --exclude-standard -z` lists in dir.
type gitLister func(dir string) ([]string, error)

func (a *Agent) snapshotStore() *snapshots {
	a.snapOnce.Do(func() {
		a.snap = &snapshots{
			workspace: Workdir,
			store:     path.Join(a.user.home, snapshotDir),
			owner:     [2]int{int(a.user.uid), int(a.user.gid)},
			list: func(dir string) ([]string, error) {
				cmd := a.command("git", "-c", "core.fsmonitor=false", "-c", "safe.directory=*", "ls-files", "-co", "--exclude-standard", "-z")
				cmd.Dir = dir
				return gitOutput(cmd)
			},
			cap:       agentproto.MaxSnapshotBytes,
			maxFile:   agentproto.MaxSnapshotFile,
			maxListed: agentproto.MaxChangesListed,
		}
	})
	return a.snap
}

// gitOutput runs a git listing, killing it after a minute: a snapshot holds up its turn.
func gitOutput(cmd *exec.Cmd) ([]string, error) {
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	timer := time.AfterFunc(time.Minute, func() { cmd.Process.Kill() })
	err := cmd.Wait()
	timer.Stop()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(out.String(), "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// snapshot serves a KindSnapshot stream.
func (a *Agent) snapshot(st net.Conn, br *bufio.Reader) error {
	var req agentproto.SnapshotRequest
	if err := readBody(br, 1<<20, &req); err != nil {
		return replyError(st, err)
	}
	reply, err := a.snapshotStore().serve(req)
	if err != nil {
		return replyError(st, err)
	}
	return agentproto.WriteJSONLine(st, reply)
}

func (s *snapshots) serve(req agentproto.SnapshotRequest) (agentproto.SnapshotReply, error) {
	for _, id := range append([]string{req.ID, req.Base}, req.Keep...) {
		if id != "" && !validSessionName(id) {
			return agentproto.SnapshotReply{}, errors.New("invalid snapshot ID")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reply, err := s.do(req)
	if err == nil {
		fitReply(&reply)
	}
	return reply, err
}

// fitReply keeps a reply within what the host reads: diffs go first, then changes.
func fitReply(r *agentproto.SnapshotReply) {
	size := func() int {
		b, _ := json.Marshal(r)
		return len(b) + 1
	}
	if size() <= agentproto.MaxGuestReply {
		return
	}
	for i := range r.Changes {
		if r.Changes[i].Diff != "" {
			r.Changes[i].Diff, r.Changes[i].Truncated = "", true
		}
	}
	for len(r.Changes) > 0 && size() > agentproto.MaxGuestReply {
		cut := max(len(r.Changes)/10, 1)
		r.Changes = r.Changes[:len(r.Changes)-cut]
		r.More += cut
	}
}

func (s *snapshots) do(req agentproto.SnapshotRequest) (agentproto.SnapshotReply, error) {
	switch req.Op {
	case agentproto.SnapshotTake:
		if req.ID == "" {
			return agentproto.SnapshotReply{}, errors.New("no snapshot ID")
		}
		return s.take(req.ID, req.Base)
	case agentproto.SnapshotChanges:
		if req.ID == "" || req.Base == "" {
			return agentproto.SnapshotReply{}, errors.New("changes compares two snapshots")
		}
		if req.Path != "" {
			if _, err := cleanName(req.Path); err != nil {
				return agentproto.SnapshotReply{}, errors.New("invalid path")
			}
		}
		return s.changes(req.Base, req.ID, req.Path)
	case agentproto.SnapshotRestore:
		if req.ID == "" {
			return agentproto.SnapshotReply{}, errors.New("no snapshot ID")
		}
		return s.restore(req.ID)
	case agentproto.SnapshotGC:
		if len(req.Keep) > agentproto.MaxSnapshotKeep {
			return agentproto.SnapshotReply{}, errors.New("too many snapshots to keep")
		}
		st, err := s.openStore()
		if err != nil {
			return agentproto.SnapshotReply{}, err
		}
		defer st.close()
		return agentproto.SnapshotReply{}, st.gc(req.Keep)
	}
	return agentproto.SnapshotReply{}, fmt.Errorf("unknown snapshot op %q", req.Op)
}

// --- manifests ------------------------------------------------------------------------

// mentry is one path of a snapshot.
type mentry struct {
	Path   string `json:"p"`
	Type   byte   `json:"t"` // 'f' file, 'd' directory, 'l' symlink
	Mode   uint32 `json:"m"` // permission bits
	UID    int    `json:"u"`
	GID    int    `json:"g"`
	Size   int64  `json:"s,omitempty"`
	MTime  int64  `json:"n,omitempty"` // nanoseconds
	Hash   string `json:"h,omitempty"` // empty for a file too large to keep
	Target string `json:"l,omitempty"`
}

type manifest struct {
	Entries []mentry `json:"entries"`
}

func (m *manifest) index() map[string]mentry {
	out := make(map[string]mentry, len(m.Entries))
	for _, e := range m.Entries {
		out[e.Path] = e
	}
	return out
}

// --- the store ------------------------------------------------------------------------

type store struct {
	fd        int
	blobs     int
	manifests int
}

func (st *store) close() {
	unix.Close(st.manifests)
	unix.Close(st.blobs)
	unix.Close(st.fd)
}

var beneath = &unix.OpenHow{
	Flags:   unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC,
	Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
}

// openStore opens the store, creating it if needed. Its parents in the home belong to the
// agent user; the store itself must be ours with mode 0700, or the agent user could have
// planted manifests.
func (s *snapshots) openStore() (*store, error) {
	parent, base := path.Split(s.store)
	dir, err := unix.Open("/", unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.Trim(parent, "/"), "/") {
		next, err := unix.Openat2(dir, part, beneath)
		if errors.Is(err, unix.ENOENT) {
			if err = unix.Mkdirat(dir, part, 0o755); err == nil || errors.Is(err, unix.EEXIST) {
				unix.Fchownat(dir, part, s.owner[0], s.owner[1], unix.AT_SYMLINK_NOFOLLOW)
				next, err = unix.Openat2(dir, part, beneath)
			}
		}
		unix.Close(dir)
		if err != nil {
			return nil, fmt.Errorf("the snapshot store: %w", err)
		}
		dir = next
	}
	defer unix.Close(dir)
	if err := unix.Mkdirat(dir, base, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("the snapshot store: %w", err)
	}
	fd, err := unix.Openat2(dir, base, beneath)
	if err != nil {
		return nil, fmt.Errorf("the snapshot store: %w", err)
	}
	var fs unix.Stat_t
	if err := unix.Fstat(fd, &fs); err != nil || int(fs.Uid) != os.Geteuid() || fs.Mode&0o077 != 0 {
		unix.Close(fd)
		return nil, errors.New("the snapshot store is not Studio's: remove " + s.store)
	}
	st := &store{fd: fd, blobs: -1, manifests: -1}
	for _, sub := range []string{"blobs", "manifests"} {
		if err := unix.Mkdirat(fd, sub, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			st.close()
			return nil, err
		}
		sfd, err := unix.Openat2(fd, sub, beneath)
		if err != nil {
			st.close()
			return nil, err
		}
		if sub == "blobs" {
			st.blobs = sfd
		} else {
			st.manifests = sfd
		}
	}
	return st, nil
}

func (st *store) readManifest(id string) (*manifest, error) {
	b, err := readAt(st.manifests, id+".json", 1<<30)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, fmt.Errorf("snapshot %s: %w", id, os.ErrNotExist)
		}
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", id, err)
	}
	return &m, nil
}

func (st *store) writeManifest(id string, m *manifest) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeAtomic(st.manifests, id+".json", b, 0o600)
}

func blobName(hash string) string { return hash[:2] + "/" + hash }

func (st *store) hasBlob(hash string) bool {
	var fs unix.Stat_t
	return unix.Fstatat(st.blobs, blobName(hash), &fs, unix.AT_SYMLINK_NOFOLLOW) == nil
}

func (st *store) readBlob(hash string, max int64) ([]byte, error) {
	return readAt(st.blobs, blobName(hash), max)
}

func (st *store) putBlob(hash string, b []byte) error {
	if st.hasBlob(hash) {
		return nil
	}
	if err := unix.Mkdirat(st.blobs, hash[:2], 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	return writeAtomic(st.blobs, blobName(hash), b, 0o600)
}

// readAt reads a regular file below dir without following symlinks.
func readAt(dir int, name string, max int64) ([]byte, error) {
	fd, err := unix.Openat2(dir, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var fs unix.Stat_t
	if err := unix.Fstat(fd, &fs); err != nil {
		return nil, err
	}
	if fs.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, errors.New(name + " is not a regular file")
	}
	if fs.Size > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err == nil && int64(len(b)) > max {
		err = fmt.Errorf("%s grew past %d bytes", name, max)
	}
	return b, err
}

// writeAtomic writes name in dir through a temporary file renamed over it.
func writeAtomic(dir int, name string, b []byte, mode uint32) error {
	d, base := path.Split(name)
	parent := dir
	if d != "" {
		fd, err := unix.Openat2(dir, strings.TrimSuffix(d, "/"), beneath)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		parent = fd
	}
	tmp := tempName(base)
	fd, err := unix.Openat(parent, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(b)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Renameat(parent, tmp, parent, base)
	}
	if err != nil {
		unix.Unlinkat(parent, tmp, 0)
	}
	return err
}

func tempName(base string) string {
	var suffix [6]byte
	rand.Read(suffix[:])
	return ".studio-" + hex.EncodeToString(suffix[:]) + "-" + clipName(base)
}

func clipName(s string) string {
	if len(s) > 100 {
		return s[:100]
	}
	return s
}

// manifestIDs lists the store's snapshots, oldest first.
func (st *store) manifestIDs() ([]string, error) {
	type m struct {
		id string
		t  int64
	}
	var all []m
	err := eachName(st.manifests, func(name string) error {
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || strings.HasPrefix(name, ".") {
			unix.Unlinkat(st.manifests, name, 0) // a temporary file left by a crash
			return nil
		}
		var fs unix.Stat_t
		if unix.Fstatat(st.manifests, name, &fs, unix.AT_SYMLINK_NOFOLLOW) == nil {
			all = append(all, m{id, fs.Mtim.Nano()})
		}
		return nil
	})
	slices.SortFunc(all, func(a, b m) int {
		if a.t != b.t {
			return int(a.t - b.t)
		}
		return strings.Compare(a.id, b.id)
	})
	ids := make([]string, len(all))
	for i, x := range all {
		ids[i] = x.id
	}
	return ids, err
}

// eachName calls fn with the names in dir.
func eachName(dir int, fn func(name string) error) error {
	fd, err := unix.Openat2(dir, ".", beneath)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), ".")
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, n := range names {
		if err := fn(n); err != nil {
			return err
		}
	}
	return nil
}

// gc removes the snapshots not in keep and every blob no remaining snapshot uses.
func (st *store) gc(keep []string) error {
	ids, err := st.manifestIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.Contains(keep, id) {
			if err := unix.Unlinkat(st.manifests, id+".json", 0); err != nil && !errors.Is(err, unix.ENOENT) {
				return err
			}
		}
	}
	_, err = st.collect()
	return err
}

// collect removes unused blobs and returns the size of the rest.
func (st *store) collect() (int64, error) {
	ids, err := st.manifestIDs()
	if err != nil {
		return 0, err
	}
	used := map[string]bool{}
	for _, id := range ids {
		m, err := st.readManifest(id)
		if err != nil {
			return 0, err
		}
		for _, e := range m.Entries {
			if e.Hash != "" {
				used[e.Hash] = true
			}
		}
	}
	var total int64
	err = eachName(st.blobs, func(sub string) error {
		dir, err := unix.Openat2(st.blobs, sub, beneath)
		if err != nil {
			return nil
		}
		defer unix.Close(dir)
		return eachName(dir, func(name string) error {
			var fs unix.Stat_t
			if unix.Fstatat(dir, name, &fs, unix.AT_SYMLINK_NOFOLLOW) != nil {
				return nil
			}
			if !used[name] {
				return ignoreENOENT(unix.Unlinkat(dir, name, 0))
			}
			total += fs.Size
			return nil
		})
	})
	return total, err
}

func ignoreENOENT(err error) error {
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

// --- walking the workspace ------------------------------------------------------------

// gitFilter is what a git work tree keeps: the listed files and their directories, with
// paths relative to the work tree at prefix.
type gitFilter struct {
	prefix string
	files  map[string]bool
	dirs   map[string]bool
}

func (f *gitFilter) keeps(p string, dir bool) bool {
	if f == nil {
		return true
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(p, f.prefix), "/")
	if dir {
		return f.dirs[rel] || f.files[rel] // a submodule is listed like a file
	}
	return f.files[rel]
}

func (s *snapshots) filter(rel string) *gitFilter {
	names, err := s.list(path.Join(s.workspace, rel))
	if err != nil {
		return nil // not a usable work tree: keep everything but .git
	}
	f := &gitFilter{prefix: rel, files: map[string]bool{}, dirs: map[string]bool{}}
	for _, n := range names {
		if d, ok := strings.CutSuffix(n, "/"); ok { // a nested repository
			n = d
			f.dirs[n] = true
		} else {
			f.files[n] = true
		}
		for d := path.Dir(n); d != "."; d = path.Dir(d) {
			f.dirs[d] = true
		}
	}
	return f
}

// walker scans the workspace into manifest entries. With st set it stores the blobs of
// new content; base supplies hashes of files whose size and mtime didn't change, as long
// as the store still has their blob.
type walker struct {
	s     *snapshots
	st    *store
	base  map[string]mentry
	out   []mentry
	bytes int64
}

func (s *snapshots) scan(st *store, base map[string]mentry, save bool) (*walker, error) {
	root, err := unix.Open(s.workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(root)
	w := &walker{s: s, base: base}
	if save {
		w.st = st
	}
	if err := w.dir(root, "", nil, st); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *walker) dir(fd int, rel string, f *gitFilter, st *store) error {
	dup, err := unix.Dup(fd)
	if err != nil {
		return err
	}
	df := os.NewFile(uintptr(dup), rel)
	names, err := df.Readdirnames(-1)
	df.Close()
	if err != nil {
		return err
	}
	slices.Sort(names)
	if slices.Contains(names, ".git") {
		f = w.s.filter(rel)
	}
	for _, name := range names {
		if name == ".git" || (rel == "" && name == lostFound) {
			continue
		}
		p := path.Join(rel, name)
		var fs unix.Stat_t
		if err := unix.Fstatat(fd, name, &fs, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue // gone meanwhile
		}
		e := mentry{Path: p, Mode: fs.Mode & 0o7777, UID: int(fs.Uid), GID: int(fs.Gid)}
		switch fs.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if !f.keeps(p, true) {
				continue
			}
			e.Type = 'd'
			w.out = append(w.out, e)
			sub, err := unix.Openat2(fd, name, beneath)
			if err != nil {
				continue // replaced meanwhile
			}
			err = w.dir(sub, p, f, st)
			unix.Close(sub)
			if err != nil {
				return err
			}
		case unix.S_IFREG:
			if !f.keeps(p, false) {
				continue
			}
			e.Type, e.Size, e.MTime = 'f', fs.Size, fs.Mtim.Nano()
			if err := w.file(fd, name, &e, st); err != nil {
				continue // unreadable or gone: left out
			}
			w.out = append(w.out, e)
		case unix.S_IFLNK:
			if !f.keeps(p, false) {
				continue
			}
			buf := make([]byte, unix.PathMax)
			n, err := unix.Readlinkat(fd, name, buf)
			if err != nil {
				continue
			}
			e.Type, e.Target = 'l', string(buf[:n])
			w.out = append(w.out, e)
		}
		if len(w.out) > maxSnapshotEntries {
			return fmt.Errorf("the workspace has more than %d files to snapshot", maxSnapshotEntries)
		}
	}
	return nil
}

// file hashes a regular file, and stores it when the walker saves.
func (w *walker) file(dir int, name string, e *mentry, st *store) error {
	if e.Size > w.s.maxFile {
		return nil // too large: recorded without content
	}
	if b, ok := w.base[e.Path]; ok && b.Type == 'f' && b.Hash != "" && b.Size == e.Size && b.MTime == e.MTime && (st == nil || st.hasBlob(b.Hash)) {
		e.Hash = b.Hash
		w.bytes += e.Size
		return nil
	}
	data, err := readAt(dir, name, w.s.maxFile)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	e.Hash = hex.EncodeToString(sum[:])
	if w.st != nil {
		if err := w.st.putBlob(e.Hash, data); err != nil {
			return err
		}
	}
	w.bytes += e.Size
	return nil
}

// --- ops ------------------------------------------------------------------------------

func (s *snapshots) take(id, baseID string) (agentproto.SnapshotReply, error) {
	st, err := s.openStore()
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	defer st.close()
	var base *manifest
	if baseID != "" {
		if base, err = st.readManifest(baseID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return agentproto.SnapshotReply{}, err
		}
	}
	var baseIdx map[string]mentry
	if base != nil {
		baseIdx = base.index()
	}
	w, err := s.scan(st, baseIdx, true)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	m := &manifest{Entries: w.out}
	if err := st.writeManifest(id, m); err != nil {
		return agentproto.SnapshotReply{}, err
	}
	reply := agentproto.SnapshotReply{Files: len(m.Entries), Bytes: w.bytes}
	// The cap: drop the oldest snapshots until the blobs fit.
	size, err := st.collect()
	for err == nil && size > s.cap {
		var ids []string
		if ids, err = st.manifestIDs(); err != nil || len(ids) == 0 || ids[0] == id {
			break
		}
		if err = unix.Unlinkat(st.manifests, ids[0]+".json", 0); err == nil {
			reply.Dropped = append(reply.Dropped, ids[0])
			size, err = st.collect()
		}
	}
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	if base != nil {
		reply.Changes, reply.More = s.compare(st, base, m, "", false)
	}
	return reply, nil
}

func (s *snapshots) changes(baseID, id, only string) (agentproto.SnapshotReply, error) {
	st, err := s.openStore()
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	defer st.close()
	a, err := st.readManifest(baseID)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	b, err := st.readManifest(id)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	var reply agentproto.SnapshotReply
	reply.Changes, reply.More = s.compare(st, a, b, only, true)
	return reply, nil
}

// readable reports whether the agent user may read e, so its content may be shown.
func (s *snapshots) readable(e mentry) bool {
	return e.UID == s.owner[0] || e.Mode&0o004 != 0 || (e.GID == s.owner[1] && e.Mode&0o040 != 0)
}

// compare lists the files and symlinks that differ from a to b, in path order.
func (s *snapshots) compare(st *store, a, b *manifest, only string, diffs bool) ([]agentproto.FileChange, int) {
	ai, bi := a.index(), b.index()
	var paths []string
	for p, e := range ai {
		if e.Type != 'd' {
			if x, ok := bi[p]; !ok || x.Type != e.Type || x.Hash != e.Hash || x.Target != e.Target || x.Mode != e.Mode || (e.Hash == "" && (x.Size != e.Size || x.MTime != e.MTime)) {
				paths = append(paths, p)
			}
		}
	}
	for p, e := range bi {
		if _, ok := ai[p]; !ok && e.Type != 'd' {
			paths = append(paths, p)
		}
	}
	if only != "" {
		paths = slices.DeleteFunc(paths, func(p string) bool { return p != only })
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	more := 0
	if len(paths) > s.maxListed {
		more = len(paths) - s.maxListed
		paths = paths[:s.maxListed]
	}
	budget, diffBytes := int64(lineBudget), 0
	out := make([]agentproto.FileChange, 0, len(paths))
	for _, p := range paths {
		x, inA := ai[p]
		y, inB := bi[p]
		c := agentproto.FileChange{Path: p, Status: "modified"}
		switch {
		case !inA || (inB && x.Type == 'd'):
			c.Status = "added"
		case !inB || y.Type == 'd':
			c.Status = "deleted"
		}
		var before, after []byte
		ok := true
		load := func(e mentry, present bool) []byte {
			if !present || e.Type == 'd' {
				return nil
			}
			if e.Type == 'l' {
				return []byte(e.Target)
			}
			if e.Hash == "" || e.Size > agentproto.MaxReadBytes || !s.readable(e) {
				ok = false
				return nil
			}
			if budget -= e.Size; budget < 0 {
				ok = false
				return nil
			}
			data, err := st.readBlob(e.Hash, agentproto.MaxReadBytes)
			if err != nil || !isText(data) {
				ok = false
			}
			return data
		}
		before, after = load(x, inA), load(y, inB)
		switch {
		case !ok && budget < 0:
			c.Truncated = true
		case !ok:
			c.Binary = true
		default:
			edits := diffLines(splitLines(before), splitLines(after))
			c.Added, c.Removed = countEdits(edits)
			if diffs {
				if diffBytes >= agentproto.MaxDiffTotalBytes {
					c.Truncated = true
				} else {
					c.Diff, c.Truncated = unified(p, edits, min(agentproto.MaxDiffBytes, agentproto.MaxDiffTotalBytes-diffBytes))
					encoded, _ := json.Marshal(c.Diff) // the reply's size counts
					diffBytes += len(encoded)
				}
			}
		}
		out = append(out, c)
	}
	return out, more
}

// restore puts the workspace back as snapshot id has it. Paths the snapshot leaves out
// (.git, ignored files, files too large) are not touched.
func (s *snapshots) restore(id string) (agentproto.SnapshotReply, error) {
	st, err := s.openStore()
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	defer st.close()
	m, err := st.readManifest(id)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	want := m.index()
	root, err := unix.Open(s.workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	defer unix.Close(root)
	var reply agentproto.SnapshotReply
	fail := func(p string, err error) {
		if len(reply.Failed) < 100 {
			reply.Failed = append(reply.Failed, p+": "+err.Error())
		}
	}
	// The .gitignore files go back first, so what the snapshot ignored is still ignored
	// when the live tree is scanned for files to remove.
	for _, e := range m.Entries {
		if e.Type == 'f' && e.Hash != "" && path.Base(e.Path) == ".gitignore" {
			var fs unix.Stat_t
			err := unix.Fstatat(root, e.Path, &fs, unix.AT_SYMLINK_NOFOLLOW)
			if errors.Is(err, unix.ENOENT) || (err == nil && fs.Mode&unix.S_IFMT == unix.S_IFREG) {
				if err := restoreFile(root, st, e); err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ELOOP) && !errors.Is(err, unix.EXDEV) {
					fail(e.Path, err)
				}
			}
		}
	}
	live, err := s.scan(st, want, false)
	if err != nil {
		return agentproto.SnapshotReply{}, err
	}
	have := map[string]mentry{}
	for _, e := range live.out {
		have[e.Path] = e
	}

	// Remove what the snapshot doesn't have, deepest first.
	for i := len(live.out) - 1; i >= 0; i-- {
		e := live.out[i]
		w, ok := want[e.Path]
		if ok && w.Type == e.Type {
			continue
		}
		if e.Type == 'f' && e.Hash == "" && !ok {
			continue // too large to have been kept
		}
		err := removeAt(root, e.Path, e.Type == 'd')
		switch {
		case err == nil:
			reply.Removed++
		case errors.Is(err, unix.ENOTEMPTY) && !ok:
			// A new directory still holding what the snapshot leaves out.
		case !errors.Is(err, unix.ENOENT):
			fail(e.Path, err)
		}
	}
	// Then put back directories, files and symlinks, parents first.
	for _, e := range m.Entries {
		h, ok := have[e.Path]
		switch e.Type {
		case 'd':
			if ok && h.Type == 'd' && h.Mode == e.Mode && h.UID == e.UID && h.GID == e.GID {
				continue
			}
			err = restoreDir(root, e)
		case 'f':
			if e.Hash == "" || (ok && h.Type == 'f' && h.Hash == e.Hash && h.Mode == e.Mode && h.UID == e.UID && h.GID == e.GID) {
				continue
			}
			err = restoreFile(root, st, e)
		case 'l':
			if ok && h.Type == 'l' && h.Target == e.Target && h.UID == e.UID && h.GID == e.GID {
				continue
			}
			err = restoreLink(root, e)
		}
		if err != nil {
			fail(e.Path, err)
		} else {
			reply.Restored++
		}
	}
	return reply, nil
}

// parentAt opens the directory holding p, beneath root, without following symlinks.
func parentAt(root int, p string) (int, string, error) {
	d, base := path.Split(p)
	d = strings.TrimSuffix(d, "/")
	if d == "" {
		d = "."
	}
	fd, err := unix.Openat2(root, d, beneath)
	return fd, base, err
}

func removeAt(root int, p string, dir bool) error {
	fd, base, err := parentAt(root, p)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	flags := 0
	if dir {
		flags = unix.AT_REMOVEDIR
	}
	err = unix.Unlinkat(fd, base, flags)
	if errors.Is(err, unix.EEXIST) {
		err = unix.ENOTEMPTY
	}
	return err
}

func restoreDir(root int, e mentry) error {
	fd, base, err := parentAt(root, e.Path)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.Mkdirat(fd, base, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	dir, err := unix.Openat2(fd, base, beneath)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	return setOwnerMode(dir, e)
}

// setOwnerMode sets the owner before the mode, since chown clears setuid and setgid.
// Restores never set those bits.
func setOwnerMode(fd int, e mentry) error {
	if err := unix.Fchown(fd, e.UID, e.GID); err != nil && !errors.Is(err, unix.EPERM) {
		return err
	}
	return unix.Fchmod(fd, e.Mode&0o777|e.Mode&0o1000)
}

func restoreFile(root int, st *store, e mentry) error {
	data, err := st.readBlob(e.Hash, agentproto.MaxSnapshotFile)
	if err != nil {
		return err
	}
	fd, base, err := parentAt(root, e.Path)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	tmp := tempName(base)
	f, err := unix.Openat(fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(f), tmp)
	_, err = file.Write(data)
	if err == nil {
		err = setOwnerMode(f, e)
	}
	if err == nil {
		ts := [2]unix.Timespec{unix.NsecToTimespec(e.MTime), unix.NsecToTimespec(e.MTime)}
		if _, _, errno := unix.Syscall6(unix.SYS_UTIMENSAT, uintptr(f), 0, uintptr(unsafe.Pointer(&ts[0])), 0, 0, 0); errno != 0 {
			err = errno
		}
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Renameat(fd, tmp, fd, base)
	}
	if err != nil {
		unix.Unlinkat(fd, tmp, 0)
	}
	return err
}

func restoreLink(root int, e mentry) error {
	fd, base, err := parentAt(root, e.Path)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	tmp := tempName(base)
	if err := unix.Symlinkat(e.Target, fd, tmp); err != nil {
		return err
	}
	err = unix.Fchownat(fd, tmp, e.UID, e.GID, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.EPERM) {
		err = nil
	}
	if err == nil {
		err = unix.Renameat(fd, tmp, fd, base)
	}
	if err != nil {
		unix.Unlinkat(fd, tmp, 0)
	}
	return err
}
