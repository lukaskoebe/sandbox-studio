package agenthook

import "testing"

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:secret@GitHub.com/LukasKoebe/sandbox-studio.git": "github.com/lukaskoebe/sandbox-studio",
		"git@github.com:lukaskoebe/sandbox-studio.git":                 "github.com/lukaskoebe/sandbox-studio",
		"ssh://git@git.example.com:2222/team/repo":                     "git.example.com/team/repo",
		"https://git.studio.internal/sbx/repo/":                        "git.studio.internal/sbx/repo",
		"/srv/repo.git":                                                "",
		"":                                                             "",
	} {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
