//go:build linux

package guest

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestBrowserArgvKeepsPersistentProfileAndCertificateVerification(t *testing.T) {
	argv := browserArgv(browserSlotFor(0), []string{"open", "https://example.com"})
	profile := -1
	for i, arg := range argv {
		if arg == "--profile" {
			profile = i
			break
		}
	}
	if profile < 0 || profile+1 >= len(argv) || argv[profile+1] != browserProfile {
		t.Fatalf("browser argv does not keep profile %q: %q", browserProfile, argv)
	}
	for _, forbidden := range []string{
		"--ca-cert", "--ignore-https-errors", "--ignore-certificate-errors",
		"--ignore-certificate-errors-spki-list",
	} {
		for _, arg := range argv {
			if arg == forbidden {
				t.Errorf("browser argv contains certificate bypass %q: %q", forbidden, argv)
			}
		}
	}
}

func TestCheckBrowserArgs(t *testing.T) {
	ok := [][]string{
		{"open", "https://example.com"},
		{"snapshot", "-i"},
		{"click", "@e3"},
		{"fill", "@e3", "hello"},
		{"get", "text", "@e1"},
		{"get", "attr", "@e2", "type"},
		{"wait", "--text", "-Done-"},
		{"screenshot", "/tmp/x.jpg", "--screenshot-format", "jpeg", "--screenshot-quality", "60"},
		{"batch"},
	}
	for _, args := range ok {
		if err := checkBrowserArgs(args, false); err != nil {
			t.Errorf("%q refused: %v", args, err)
		}
	}
	bad := [][]string{
		{},
		{"eval", "document.cookie"},
		{"addscript", "x"},
		{"addinitscript", "x"},
		{"setcontent", "<p>"},
		{"network", "route", "*"},
		{"network", "har", "start"},
		{"cookies"},
		{"storage", "local"},
		{"state", "save", "x"},
		{"get", "value", "@e1"},
		{"get", "cdp-url"},
		{"get", "html", "@e1"},
		{"wait", "--fn", "window.x"},
		{"open", "https://x", "--cdp", "9222"},
		{"click", "@e1", "--args", "--remote-debugging-port=9222"},
		{"batch", "x"},
	}
	for _, args := range bad {
		if err := checkBrowserArgs(args, false); err == nil {
			t.Errorf("%q allowed", args)
		}
	}
	if err := checkBrowserArgs([]string{"batch"}, true); err == nil {
		t.Error("nested batch allowed")
	}
	if err := checkBrowserBatch(`[["fill","@e3","s3cret"],["get","attr","@e3","type"]]`); err != nil {
		t.Errorf("batch refused: %v", err)
	}
	for _, in := range []string{`[["eval","1"]]`, `[]`, `{}`, `[["batch"]]`} {
		if err := checkBrowserBatch(in); err == nil {
			t.Errorf("batch %s allowed", in)
		}
	}
}

func TestBrowserEphemeralSlots(t *testing.T) {
	profile := browserSlotFor(0)
	if profile.ephemeral || profile.session != browserSession || profile.port != 9223 {
		t.Fatalf("profile slot %+v", profile)
	}
	seen := map[string]bool{profile.session: true}
	ports := map[int]bool{profile.port: true}
	for n := 1; n <= agentproto.MaxBrowserEphemeral; n++ {
		slot := browserSlotFor(n)
		if !slot.ephemeral || seen[slot.session] || ports[slot.port] {
			t.Fatalf("slot %d shares a session or port: %+v", n, slot)
		}
		seen[slot.session], ports[slot.port] = true, true
		if slot.port != agentproto.BrowserStreamPort+n {
			t.Errorf("slot %d streams on %d", n, slot.port)
		}
		// Ephemeral state lives on the guest's tmpfs, never on the persistent disk.
		for _, dir := range []string{slot.profile, slot.downloads, slot.dir()} {
			if !strings.HasPrefix(dir, browserEphemeralRoot+"/") || strings.HasPrefix(dir, Workdir) {
				t.Errorf("slot %d keeps %s outside the tmpfs", n, dir)
			}
		}
		if filepath.Dir(slot.profile) != slot.dir() || filepath.Dir(slot.downloads) != slot.dir() {
			t.Errorf("slot %d: profile and downloads are not deleted with %s", n, slot.dir())
		}
		argv := strings.Join(browserArgv(slot, []string{"open", "https://example.com"}), " ")
		want := "--session " + slot.session + " --profile " + slot.profile + " "
		if !strings.Contains(argv, want) || !strings.Contains(argv, "--download-path "+slot.downloads) {
			t.Errorf("slot %d argv %q", n, argv)
		}
		env := strings.Join(browserEnv(slot), " ")
		if !strings.Contains(env, "AGENT_BROWSER_STREAM_PORT="+strconv.Itoa(slot.port)) {
			t.Errorf("slot %d env %q", n, env)
		}
	}
}

func TestCheckBrowserSession(t *testing.T) {
	for _, req := range []agentproto.BrowserCommand{
		{Args: []string{"open", "https://x"}, Ephemeral: -1},
		{Args: []string{"open", "https://x"}, Ephemeral: agentproto.MaxBrowserEphemeral + 1},
		{Args: []string{"open", "https://x"}, Fresh: true}, // the profile is never wiped
	} {
		if _, _, err := checkBrowserSession(req); err == nil {
			t.Errorf("%+v allowed", req)
		}
	}
	if _, closing, _ := checkBrowserSession(agentproto.BrowserCommand{Args: []string{"close"}}); closing {
		t.Error("the profile session can be closed")
	}
	if err := checkBrowserArgs([]string{"close"}, false); err == nil {
		t.Error("close passes the command allow-list")
	}
	if err := checkBrowserBatch(`[["close"]]`); err == nil {
		t.Error("close passes in a batch")
	}
	slot, closing, err := checkBrowserSession(agentproto.BrowserCommand{Args: []string{"close"}, Ephemeral: 2})
	if err != nil || !closing || slot != browserSlotFor(2) {
		t.Errorf("closing slot 2: %+v %v %v", slot, closing, err)
	}
	if _, closing, _ := checkBrowserSession(agentproto.BrowserCommand{Args: []string{"close", "--all"}, Ephemeral: 1}); closing {
		t.Error("close --all is a close")
	}
}
