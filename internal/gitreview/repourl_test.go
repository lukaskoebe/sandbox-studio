package gitreview

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestParseRepoURL(t *testing.T) {
	for in, want := range map[string]RepoURL{
		"git@github.com:owner/repo.git":                    {Kind: KindSSH, BaseURL: "ssh://git@github.com", Name: "github", Path: "owner/repo", Auth: "agent"},
		"git@github.com:owner/repo":                        {Kind: KindSSH, BaseURL: "ssh://git@github.com", Name: "github", Path: "owner/repo", Auth: "agent"},
		"ssh://git@git.example.org:2222/team/x.git":        {Kind: KindSSH, BaseURL: "ssh://git@git.example.org:2222", Name: "example", Path: "team/x", Auth: "agent"},
		"ssh://gitolite@Box.Example.co.uk/repo":            {Kind: KindSSH, BaseURL: "ssh://gitolite@box.example.co.uk", Name: "example", Path: "repo", Auth: "agent"},
		"https://github.com/owner/repo":                    {Kind: KindGitHub, BaseURL: "https://github.com", Name: "github", Path: "owner/repo"},
		"https://github.com/owner/repo.git":                {Kind: KindGitHub, BaseURL: "https://github.com", Name: "github", Path: "owner/repo"},
		"https://github.com/owner/repo/tree/main":          {Kind: KindGitHub, BaseURL: "https://github.com", Name: "github", Path: "owner/repo"},
		"  https://codeberg.org/o/r.git ":                  {Kind: KindForgejo, BaseURL: "https://codeberg.org", Name: "codeberg", Path: "o/r"},
		"https://gitlab.com/group/sub/project.git":         {Kind: KindGitLab, BaseURL: "https://gitlab.com", Name: "gitlab", Path: "group/sub/project"},
		"https://gitlab.com/group/sub/project/-/tree/main": {Kind: KindGitLab, BaseURL: "https://gitlab.com", Name: "gitlab", Path: "group/sub/project"},
		"https://git.example.org/group/project.git":        {Kind: KindHTTPS, BaseURL: "https://git.example.org", Name: "example", Path: "group/project"},
	} {
		got, err := ParseRepoURL(in)
		if err != nil || got != want {
			t.Errorf("%q = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "github.com", "git@github.com:", "git@github.com:/srv/repo.git", "http://github.com/o/r", "ftp://host/x",
		"https://github.com/onlyowner", "git@10.0.0.1:o/r.git", "https://localhost/o/r", "ssh://git@github.com",
		"https://github.com/o/../r", "git@git.studio.internal:o/r",
	} {
		if got, err := ParseRepoURL(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted as %+v", in, got)
		}
	}
	taken := []store.Forge{{Name: "github"}, {Name: "github-2"}}
	if got := FreeName("github", taken); got != "github-3" {
		t.Errorf("free name = %q", got)
	}
	if got := FreeName("gitlab", taken); got != "gitlab" {
		t.Errorf("free name = %q", got)
	}
}
