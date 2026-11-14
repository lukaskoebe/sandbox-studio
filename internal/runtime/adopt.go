package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
)

// adoptDir is where AdoptDisks keeps a VM's directory while msb makes it a new record, in
// microsandbox's home so that moving it there is a rename.
const adoptDir = "studio-repair"

// ErrNoDisks means msb has no record of a VM and no directory of its disks is left either.
var ErrNoDisks = errors.New("the VM's disks are gone too")

// VMDirectory is the directory msb keeps a VM's disks in, whether or not its catalog still
// has a record of the VM.
func VMDirectory(name string) (string, error) {
	home, err := msbdb.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sandboxes", name), nil
}

// LostDisks returns the directory holding the disks of the VM name, which msb's catalog has
// no record of: msb's own directory for it, or what an interrupted AdoptDisks kept aside. It
// returns ErrNoDisks when there is neither.
func LostDisks(name string) (string, error) {
	dir, err := VMDirectory(name)
	if err != nil {
		return "", err
	}
	if hasDisks(dir) {
		return dir, nil
	}
	kept, err := keptDisks(filepath.Dir(filepath.Dir(dir)), name)
	if err != nil {
		return "", err
	}
	if kept == "" {
		return "", ErrNoDisks
	}
	return kept, nil
}

// hasDisks reports whether dir is a VM's directory with its owned disks.
func hasDisks(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, "owned-volumes"))
	return err == nil && info.IsDir()
}

// keptDisks returns the newest directory of name's disks an interrupted AdoptDisks left in
// home's adoptDir, or "".
func keptDisks(home, name string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(home, adoptDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), name+".") {
			if d := filepath.Join(home, adoptDir, e.Name(), "disks"); hasDisks(d) {
				found = append(found, d)
			}
		}
	}
	// The names end in a UTC time, so they sort by it.
	slices.Sort(found)
	if len(found) == 0 {
		return "", nil
	}
	return found[len(found)-1], nil
}

// AdoptDisks gives the VM name, which msb's catalog has lost, a new record around the disks
// it left: msb makes a new VM from spec in transfer mode, the way it made the lost one, and
// once it is stopped its directory is swapped for the lost VM's, so the record now points at
// the old disks, root disk and checkpoints included. The VM is left stopped; starting it
// boots the old disks. spec must be the one the lost VM was made with: its disk sizes are
// checked against the disks when it starts.
//
// The old directory is first moved aside into microsandbox's home, where LostDisks finds it if
// Studio stops part way, and is moved back if msb can't make the record.
func (r *Runtime) AdoptDisks(ctx context.Context, name string, spec Spec, agentSocket string, labels map[string]string) (err error) {
	status, err := r.Status(ctx, name)
	if err != nil {
		return err
	}
	if status != StatusAbsent {
		return fmt.Errorf("msb still has a record of %s", name)
	}
	disks, err := LostDisks(name)
	if err != nil {
		return err
	}
	dir, err := VMDirectory(name)
	if err != nil {
		return err
	}
	home := filepath.Dir(filepath.Dir(dir))
	aside := filepath.Dir(disks) // the directory of an earlier, interrupted adoption
	if disks == dir {
		aside = filepath.Join(home, adoptDir, name+"."+time.Now().UTC().Format("20060102T150405Z"))
		if err := os.MkdirAll(aside, 0o700); err != nil {
			return err
		}
		if err := os.Rename(dir, filepath.Join(aside, "disks")); err != nil {
			return fmt.Errorf("move the VM's disks aside: %w", err)
		}
		disks = filepath.Join(aside, "disks")
	} else if _, err := os.Lstat(dir); err == nil {
		// msb's directory for the name is a leftover without disks, from a record that was
		// never made: msb won't make a new one over it.
		if err := os.Rename(dir, filepath.Join(aside, fmt.Sprintf("leftover-%d", time.Now().UnixNano()))); err != nil {
			return fmt.Errorf("move a leftover directory aside: %w", err)
		}
	}
	defer func() {
		if err == nil {
			return
		}
		// Undo the new record, whose directory holds only empty disks, and put the old disks
		// back where msb keeps them, so that the next try finds them there.
		if _, statErr := os.Lstat(dir); statErr == nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if rmErr := r.Remove(cleanup, name); rmErr != nil {
				err = errors.Join(err, fmt.Errorf("remove the new record: %v; the disks stay in %s", rmErr, disks))
				return
			}
			if _, statErr := os.Lstat(dir); statErr == nil {
				err = errors.Join(err, fmt.Errorf("the new record's directory %s is still there; the disks stay in %s", dir, disks))
				return
			}
		}
		if back := os.Rename(disks, dir); back != nil {
			err = errors.Join(err, fmt.Errorf("the disks stay in %s: %v", disks, back))
		}
	}()

	if err := r.create(ctx, name, spec, agentSocket, labels, false); err != nil {
		return fmt.Errorf("make a new record: %w", err)
	}
	if err := r.Stop(ctx, name); err != nil {
		return fmt.Errorf("stop the new VM: %w", err)
	}
	fresh := filepath.Join(aside, "new")
	if err := os.Rename(dir, fresh); err != nil {
		return fmt.Errorf("move the new VM's directory aside: %w", err)
	}
	if err := os.Rename(disks, dir); err != nil {
		if back := os.Rename(fresh, dir); back != nil {
			return errors.Join(fmt.Errorf("put the disks in place: %w", err), fmt.Errorf("put the new VM's directory back: %v", back))
		}
		return fmt.Errorf("put the disks in place: %w", err)
	}
	// What is left aside is the new VM's empty disks and leftovers.
	if err := os.RemoveAll(aside); err != nil {
		return fmt.Errorf("the VM has its disks back, but %s could not be removed: %w", aside, err)
	}
	_ = os.Remove(filepath.Join(home, adoptDir)) // only when empty
	return nil
}

// LostDisks is the package's LostDisks, for the sandbox manager.
func (r *Runtime) LostDisks(name string) (string, error) { return LostDisks(name) }
