//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkCommand(t *testing.T) {
	dir := t.TempDir()
	self := "/opt/studio/bin/studio-agent"
	link := filepath.Join(dir, "bin", "studio")
	if err := linkCommand(link, self); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != self {
		t.Fatalf("link %q", got)
	}
	// Again changes nothing.
	if err := linkCommand(link, self); err != nil {
		t.Fatal(err)
	}
	// An older agent's link moves to this one.
	_ = os.Remove(link)
	_ = os.Symlink("/old/bin/studio-agent", link)
	if err := linkCommand(link, self); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != self {
		t.Fatalf("link %q", got)
	}
	// The user's own studio stays: a file, or a link to something else.
	_ = os.Remove(link)
	if err := os.WriteFile(link, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkCommand(link, self); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(link); string(data) != "#!/bin/sh\n" {
		t.Fatalf("the user's file became %q", data)
	}
	_ = os.Remove(link)
	_ = os.Symlink("/home/agent/bin/studio", link)
	if err := linkCommand(link, self); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(link); got != "/home/agent/bin/studio" {
		t.Fatalf("link %q", got)
	}
}
