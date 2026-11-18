//go:build unix

package msbdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTempDir is a test directory that msb's rule applies to: below /tmp, sticky as it is, rather
// than Go's temporary directory, which on macOS sits in a private (0700) per-user folder that keeps
// everything below it safe, so nothing there could ever be refused.
func openTempDir(t *testing.T) string {
	tmp, err := os.MkdirTemp("/tmp", "msbdb-")
	if err != nil {
		t.Skipf("no directory in /tmp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(tmp, 0o755)
		_ = os.RemoveAll(tmp)
	})
	if err := os.Chmod(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckHome(filepath.Join(tmp, "probe")); err != nil {
		t.Skipf("the temporary directories are below one other accounts can change: %v", err)
	}
	// A private directory above would keep it safe however open it is.
	if err := os.Chmod(tmp, 0o775); err != nil {
		t.Fatal(err)
	}
	var u *UnsafeHome
	refused := errors.As(CheckHome(filepath.Join(tmp, "probe")), &u)
	if err := os.Chmod(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if !refused {
		t.Skip("a private directory above the temporary directories keeps everything below them safe")
	}
	return tmp
}

func TestCheckHomeRefusesAGroupWritableDirectoryAboveIt(t *testing.T) {
	tmp := openTempDir(t)
	home := filepath.Join(tmp, "work", ".microsandbox")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0o775); err != nil {
		t.Fatal(err)
	}
	var u *UnsafeHome
	if err := CheckHome(home); !errors.As(err, &u) {
		t.Fatalf("CheckHome = %v, want an *UnsafeHome", err)
	}
	real, _ := filepath.EvalSymlinks(tmp)
	if u.Dir != real || !strings.Contains(u.Why, "its group (mode 775)") || !strings.Contains(u.Fix, "chmod g-w,o-w "+real) {
		t.Errorf("got %+v", u)
	}
	// The same through a link: msb checks the real directories.
	link := filepath.Join(t.TempDir(), "msb")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckHome(link); !errors.As(err, &u) || u.Dir != real {
		t.Errorf("CheckHome through a link = %v", err)
	}
	// And one that doesn't exist yet, as on a first start.
	if err := CheckHome(filepath.Join(tmp, "new", "msb")); !errors.As(err, &u) || u.Dir != real {
		t.Errorf("CheckHome of a missing home = %v", err)
	}
	// A sticky one, as /tmp is, is fine.
	if err := os.Chmod(tmp, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckHome(home); err != nil {
		t.Errorf("CheckHome under a sticky directory = %v", err)
	}
}

func TestCheckHomeStopsAtAPrivateDirectory(t *testing.T) {
	tmp := openTempDir(t)
	inner := filepath.Join(tmp, "shared")
	home := filepath.Join(inner, ".microsandbox")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inner, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckHome(home); err != nil {
		t.Errorf("CheckHome below a private directory = %v", err)
	}
}

func TestPrepareHomeMakesTheHomePrivateAndLeavesItsParentsAlone(t *testing.T) {
	tmp := openTempDir(t)
	if err := os.Chmod(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, ".microsandbox")
	if err := os.Mkdir(home, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o775); err != nil { // past the umask
		t.Fatal(err)
	}
	tightened, err := PrepareHome(home)
	if err != nil || !tightened {
		t.Fatalf("PrepareHome = %v, %v", tightened, err)
	}
	if fi, _ := os.Stat(home); fi.Mode().Perm() != 0o700 {
		t.Errorf("home is %o, want 700", fi.Mode().Perm())
	}
	if again, err := PrepareHome(home); again || err != nil {
		t.Errorf("PrepareHome again = %v, %v", again, err)
	}
	// A missing one is made private.
	fresh := filepath.Join(tmp, "fresh")
	if _, err := PrepareHome(fresh); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o700 {
		t.Errorf("new home is %o, want 700", fi.Mode().Perm())
	}
	// A bad parent is reported, and not changed.
	if err := os.Chmod(tmp, 0o775); err != nil {
		t.Fatal(err)
	}
	var u *UnsafeHome
	if _, err := PrepareHome(home); !errors.As(err, &u) {
		t.Fatalf("PrepareHome under a group-writable parent = %v", err)
	}
	if fi, _ := os.Stat(tmp); fi.Mode().Perm() != 0o775 {
		t.Errorf("the parent became %o", fi.Mode().Perm())
	}
}

// The case from a Linux upgrade from msb 0.7.8: ~/.microsandbox was a link, and the folders the
// older msb made under umask 0002 were 775. msb 0.7.15 refused them on the link's path and
// wouldn't move any sandbox's disks.
func TestPrepareHomeTakesOthersWriteOffStorageBehindALink(t *testing.T) {
	tmp := openTempDir(t)
	real := filepath.Join(tmp, "git", ".microsandbox")
	mode := func(path string) os.FileMode {
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode() & (os.ModePerm | os.ModeSticky)
	}
	set := func(path string, m os.FileMode) {
		if err := os.Chmod(path, m); err != nil { // past the umask
			t.Fatal(err)
		}
	}
	sb := filepath.Join(real, "sandboxes", "ss-a-g1")
	tmpInGuest := filepath.Join(sb, "runtime", "rootfs", "upperfs", "tmp")
	layer := filepath.Join(real, "cache", "layers", "sha256-x", "tmp")
	volume := filepath.Join(sb, "owned-volumes", "workspace_1")
	for _, d := range []string{tmpInGuest, layer, volume, filepath.Join(real, "volumes", "named", "data"), filepath.Join(real, "db")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	disk := filepath.Join(volume, "disk.raw")
	if err := os.WriteFile(disk, nil, 0o664); err != nil {
		t.Fatal(err)
	}
	set(disk, 0o664)
	for _, d := range []string{real, filepath.Join(real, "sandboxes"), sb, filepath.Join(sb, "runtime"),
		filepath.Join(sb, "owned-volumes"), volume, filepath.Join(real, "cache"), filepath.Join(real, "cache", "layers"),
		filepath.Join(real, "volumes"), filepath.Join(real, "volumes", "named"), filepath.Join(real, "volumes", "named", "data")} {
		set(d, 0o775)
	}
	set(filepath.Join(sb, "runtime", "rootfs"), 0o700)
	set(tmpInGuest, os.ModeSticky|0o777)
	set(layer, os.ModeSticky|0o777)
	link := filepath.Join(tmp, ".microsandbox")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	tightened, err := PrepareHome(link)
	if err != nil || !tightened {
		t.Fatalf("PrepareHome = %v, %v", tightened, err)
	}
	for path, want := range map[string]os.FileMode{
		real:                             0o700,
		filepath.Join(real, "sandboxes"): 0o755,
		sb:                               0o755,
		filepath.Join(sb, "runtime"):     0o755,
		volume:                           0o755,
		filepath.Join(real, "cache"):     0o755,
		filepath.Join(real, "volumes"):   0o755,
		filepath.Join(real, "db"):        0o755,
		// What the guests see stays as it is: a guest's /tmp, an image layer's, a volume's.
		tmpInGuest:                             os.ModeSticky | 0o777,
		layer:                                  os.ModeSticky | 0o777,
		filepath.Join(real, "cache", "layers"): 0o775,
		filepath.Join(real, "volumes", "named", "data"): 0o775,
		disk: 0o664,
	} {
		if got := mode(path); got != want {
			t.Errorf("%s is %v, want %v", strings.TrimPrefix(path, tmp), got, want)
		}
	}
	if again, err := PrepareHome(link); again || err != nil {
		t.Errorf("PrepareHome again = %v, %v", again, err)
	}

	// A home that is no link is private itself, which keeps everything below it safe: its
	// folders stay as they are.
	set(sb, 0o775)
	if _, err := PrepareHome(real); err != nil {
		t.Fatal(err)
	}
	if got := mode(sb); got != 0o775 {
		t.Errorf("below a home that is no link, a sandbox's folder became %v", got)
	}
}

func TestParseACLGrants(t *testing.T) {
	trusted := map[string]bool{"user:root": true, "user:ada": true}
	for out, want := range map[string]string{
		"drwxr-x---+ 5 ada staff 160 Nov 14 /Users/ada\n 0: group:everyone deny delete\n":                   "",
		"drwxr-xr-x+ 5 ada staff 160 Nov 14 /x\n 0: group:staff allow list,search,readattr\n":               "",
		"drwxr-xr-x+ 5 ada staff 160 Nov 14 /x\n 0: user:ada allow add_file,delete_child\n":                 "",
		"drwxr-xr-x+ 5 ada staff 160 Nov 14 /x\n 0: group:staff allow list,add_file\n":                      "group:staff",
		"drwxr-xr-x+ 5 ada staff 160 Nov 14 /x\n 0: user:bob inherited allow delete_child\n":                "user:bob",
		"drwxr-xr-x  5 ada staff 160 Nov 14 /x\n":                                                           "",
		"drwxr-xr-x+ 5 ada staff 160 Nov 14 /x\n 0: group:everyone deny delete\n 1: user:eve allow chown\n": "user:eve",
	} {
		if got := parseACLGrants(out, trusted); got != want {
			t.Errorf("parseACLGrants(%q) = %q, want %q", out, got, want)
		}
	}
}
