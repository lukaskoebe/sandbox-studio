//go:build linux

package guest

import "testing"

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
