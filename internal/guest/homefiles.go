//go:build linux

package guest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// writeHomeFiles writes files below home, owned by uid and gid. Paths are relative and are
// resolved with openat2 beneath home without following any symlink, so a file in the
// untrusted home can't redirect a write elsewhere. Each file is written to a temporary
// file in its directory and renamed over the target, so readers never see half a file and
// a hardlinked target is replaced rather than written through. Missing directories are
// created with mode 0755.
func writeHomeFiles(home string, uid, gid int, files []agentproto.HomeFile) error {
	if err := checkHomeFiles(files); err != nil {
		return err
	}
	root, err := unix.Open(home, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open home: %w", err)
	}
	defer unix.Close(root)
	for _, f := range files {
		if err := writeHomeFile(root, uid, gid, f); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return nil
}

func checkHomeFiles(files []agentproto.HomeFile) error {
	if len(files) == 0 || len(files) > agentproto.MaxHomeFiles {
		return fmt.Errorf("between 1 and %d files can be written at once", agentproto.MaxHomeFiles)
	}
	for _, f := range files {
		if _, err := cleanName(f.Path); err != nil || f.Path != strings.TrimSuffix(f.Path, "/") {
			return fmt.Errorf("invalid path %q", f.Path)
		}
		if len(f.Content) > agentproto.MaxHomeFileBytes {
			return fmt.Errorf("%s is larger than %d KiB", f.Path, agentproto.MaxHomeFileBytes>>10)
		}
		if f.Mode&^0o755 != 0 {
			return fmt.Errorf("%s: mode %o is not allowed", f.Path, f.Mode)
		}
		begin, end := markers(f)
		if f.Block && (!strings.HasPrefix(f.Content, begin) ||
			!strings.HasSuffix(strings.TrimRight(f.Content, "\n"), end) ||
			strings.Count(f.Content, agentproto.BlockBegin) != 1 || strings.Count(f.Content, agentproto.BlockEnd) != 1) {
			return fmt.Errorf("%s: a managed block must start and end with its markers", f.Path)
		}
		if f.Comment != "" && (!f.Block || len(f.Comment) > 8 || strings.ContainsAny(f.Comment, "\n\r")) {
			return fmt.Errorf("%s: a comment is a short prefix of a managed block's markers", f.Path)
		}
		if f.Keys {
			var keys map[string]any
			if f.Block || json.Unmarshal([]byte(f.Content), &keys) != nil || keys == nil {
				return fmt.Errorf("%s: keys must be a JSON object, and not a managed block", f.Path)
			}
		}
		if f.Defaults != "" {
			var defaults map[string]any
			if !f.Keys || json.Unmarshal([]byte(f.Defaults), &defaults) != nil || defaults == nil {
				return fmt.Errorf("%s: defaults must be a JSON object, with keys", f.Path)
			}
		}
	}
	return nil
}

var resolveHome = &unix.OpenHow{
	Flags:   unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC,
	Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
}

func writeHomeFile(root, uid, gid int, f agentproto.HomeFile) error {
	parts := strings.Split(f.Path, "/")
	dir, err := unix.Dup(root)
	if err != nil {
		return err
	}
	defer func() { unix.Close(dir) }()
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat2(dir, part, resolveHome)
		if errors.Is(err, unix.ENOENT) {
			if err := unix.Mkdirat(dir, part, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
				return err
			}
			if err := unix.Fchownat(dir, part, uid, gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			next, err = unix.Openat2(dir, part, resolveHome)
		}
		if err != nil {
			return err
		}
		unix.Close(dir)
		dir = next
	}
	base := parts[len(parts)-1]

	content := f.Content
	if f.Keys {
		existing, err := readExisting(dir, base, agentproto.MaxKeysFileBytes)
		if err != nil {
			return err
		}
		if content, err = mergeKeys(existing, f.Content, f.Defaults); err != nil {
			return err
		}
		if len(content) > agentproto.MaxKeysFileBytes {
			return errors.New("the file would grow too large")
		}
	}
	if f.Block {
		existing, err := readExisting(dir, base, agentproto.MaxHomeFileBytes)
		if err != nil {
			return err
		}
		begin, end := markers(f)
		if strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimRight(f.Content, "\n"), begin), end)) == "" {
			// An empty block removes the file's block, if it has one.
			if !strings.Contains(existing, begin) {
				return nil
			}
			content, err = removeBlock(existing, begin, end)
		} else {
			content, err = mergeBlock(existing, f.Content, begin, end)
		}
		if err != nil {
			return err
		}
		if len(content) > 2*agentproto.MaxHomeFileBytes {
			return errors.New("the file would grow too large")
		}
	}

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := "." + base + ".studio-" + hex.EncodeToString(suffix[:])
	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), tmp)
	_, err = io.WriteString(file, content)
	if err == nil {
		err = unix.Fchown(fd, uid, gid)
	}
	if err == nil {
		err = unix.Fchmod(fd, f.Mode)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Renameat(dir, tmp, dir, base)
	}
	if err != nil {
		unix.Unlinkat(dir, tmp, 0)
	}
	return err
}

// readExisting returns the content of a regular file base in dir, or "" if there is none.
// A symlink, directory or other special file is refused.
func readExisting(dir int, base string, limit int) (string, error) {
	fd, err := unix.Openat2(dir, base, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOENT) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), base)
	defer file.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return "", errors.New("the existing file is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(b) > limit {
		return "", fmt.Errorf("the existing file is larger than %d KiB", limit>>10)
	}
	return string(b), nil
}

// markers are the lines a managed block of f starts and ends with.
func markers(f agentproto.HomeFile) (begin, end string) {
	return f.Comment + agentproto.BlockBegin, f.Comment + agentproto.BlockEnd
}

// mergeBlock replaces the managed block of existing, from the marker begin to end, with
// block, or puts block first if existing has none. Text outside the block is kept as it is.
func mergeBlock(existing, block, beginMarker, endMarker string) (string, error) {
	block = strings.TrimRight(block, "\n") + "\n"
	begin, end, err := findBlock(existing, beginMarker, endMarker)
	if err != nil {
		return "", err
	}
	if begin < 0 {
		if existing == "" {
			return block, nil
		}
		return block + "\n" + existing, nil
	}
	return existing[:begin] + block + existing[end:], nil
}

// removeBlock removes the managed block of existing, and the blank line mergeBlock put after
// it.
func removeBlock(existing, beginMarker, endMarker string) (string, error) {
	begin, end, err := findBlock(existing, beginMarker, endMarker)
	if err != nil || begin < 0 {
		return existing, err
	}
	if begin == 0 && strings.HasPrefix(existing[end:], "\n") {
		end++
	}
	return existing[:begin] + existing[end:], nil
}

// findBlock returns where the managed block of existing starts and ends, its line end
// included, or -1 when it has none.
func findBlock(existing, beginMarker, endMarker string) (begin, end int, err error) {
	begin = strings.Index(existing, beginMarker)
	if begin < 0 {
		if strings.Contains(existing, endMarker) {
			return -1, -1, errors.New("the file has an end marker without a begin marker")
		}
		return -1, -1, nil
	}
	end = strings.Index(existing[begin:], endMarker)
	if end < 0 {
		return -1, -1, errors.New("the managed block in the file has no end marker")
	}
	end += begin + len(endMarker)
	if end < len(existing) && existing[end] == '\n' {
		end++
	}
	return begin, end, nil
}

// mergeKeys merges the JSON object keys into the JSON object existing (agentproto.HomeFile
// Keys). A missing or blank file counts as an empty object; one that isn't a JSON object is
// left alone with an error rather than replaced.
func mergeKeys(existing, keys, defaults string) (string, error) {
	file := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &file); err != nil || file == nil {
			return "", errors.New("the existing file is not a JSON object")
		}
	}
	var add map[string]any
	if err := json.Unmarshal([]byte(keys), &add); err != nil {
		return "", err
	}
	mergeObject(file, add)
	if defaults != "" {
		var missing map[string]any
		if err := json.Unmarshal([]byte(defaults), &missing); err != nil {
			return "", err
		}
		fillObject(file, missing)
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// fillObject sets src's keys that dst lacks, and inside objects both have, the same way.
func fillObject(dst, src map[string]any) {
	for k, v := range src {
		have, ok := dst[k]
		if !ok {
			dst[k] = v
			continue
		}
		sub, isObject := v.(map[string]any)
		if haveObject, hasObject := have.(map[string]any); isObject && hasObject {
			fillObject(haveObject, sub)
		}
	}
}

func mergeObject(dst, src map[string]any) {
	for k, v := range src {
		sub, isObject := v.(map[string]any)
		have, hasObject := dst[k].(map[string]any)
		if isObject && hasObject {
			mergeObject(have, sub)
			continue
		}
		dst[k] = v
	}
}

// writeHomeDir replaces the directory dir of home with one holding exactly files: it writes
// them into a new directory beside it, owned by uid and gid, and swaps the two with
// RENAME_EXCHANGE, so a reader sees either the old copy or the new one. The old copy is
// removed afterwards.
func writeHomeDir(home string, uid, gid int, req agentproto.HomeDir) error {
	if req.Dir == "" || strings.ContainsAny(req.Dir, "/\x00") || req.Dir == "." || req.Dir == ".." || strings.HasPrefix(req.Dir, ".") {
		return fmt.Errorf("invalid directory %q", req.Dir)
	}
	if len(req.Files) > agentproto.MaxHomeDirFiles {
		return fmt.Errorf("at most %d files", agentproto.MaxHomeDirFiles)
	}
	total := 0
	for _, f := range req.Files {
		if _, err := cleanName(f.Path); err != nil || f.Path != strings.TrimSuffix(f.Path, "/") {
			return fmt.Errorf("invalid path %q", f.Path)
		}
		if f.Mode&^0o755 != 0 || f.Block || f.Keys {
			return fmt.Errorf("%s: only plain files with a mode of at most 0755", f.Path)
		}
		total += len(f.Content)
	}
	if total > agentproto.MaxHomeDirBytes {
		return fmt.Errorf("the files are larger than %d MiB together", agentproto.MaxHomeDirBytes>>20)
	}
	root, err := unix.Open(home, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open home: %w", err)
	}
	defer unix.Close(root)
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := "." + req.Dir + ".studio-" + hex.EncodeToString(suffix[:])
	if err := unix.Mkdirat(root, tmp, 0o755); err != nil {
		return err
	}
	cleanup := tmp
	defer func() {
		if cleanup != "" {
			os.RemoveAll(home + "/" + cleanup)
		}
	}()
	if err := unix.Fchownat(root, tmp, uid, gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	dir, err := unix.Openat2(root, tmp, resolveHome)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	for _, f := range req.Files {
		if err := writeHomeFile(dir, uid, gid, f); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	err = unix.Renameat2(root, tmp, root, req.Dir, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.ENOENT) {
		// No copy yet: the new one takes its name.
		if err = unix.Renameat2(root, tmp, root, req.Dir, unix.RENAME_NOREPLACE); err == nil {
			cleanup = ""
		}
	}
	return err // on an exchange, cleanup removes the old copy, now at tmp
}
