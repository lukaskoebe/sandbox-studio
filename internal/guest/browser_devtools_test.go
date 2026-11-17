//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestDevToolsPortReadsChromiumsFile(t *testing.T) {
	dir := t.TempDir()
	req := agentproto.BrowserCommand{Args: []string{agentproto.BrowserDevToolsPort}}
	if _, err := devToolsPort(req, dir); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("no file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, devToolsActivePort), []byte("33005\n/devtools/browser/f6c7c29b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := devToolsPort(req, dir)
	if err != nil || string(out) != `{"data":{"port":33005},"error":null,"success":true}` {
		t.Fatalf("got %s, %v", out, err)
	}
	for _, bad := range []string{"", "0\n", "70000\n", "port\n"} {
		if err := os.WriteFile(filepath.Join(dir, devToolsActivePort), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := devToolsPort(req, dir); err == nil {
			t.Errorf("%q: read as a port", bad)
		}
	}
	for _, r := range []agentproto.BrowserCommand{
		{Args: []string{agentproto.BrowserDevToolsPort, "9222"}},
		{Args: []string{agentproto.BrowserDevToolsPort}, Stdin: "x"},
		{Args: []string{agentproto.BrowserDevToolsPort}, Screenshot: true},
	} {
		if _, err := devToolsPort(r, dir); err == nil {
			t.Errorf("%+v: allowed", r)
		}
	}
	// Neither an agent's command nor a batch can name it.
	if err := checkBrowserArgs([]string{agentproto.BrowserDevToolsPort}, false); err == nil {
		t.Error("devtools-port passed the allow-list")
	}
	if err := checkBrowserBatch(`[["devtools-port"]]`); err == nil {
		t.Error("devtools-port allowed in a batch")
	}
}
