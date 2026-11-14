package msbdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// UnsafeHome is a microsandbox home that msb 0.7.12 and later refuses to keep its database in:
// a directory on the way to it that another account could write to, and so swap the database
// out from under msb. Every msb call fails then, so Studio stops before it changes anything.
type UnsafeHome struct {
	Home string // microsandbox's home, with links resolved
	Dir  string // the directory that's the problem
	Why  string // what's wrong with it, such as "is writable by its group (mode 775)"
	Fix  string
}

func (e *UnsafeHome) Error() string {
	where := e.Dir + " " + e.Why
	if e.Dir != e.Home {
		where = e.Dir + ", above it, " + e.Why
	}
	return fmt.Sprintf("microsandbox won't keep its database in %s: %s, so another account could swap the database out. %s", e.Home, where, e.Fix)
}

// PrepareHome makes microsandbox's home ready for msb's ownership checks before msb sees it:
// it creates the home private to this account if it is missing, and makes an existing one of
// this account's private (0700), as msb does with its db directory. Directories above it are
// never changed: one another account could write to is an *UnsafeHome, with how to fix it.
// It reports whether it changed the home's mode.
func PrepareHome(home string) (tightened bool, err error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return false, err
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return false, err
	}
	tightened, err = makePrivate(real)
	if err != nil {
		return false, err
	}
	return tightened, CheckHome(home)
}

// CheckHome reports, without changing anything, whether msb can keep its database in home:
// nil, or an *UnsafeHome. A home that doesn't exist yet is checked as far as it does.
func CheckHome(home string) error {
	abs, err := filepath.Abs(home)
	if err != nil {
		return err
	}
	// msb resolves the home's links and checks the real directories.
	missing := ""
	real, err := filepath.EvalSymlinks(abs)
	for errors.Is(err, os.ErrNotExist) && filepath.Dir(abs) != abs {
		missing = filepath.Join(filepath.Base(abs), missing)
		abs = filepath.Dir(abs)
		real, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return err
	}
	resolved := filepath.Join(real, missing)
	// From the root down, as msb walks them: every directory until the first private one of
	// this account (0700, which keeps others out of everything below it).
	var dirs []string
	for d := real; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		why, mine, private, err := inspectDir(dirs[i])
		if err != nil {
			return err
		}
		if why != "" {
			return &UnsafeHome{Home: resolved, Dir: dirs[i], Why: why, Fix: unsafeFix(dirs[i], mine)}
		}
		if private {
			return nil
		}
	}
	return nil
}

func unsafeFix(dir string, mine bool) string {
	other := "start Studio with MSB_HOME set to a directory that only you can write to, all the way up"
	if mine {
		return fmt.Sprintf("Run `chmod g-w,o-w %s`, or %s.", dir, other)
	}
	return "Ask the directory's owner to make it writable only by them, or " + other + "."
}
