package gitreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// storeID matches store.NewID's output: lowercase base32, no padding.
var storeID = regexp.MustCompile(`^[a-z2-7]{1,32}$`)

// sandboxDir returns <Dir>/<envID>/<sandboxID>, the directory holding all of one sandbox's
// staging repositories. It refuses IDs that aren't store IDs and any path whose components
// below Dir aren't plain directories, so removing it can't reach outside Dir.
func (s *Service) sandboxDir(envID, sandboxID string) (string, error) {
	if !storeID.MatchString(envID) || !storeID.MatchString(sandboxID) {
		return "", fmt.Errorf("staging: invalid id %q/%q", envID, sandboxID)
	}
	root, err := filepath.Abs(s.Dir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, envID, sandboxID)
	if rel, err := filepath.Rel(root, dir); err != nil || rel != filepath.Join(envID, sandboxID) {
		return "", fmt.Errorf("staging: %q is outside %q", dir, root)
	}
	for _, p := range []string{filepath.Join(root, envID), dir} {
		fi, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("staging: %q is not a directory", p)
		}
	}
	return dir, nil
}

// ForgetSandbox removes the staging repositories of a deleted sandbox.
func (s *Service) ForgetSandbox(envID, sandboxID string) error {
	dir, err := s.sandboxDir(envID, sandboxID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	os.Remove(filepath.Dir(dir)) // the environment's directory, if that was its last sandbox
	return nil
}

// Sweep removes the staging repositories of sandboxes that are no longer in the store,
// such as those deleted while an older Studio ran. Sandboxes that still exist keep theirs,
// whatever their state.
func (s *Service) Sweep(ctx context.Context) error {
	s.httpClient() // defaults the logger
	envs, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, env := range envs {
		// DirEntry types come from Lstat: symlinks are skipped, never followed.
		if !env.IsDir() || !storeID.MatchString(env.Name()) {
			s.Log.Warn("git remote: unexpected entry in staging root", "name", env.Name())
			continue
		}
		boxes, err := os.ReadDir(filepath.Join(s.Dir, env.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, box := range boxes {
			if !box.IsDir() || !storeID.MatchString(box.Name()) {
				s.Log.Warn("git remote: unexpected entry in staging root", "name", filepath.Join(env.Name(), box.Name()))
				continue
			}
			_, err := s.Store.Sandbox(ctx, env.Name(), box.Name())
			if err == nil {
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				errs = append(errs, err)
				continue
			}
			if err := s.ForgetSandbox(env.Name(), box.Name()); err != nil {
				errs = append(errs, err)
				continue
			}
			s.Log.Info("git remote: removed staging of deleted sandbox", "environment", env.Name(), "sandbox", box.Name())
		}
	}
	return errors.Join(errs...)
}
