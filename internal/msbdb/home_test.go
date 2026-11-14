//go:build unix

package msbdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTempDir is a test directory without the private (0700) one Go makes above it, which would
// keep everything below it private.
func openTempDir(t *testing.T) string {
	tmp := t.TempDir()
	if err := CheckHome(filepath.Dir(filepath.Dir(tmp))); err != nil {
		t.Skipf("the temporary directories are below one other accounts can change: %v", err)
	}
	if err := os.Chmod(filepath.Dir(tmp), 0o755); err != nil {
		t.Fatal(err)
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
