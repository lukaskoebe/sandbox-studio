package gitreview

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// shortNameForm is the form shortName gives a forge: lower-case letters and digits, hyphens
// only inside, at most 24 characters.
var shortNameForm = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,22}[a-z0-9])?$`)

// FuzzParseRepoURL feeds ParseRepoURL pasted text. Whatever it accepts is a forge whose
// path and name have the shapes the rest of the package relies on.
func FuzzParseRepoURL(f *testing.F) {
	for _, raw := range []string{
		"git@github.com:owner/repo.git",
		"git@git.studio.internal:o/r",
		"ssh://git@git.example.com:2222/team/repo",
		"https://github.com/owner/repo",
		"https://github.com/owner/repo/tree/main/internal/x",
		"https://codeberg.org/owner/repo.git",
		"https://user:token@git.example.co.uk/group/sub/project.git",
		"https://gitlab.com/group/sub/project.git",
		"https://[::1]/a/b",
		"https://10.0.0.1/a/b",
		"https://localhost/a/b",
		"https://github.com/o/../r",
		"git@host:/absolute/path",
		"ftp://example.com/a/b",
		"git@github.com:a/b/c/d/e/f/g/h/i.git",
		"",
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		r, err := ParseRepoURL(raw)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v does not mark the URL as invalid", err)
			}
			return
		}
		switch r.Kind {
		case KindForgejo, KindGitHub, KindGitLab, KindHTTPS, KindSSH:
		default:
			t.Fatalf("kind %q", r.Kind)
		}
		if r.BaseURL == "" {
			t.Error("no base URL")
		}
		if r.Auth != "" && (r.Kind != KindSSH || r.Auth != store.ForgeAuthAgent) {
			t.Errorf("auth %q for kind %s", r.Auth, r.Kind)
		}
		segs := strings.Split(r.Path, "/")
		for _, seg := range segs {
			if !repoName.MatchString(seg) || strings.Contains(seg, "..") {
				t.Errorf("path %q has segment %q", r.Path, seg)
			}
		}
		// Forgejo and GitHub take owner/repo; GitLab's owners may be subgroups.
		if len(segs) > MaxPathSegments || (r.Kind == KindForgejo || r.Kind == KindGitHub) && len(segs) != 2 {
			t.Errorf("path %q for kind %s", r.Path, r.Kind)
		}
		if !shortNameForm.MatchString(r.Name) {
			t.Errorf("name %q", r.Name)
		}
	})
}
