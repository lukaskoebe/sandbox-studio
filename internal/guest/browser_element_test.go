//go:build linux

package guest

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestElementAtArgs(t *testing.T) {
	args, err := elementAtArgs(agentproto.BrowserCommand{Args: []string{agentproto.BrowserElementAt, "120.5", "86"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 || args[0] != "eval" || args[1] != "-b" {
		t.Fatalf("args = %q", args)
	}
	script, err := base64.StdEncoding.DecodeString(args[2])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(script), "(() => {\n  const x = 120.5, y = 86;\n") || strings.Contains(string(script), "__X__") {
		t.Fatalf("script starts %q", string(script)[:60])
	}
	// The script is the guest's own: the host's numbers are all that changes.
	if want := strings.NewReplacer("__X__", "120.5", "__Y__", "86").Replace(elementAtScript); string(script) != want {
		t.Fatal("the script is not elementAtScript at the point")
	}

	bad := []agentproto.BrowserCommand{
		{Args: []string{agentproto.BrowserElementAt}},
		{Args: []string{agentproto.BrowserElementAt, "1"}},
		{Args: []string{agentproto.BrowserElementAt, "1", "2", "3"}},
		{Args: []string{agentproto.BrowserElementAt, "1;alert(1)", "2"}},
		{Args: []string{agentproto.BrowserElementAt, "document.cookie", "2"}},
		{Args: []string{agentproto.BrowserElementAt, "1", "NaN"}},
		{Args: []string{agentproto.BrowserElementAt, "Inf", "2"}},
		{Args: []string{agentproto.BrowserElementAt, "-1", "2"}},
		{Args: []string{agentproto.BrowserElementAt, "1", "1e9"}},
		{Args: []string{agentproto.BrowserElementAt, "1", "2"}, Stdin: "x"},
		{Args: []string{agentproto.BrowserElementAt, "1", "2"}, Screenshot: true},
		{Args: []string{agentproto.BrowserElementAt, "1", "2"}, Fresh: true, Ephemeral: 1},
	}
	for _, req := range bad {
		if _, err := elementAtArgs(req); err == nil {
			t.Errorf("%+v allowed", req)
		}
	}
	// The lookup is not a command of the allow-list, so a batch can't carry it, and eval
	// stays refused.
	if err := checkBrowserBatch(`[["element-at","1","2"]]`); err == nil {
		t.Error("element-at allowed in a batch")
	}
	if err := checkBrowserArgs([]string{"eval", "-b", args[2]}, false); err == nil {
		t.Error("eval allowed")
	}
}

// The lookup must not hand back what a form field holds, such as a password filled from the
// vault: the script reads no value, checked state or selection, and no text that would
// include a field's.
func TestElementAtScriptReadsNoFieldValues(t *testing.T) {
	for _, re := range []string{`\.value\b`, `innerText`, `textContent`, `outerHTML`, `innerHTML`, `selectedOptions`, `selectedIndex`, `\.checked\b`, `\.files\b`} {
		if regexp.MustCompile(re).MatchString(elementAtScript) {
			t.Errorf("the script reads %s", re)
		}
	}
	// It writes nothing either: no assignment to the page, its storage or its location.
	for _, re := range []string{`\.(innerHTML|textContent|value|href|src|style)\s*=[^=]`, `setAttribute`, `\.click\(`, `\.focus\(`, `scrollIntoView`, `dispatchEvent`, `localStorage`, `cookie`} {
		if regexp.MustCompile(re).MatchString(elementAtScript) {
			t.Errorf("the script calls or writes %s", re)
		}
	}
	if !strings.Contains(elementAtScript, `skip = new Set(["textarea", "select", "option"`) {
		t.Error("the text walker no longer skips fields")
	}
}
