package gitreview

import (
	"errors"
	"testing"
)

func TestPublishedHostKeysParse(t *testing.T) {
	for _, p := range publishedHostKeys {
		for _, k := range p.keys {
			if _, err := CheckHostKey(k); err != nil {
				t.Errorf("%s: %v", p.name, err)
			}
		}
	}
}

// A key a forge publishes is recognized on each of its addresses; another key for that
// host is refused, and a host Studio has no keys for is left to the user.
func TestCheckPublished(t *testing.T) {
	gh, err := CheckHostKey(publishedHostKeys[0].keys[0])
	if err != nil {
		t.Fatal(err)
	}
	other, err := CheckHostKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []ForgeURL{{Host: "github.com", Port: 22}, {Host: "GitHub.com", Port: 22}, {Host: "ssh.github.com", Port: 443}} {
		got, err := CheckPublished(u, gh)
		if err != nil || got.PublishedBy != "GitHub" {
			t.Errorf("%s:%d: got %q, %v", u.Host, u.Port, got.PublishedBy, err)
		}
	}
	if _, err := CheckPublished(ForgeURL{Host: "github.com", Port: 22}, other); !errors.Is(err, ErrInvalid) {
		t.Errorf("Bitbucket's key for github.com: %v", err)
	}
	for _, u := range []ForgeURL{{Host: "git.example.com", Port: 22}, {Host: "github.com", Port: 2222}} {
		got, err := CheckPublished(u, other)
		if err != nil || got.PublishedBy != "" {
			t.Errorf("%s:%d: got %q, %v", u.Host, u.Port, got.PublishedBy, err)
		}
	}
}
