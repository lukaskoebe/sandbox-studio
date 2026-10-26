//go:build linux

package guest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Sandboxes have the agent user's home on its own owned disk, so fork, rebase and export
// carry it like the workspace (docs/sandboxes.md). Older sandboxes, browser VMs and
// template build VMs keep it on the root disk. The disk hides the image's /home/agent, so
// the supervisor seeds an empty disk from the skeleton the image keeps, once; a home filled
// by home-import is never seeded.

// Home is the agent user's home.
const Home = "/home/agent"

// HomeSkel is the agent user's home as the image left it. The base image and the template
// build copy it there last, so a new home disk starts with what the image's home had.
const HomeSkel = "/usr/share/sandbox-studio/home-skel"

const (
	// homeSeeded marks a home disk that was seeded or filled by home-import.
	homeSeeded = ".sandbox-studio-home"
	// legacyMiseDir held the toolchains of templates built before they moved to /opt/mise.
	legacyMiseDir = ".local/share/mise"
	// legacyProfileStart and legacyProfileEnd delimit the login profile block those
	// templates appended to ~/.profile.
	legacyProfileStart = "# sandbox-studio managed mise login profile"
	legacyProfileEnd   = `eval "$(/usr/local/bin/mise activate bash)"`
	maxProfileBytes    = 1 << 20
)

// prepareHome seeds the home disk from the skeleton on the first boot. It does nothing
// when the home is on the root disk or already marked.
func prepareHome() error {
	mounted, err := isMountpoint(Home)
	if err != nil || !mounted {
		return err
	}
	if _, err := os.Lstat(filepath.Join(Home, homeSeeded)); err == nil {
		return nil
	}
	skel := HomeSkel
	if _, err := os.Lstat(skel); err != nil {
		// An image from before the skeleton still gets the distribution's dotfiles.
		skel = "/etc/skel"
	}
	return errors.Join(SeedHome(skel, Home, os.Stderr), ownHome())
}

// ownHome gives the home disk's root to the agent user if it is still root's: there was
// no skeleton to seed from, or seeding failed before it set the root's owner.
func ownHome() error {
	st, err := os.Lstat(Home)
	if err != nil || st.Sys().(*syscall.Stat_t).Uid != 0 {
		return err
	}
	acct, err := lookupAccount(User)
	if err != nil {
		return err
	}
	if err := os.Chown(Home, int(acct.uid), int(acct.gid)); err != nil {
		return err
	}
	return os.Chmod(Home, 0o755)
}

// SeedHome copies skel into home and marks it, unless home is marked already. Owners,
// modes, mtimes, symlinks and hardlinks are kept and no symlink is followed, as in a
// workspace copy. A home that holds files but no mark (a seed cut short) is only marked,
// so nothing in it is ever overwritten.
func SeedHome(skel, home string, warn io.Writer) error {
	if _, err := os.Lstat(filepath.Join(home, homeSeeded)); err == nil {
		return nil
	}
	if checkEmpty(home) == nil {
		if st, err := os.Lstat(skel); err == nil && st.IsDir() {
			if err := copyTree(skel, home, warn); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(warn, "the image has no %s to seed the home disk from\n", skel)
		}
	}
	return markHome(home)
}

func copyTree(src, dst string, warn io.Writer) error {
	pr, pw := io.Pipe()
	exported := make(chan error, 1)
	go func() {
		err := ExportWorkspaceAs(src, pw, warn, Zstd)
		pw.CloseWithError(err)
		exported <- err
	}()
	err := ImportWorkspace(dst, pr)
	pr.CloseWithError(errors.New("import ended"))
	return errors.Join(<-exported, err)
}

// ExportHome writes home as a workspace archive. A home on the root disk (a sandbox from
// before the home disk) leaves out the toolchains its template put there: templates now
// keep them in /opt/mise.
func ExportHome(home string, w io.Writer, warn io.Writer) error {
	return ExportHomeAs(home, w, warn, Gzip)
}

// ExportHomeAs is ExportHome with the archive compressed as c.
func ExportHomeAs(home string, w io.Writer, warn io.Writer, c Compression) error {
	mounted, err := isMountpoint(home)
	if err != nil {
		return err
	}
	var skip func(string) bool
	if !mounted {
		skip = func(rel string) bool { return rel == legacyMiseDir }
	}
	return exportTree(home, w, warn, skip, c)
}

// ImportHome extracts a home archive into the empty home disk, drops the mise login
// profile block of templates built before /opt/mise, and marks the home so it is never
// seeded.
func ImportHome(home string, r io.Reader) error {
	if err := ImportWorkspace(home, r); err != nil {
		return err
	}
	if err := stripLegacyProfile(home); err != nil {
		return err
	}
	return markHome(home)
}

// markHome writes the seed mark, owned like the home itself.
func markHome(home string) error {
	st, err := os.Lstat(home)
	if err != nil {
		return err
	}
	sys := st.Sys().(*syscall.Stat_t)
	homeFD, err := unix.Open(home, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(homeFD)
	fd, err := unix.Openat(homeFD, homeSeeded, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), homeSeeded)
	_, err = f.WriteString("Sandbox Studio seeded this home disk; it is not seeded again.\n")
	if err == nil {
		err = unix.Fchown(fd, int(sys.Uid), int(sys.Gid))
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// stripLegacyProfile removes the mise block that templates built before /opt/mise
// appended to the login profile. It would point mise and PATH back into the home.
func stripLegacyProfile(home string) error {
	homeFD, err := unix.Open(home, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(homeFD)
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		fd, err := unix.Openat2(homeFD, name, &unix.OpenHow{
			Flags:   unix.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
		})
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		err = stripProfileFD(fd)
		unix.Close(fd)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func stripProfileFD(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size > maxProfileBytes {
		return nil
	}
	data := make([]byte, st.Size)
	for off := 0; off < len(data); {
		n, err := unix.Pread(fd, data[off:], int64(off))
		if err != nil {
			return err
		}
		if n == 0 {
			data = data[:off]
			break
		}
		off += n
	}
	stripped, ok := withoutLegacyBlock(data)
	if !ok {
		return nil
	}
	if err := unix.Ftruncate(fd, 0); err != nil {
		return err
	}
	_, err := unix.Pwrite(fd, stripped, 0)
	return err
}

// withoutLegacyBlock returns data without the lines from legacyProfileStart through
// legacyProfileEnd, and whether it found them.
func withoutLegacyBlock(data []byte) ([]byte, bool) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	start := -1
	for i, line := range lines {
		text := string(bytes.TrimRight(line, "\n"))
		if start < 0 && text == legacyProfileStart {
			start = i
		} else if start >= 0 && text == legacyProfileEnd {
			return bytes.Join(append(lines[:start:start], lines[i+1:]...), nil), true
		}
	}
	return data, false
}

// isMountpoint reports whether p is on another file system than its parent. A missing
// p is not.
func isMountpoint(p string) (bool, error) {
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	parent, err := os.Lstat(filepath.Dir(p))
	if err != nil {
		return false, err
	}
	return st.Sys().(*syscall.Stat_t).Dev != parent.Sys().(*syscall.Stat_t).Dev, nil
}
