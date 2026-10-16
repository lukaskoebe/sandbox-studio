package gitreview

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/file"
)

// Local forges: kind local is a folder of repositories on this computer, such as ~/git.
// https://git.studio.internal/<forge>/<path>.git is the repository at <folder>/<path>: a
// working copy, or a bare repository <path>.git or <path>. Studio runs the host's git as a
// push to the folder or a fetch from it would: git upload-pack for the sandboxes' fetches
// and the staging mirror, git receive-pack for an approved push. receive-pack follows the
// repository's own config and runs its hooks, so a push to the branch a working copy has
// checked out is refused unless receive.denyCurrentBranch is updateInstead.

// CheckLocalPath normalizes the folder of a local forge: an absolute path, or one under
// the home directory as ~/path, of an existing folder other than the file system's root.
func CheckLocalPath(raw string) (ForgeURL, error) {
	raw = strings.TrimSpace(raw)
	bad := func(why string) (ForgeURL, error) {
		return ForgeURL{}, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if len(raw) > 4096 {
		return bad("the path is too long")
	}
	raw = strings.TrimPrefix(raw, "file://")
	if rest, ok := strings.CutPrefix(raw, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		home, err := os.UserHomeDir()
		if err != nil {
			return bad("this computer has no home directory to resolve ~ in")
		}
		raw = home + rest
	}
	if !filepath.IsAbs(raw) {
		return bad("give the folder's full path, such as /home/me/git or ~/git")
	}
	dir := filepath.Clean(raw)
	if filepath.Dir(dir) == dir {
		return bad("pick a folder of repositories, not the whole file system")
	}
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return bad(dir + " does not exist on this computer")
	case err != nil:
		return bad(fmt.Sprintf("Studio cannot read %s: %v", dir, err))
	case !info.IsDir():
		return bad(dir + " is not a folder")
	}
	return ForgeURL{Base: dir}, nil
}

// localRepo finds the repository a local forge's target names: <folder>/<path>.git, a bare
// repository, or <folder>/<path>, a working copy or a bare repository. splitPath already
// refused ".." and segments starting with a dot, so the path stays in the folder.
func localRepo(t target) (string, error) {
	p := filepath.Join(t.forge.BaseURL, filepath.FromSlash(t.path()))
	for _, dir := range []string{p + ".git", p} {
		if isFile(filepath.Join(dir, ".git")) || isFile(filepath.Join(dir, "HEAD")) && isDir(filepath.Join(dir, "objects")) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%w: %s has no repository %s", transport.ErrRepositoryNotFound, t.forge.BaseURL, t.path())
}

func isFile(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// localRemote is the go-git transport to a local forge's repository: the host's
// git-upload-pack and git-receive-pack, as git push and fetch to a path run them.
func (s *Service) localRemote(t target) (*remote, error) {
	dir, err := localRepo(t)
	if err != nil {
		return nil, err
	}
	ep, err := transport.NewEndpoint(dir)
	if err != nil {
		return nil, err
	}
	return &remote{ep: ep, tr: file.DefaultClient}, nil
}

// gitEnv is the environment for the host's git: Studio's own without git's variables, so
// that no GIT_DIR or GIT_CONFIG_* of Studio's steers it, and GIT_PROTOCOL if asked for.
func gitEnv(protocol string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	if protocol != "" {
		env = append(env, "GIT_PROTOCOL="+protocol)
	}
	return env
}

// localUploadPack serves one smart HTTP request of a fetch from a local forge with the
// host's git upload-pack, as git http-backend does.
func (s *Service) localUploadPack(w http.ResponseWriter, r *http.Request, t target) {
	dir, err := localRepo(t)
	if err != nil {
		refuse(w, http.StatusNotFound, fmt.Sprintf("There is no repository %s in %s on this computer.", t.path(), t.forge.BaseURL))
		return
	}
	protocol := ""
	if strings.Contains(r.Header.Get("Git-Protocol"), "version=2") {
		protocol = "version=2"
	}
	args := []string{"upload-pack", "--stateless-rpc"}
	if t.op == "info/refs" {
		args = append(args, "--advertise-refs")
	}
	cmd := exec.CommandContext(r.Context(), "git", append(args, dir)...)
	cmd.Env = gitEnv(protocol)
	stderr := &tail{}
	cmd.Stderr = stderr
	failed := func(why string) string {
		if msg := stderr.String(); msg != "" {
			return why + ": " + msg
		}
		return why
	}
	if t.op == "info/refs" {
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			refuse(w, http.StatusBadGateway, "Sandbox Studio could not run git on this computer: "+err.Error())
			return
		}
		adv, v2, err := readAdvertisement(out)
		if err != nil {
			_ = cmd.Wait()
			refuse(w, http.StatusBadGateway, failed("git on this computer could not read "+t.path()))
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if !v2 {
			e := pktline.NewEncoder(w)
			_ = e.EncodeString("# service=git-upload-pack\n")
			e.Flush()
		}
		_, _ = w.Write(adv)
		_, _ = io.Copy(io.Discard, out)
		_ = cmd.Wait()
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, s.Limits.MaxRequest)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			refuse(w, http.StatusBadRequest, "The fetch request is not valid gzip.")
			return
		}
		body = io.LimitReader(zr, s.Limits.MaxRequest)
	}
	in := &readErr{r: body}
	cmd.Stdin = in
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		refuse(w, http.StatusBadGateway, "Sandbox Studio could not run git on this computer: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	wrote := false
	for {
		n, err := out.Read(buf)
		if n > 0 {
			if !wrote {
				w.WriteHeader(http.StatusOK)
				wrote = true
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			rc.Flush()
		}
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	if wrote {
		return
	}
	var tooBig *http.MaxBytesError
	if errors.As(in.err, &tooBig) {
		refuse(w, http.StatusRequestEntityTooLarge, "The fetch request is too large.")
		return
	}
	refuse(w, http.StatusBadGateway, failed("git on this computer ended the fetch"))
}

// readErr keeps the error its reader ended with other than io.EOF.
type readErr struct {
	r   io.Reader
	err error
}

func (e *readErr) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF {
		e.err = err
	}
	return n, err
}
