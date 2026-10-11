//go:build linux

package guest

import "testing"

func TestBrowserArgvKeepsPersistentProfileAndCertificateVerification(t *testing.T) {
	argv := browserArgv([]string{"open", "https://example.com"})
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
