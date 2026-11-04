//go:build linux

package guest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

// MiseDir holds the toolchains and the global mise config of the agent user: the template's,
// and what `mise use -g` added in the sandbox. It is on the root disk, which a rebase replaces,
// so a rebase that keeps tools copies it as the tools tree (docs/sandboxes.md).
const MiseDir = "/opt/mise"

// keptTools are the parts of MiseDir the tools tree carries: the toolchains, the asdf plugins
// that install some of them, and the global config. Shims are made again and caches dropped.
var keptTools = []string{"installs", "plugins", "config"}

const (
	// keptToolsFile is where the import puts the pins the target's config lacks.
	keptToolsFile = "config/conf.d/studio-kept-tools.toml"
	// toolsStaging is where the import extracts the archive before it merges it.
	toolsStaging = ".studio-kept-tools"
)

// miseBinary runs reshim after an import; tests point it elsewhere.
var miseBinary = "/usr/local/bin/mise"

// ExportTools writes the kept parts of dir as a workspace archive. A sandbox without dir (an
// image from before /opt/mise) exports an empty tree.
func ExportTools(dir string, w io.Writer, warn io.Writer, c Compression) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		empty, err := os.MkdirTemp("", "studio-tools")
		if err != nil {
			return err
		}
		defer os.Remove(empty)
		dir = empty
	}
	skip := func(rel string) bool {
		top, _, _ := strings.Cut(rel, "/")
		return rel != "." && !slices.Contains(keptTools, top)
	}
	return exportTree(dir, w, warn, treeOptions{skip: skip}, c)
}

// ImportTools merges a tools archive into dir, the target's MiseDir, which holds the new
// template's toolchains. A toolchain version, alias or plugin dir lacks is moved in; one it has
// stays as the template made it. The archive's tool pins that dir's config doesn't pin go to
// keptToolsFile, so they stay on the PATH while the new template's pins win. Shims are made
// again last. What can't be merged is said on warn rather than failing the rebase.
// It reports progress as ImportWorkspace does.
func ImportTools(dir string, r io.Reader, warn, progress io.Writer) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("the new template has no %s: %w", dir, err)
	}
	staging := filepath.Join(dir, toolsStaging)
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := ImportWorkspace(staging, r, progress); err != nil {
		return err
	}
	moved := 0
	for _, sub := range []struct {
		name  string
		depth int
	}{{"installs", 2}, {"plugins", 1}} {
		n, err := moveMissing(filepath.Join(staging, sub.name), filepath.Join(dir, sub.name), sub.depth)
		if err != nil {
			return fmt.Errorf("keep %s: %w", sub.name, err)
		}
		moved += n
	}
	pins, err := missingPins(filepath.Join(staging, "config"), filepath.Join(dir, "config"))
	if err != nil {
		fmt.Fprintf(warn, "the tools are kept, but not their pins in the global mise config: %v\n", err)
	} else if len(pins) > 0 {
		if err := writePins(filepath.Join(dir, keptToolsFile), pins, st); err != nil {
			return err
		}
	}
	if moved > 0 || len(pins) > 0 {
		reshim(dir, warn)
	}
	return nil
}

// moveMissing moves each entry of src that dst lacks into dst; with depth above 1 it merges
// an entry both have as a directory, one level down. It returns how many entries moved.
func moveMissing(src, dst string, depth int) (int, error) {
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return len(entries), os.Rename(src, dst)
	}
	moved := 0
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		have, err := os.Lstat(to)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Rename(from, to); err != nil {
				return moved, err
			}
			moved++
		case err != nil:
			return moved, err
		case depth > 1 && e.IsDir() && have.IsDir():
			n, err := moveMissing(from, to, depth-1)
			moved += n
			if err != nil {
				return moved, err
			}
		}
	}
	return moved, nil
}

// missingPins returns the tools the global config in from pins and the one in to doesn't, with
// from's pins. A global config is config.toml over conf.d/*.toml, as mise reads it.
func missingPins(from, to string) (map[string]any, error) {
	old, err := toolPins(from)
	if err != nil {
		return nil, err
	}
	cur, err := toolPins(to)
	if err != nil {
		return nil, err
	}
	for name := range cur {
		delete(old, name)
	}
	return old, nil
}

// toolPins reads the [tools] of a global mise config dir: conf.d/*.toml in name order, then
// config.toml, a later file's pin replacing an earlier one's.
func toolPins(dir string) (map[string]any, error) {
	files, err := filepath.Glob(filepath.Join(dir, "conf.d", "*.toml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	files = append(files, filepath.Join(dir, "config.toml"))
	pins := map[string]any{}
	for _, f := range files {
		var cfg struct {
			Tools map[string]any `toml:"tools"`
		}
		if _, err := toml.DecodeFile(f, &cfg); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		maps.Copy(pins, cfg.Tools)
	}
	return pins, nil
}

// writePins writes pins as keptToolsFile, owned like MiseDir (st).
func writePins(path string, pins map[string]any, st fs.FileInfo) error {
	var buf bytes.Buffer
	buf.WriteString("# Tools this sandbox had before it moved to a new template, which the template's\n" +
		"# config.toml doesn't pin. Sandbox Studio wrote this file; edit or delete it as you like.\n")
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"tools": pins}); err != nil {
		return err
	}
	sys := st.Sys().(*syscall.Stat_t)
	for _, d := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if err := os.Mkdir(d, 0o755); err == nil {
			_ = os.Lchown(d, int(sys.Uid), int(sys.Gid))
		} else if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Lchown(path, int(sys.Uid), int(sys.Gid))
}

// reshim makes the shims of the kept toolchains, as the agent user, the way the base image's
// profile points mise at dir.
func reshim(dir string, warn io.Writer) {
	if _, err := os.Stat(miseBinary); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, miseBinary, "reshim")
	cmd.Env = append([]string{
		"HOME=" + Home, "USER=" + User, "LOGNAME=" + User,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"MISE_DATA_DIR=" + dir, "MISE_CONFIG_DIR=" + dir + "/config",
		"MISE_INSTALLS_DIR=" + dir + "/installs", "MISE_SHIMS_DIR=" + dir + "/shims",
	}, toolEnv...)
	cmd.Dir = "/"
	if os.Geteuid() == 0 {
		if acct, err := lookupAccount(User); err == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: acct.uid, Gid: acct.gid}}
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(warn, "the kept tools are installed, but mise reshim failed, so some may be missing from the PATH until `mise reshim`: %v: %s\n", err, bytes.TrimSpace(out))
	}
}
