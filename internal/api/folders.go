package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// maxFolderEntries caps a folder listing; a folder with more subfolders is truncated.
const maxFolderEntries = 1000

// HostFolder is one folder on the computer Studio runs on, with its subfolders, for picking the
// folder of a local forge.
type HostFolder struct {
	Path      string            `json:"path" doc:"The folder's absolute path"`
	Short     string            `json:"short" doc:"The path as ~/... when it is under the home directory, as forms show it"`
	Parent    string            `json:"parent,omitempty" doc:"The folder above, unless that is the file system's root, which is not listed"`
	Home      string            `json:"home" doc:"The home directory, where browsing starts"`
	Git       bool              `json:"git" doc:"Whether the folder is a git repository itself"`
	Entries   []HostFolderEntry `json:"entries" doc:"The subfolders, sorted by name"`
	Truncated bool              `json:"truncated" doc:"There are more subfolders than listed"`
}

// HostFolderEntry is a subfolder of a HostFolder.
type HostFolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path" doc:"Its absolute path"`
	Git  bool   `json:"git" doc:"Whether it is a git repository: a working copy or a bare repository"`
}

func (s *Server) registerFolders(api huma.API) {
	type foldersIn struct {
		Path   string `query:"path" maxLength:"4096" doc:"An absolute path, or ~/path; empty is the home directory"`
		Hidden bool   `query:"hidden" doc:"Also list folders whose names start with a dot"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listHostFolders", Method: http.MethodGet, Path: "/api/system/folders", Tags: []string{"system"},
		Summary:     "The subfolders of a folder on this computer",
		Description: "For picking the folder of a local forge: the browser can't name folders on the host. Only folders are listed, and which are git repositories. The file system's root is refused, as it is as a forge's folder.",
	}, func(ctx context.Context, in *foldersIn) (*struct{ Body HostFolder }, error) {
		out, err := hostFolder(in.Path, in.Hidden)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &struct{ Body HostFolder }{out}, nil
	})
}

// hostFolder lists the subfolders of raw, resolved as a local forge's folder is
// (gitreview.CheckLocalPath).
func hostFolder(raw string, hidden bool) (HostFolder, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return HostFolder{}, errors.New("this computer has no home directory")
	}
	home = filepath.Clean(home)
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(raw, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		raw = home + rest
	}
	if raw == "" {
		raw = home
	}
	if !filepath.IsAbs(raw) {
		return HostFolder{}, errors.New("give the folder's full path, such as ~/git")
	}
	dir := filepath.Clean(raw)
	if root(dir) {
		return HostFolder{}, errors.New("the file system's root is not listed; pick a folder in it")
	}
	list, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return HostFolder{}, fmt.Errorf("%s does not exist on this computer", dir)
	case err != nil:
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return HostFolder{}, fmt.Errorf("cannot read %s: %v", dir, err)
	}
	out := HostFolder{Path: dir, Short: shortPath(dir, home), Home: home, Git: isRepository(dir), Entries: []HostFolderEntry{}}
	if parent := filepath.Dir(dir); !root(parent) {
		out.Parent = parent
	}
	for _, e := range list {
		if !hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// A link to a folder counts as the folder, as it would as a forge's folder.
		if !e.IsDir() && (e.Type()&os.ModeSymlink == 0 || !isDir(p)) {
			continue
		}
		if len(out.Entries) == maxFolderEntries {
			out.Truncated = true
			break
		}
		out.Entries = append(out.Entries, HostFolderEntry{Name: e.Name(), Path: p, Git: isRepository(p)})
	}
	return out, nil
}

// root reports whether dir is the file system's root, or a volume's on Windows.
func root(dir string) bool { return filepath.Dir(dir) == dir }

// shortPath writes dir as ~/... when it is in home.
func shortPath(dir, home string) string {
	if dir == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return dir
}

// isRepository reports whether dir is a git repository the way a local forge finds one: a
// working copy with .git, or a bare repository with HEAD and objects.
func isRepository(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil && isDir(filepath.Join(dir, "objects"))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
