package agentcall

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestBrowserTools(t *testing.T) {
	want := []string{agentproto.MethodBrowserOpen, agentproto.MethodBrowserSnapshot, agentproto.MethodBrowserClick,
		agentproto.MethodBrowserFill, agentproto.MethodBrowserType, agentproto.MethodBrowserPress, agentproto.MethodBrowserSelect,
		agentproto.MethodBrowserScroll, agentproto.MethodBrowserGetText, agentproto.MethodBrowserScreenshot,
		agentproto.MethodBrowserWait, agentproto.MethodBrowserBack, agentproto.MethodBrowserFillCredential, agentproto.MethodBrowserClose}
	names := map[string]bool{}
	for _, tl := range BrowserTools {
		if names[tl.Name] {
			t.Errorf("duplicate tool %s", tl.Name)
		}
		names[tl.Name] = true
		// No tool takes an identity: Studio knows the caller from the channel.
		props, _ := tl.InputSchema["properties"].(map[string]any)
		for _, k := range []string{"teammate", "sandbox", "teammate_id", "sandbox_id", "environment"} {
			if _, ok := props[k]; ok {
				t.Errorf("%s takes %s", tl.Name, k)
			}
		}
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing %s", n)
		}
	}
	// The browser is the command's only: MCP points to it.
	for _, tl := range Tools {
		if strings.HasPrefix(tl.Name, "browser_") {
			t.Errorf("MCP lists %s", tl.Name)
		}
	}
	if !strings.Contains(mcpInstructions, "studio browser --help") || !strings.Contains(browserHelp, "fill-credential") {
		t.Error("the instructions do not point to the browser")
	}
	// The browser's text names commands, not tools an agent doesn't have.
	for _, tl := range BrowserTools {
		if b, _ := json.Marshal(tl); strings.Contains(strings.ReplaceAll(string(b), `"name":"`+tl.Name+`"`, ""), "browser_") {
			t.Errorf("%s names a browser tool: %s", tl.Name, b)
		}
	}
}

// Every browser tool but browser_open and browser_close takes an optional session. browser_open
// takes ephemeral, and browser_close needs its session.
func TestBrowserSessionArguments(t *testing.T) {
	for _, tl := range BrowserTools {
		if !strings.HasPrefix(tl.Name, "browser_") {
			continue
		}
		props, _ := tl.InputSchema["properties"].(map[string]any)
		required, _ := tl.InputSchema["required"].([]string)
		_, hasSession := props["session"]
		_, hasEphemeral := props["ephemeral"]
		switch tl.Name {
		case agentproto.MethodBrowserOpen:
			if !hasSession || !hasEphemeral {
				t.Errorf("%s takes no session or ephemeral", tl.Name)
			}
		case agentproto.MethodBrowserClose:
			if !hasSession || !slices.Contains(required, "session") {
				t.Errorf("%s does not need a session", tl.Name)
			}
		case agentproto.MethodBrowserDownloads:
			// The listing covers the persistent profile and every ephemeral session.
			if hasSession {
				t.Errorf("%s takes a session, but it lists them all", tl.Name)
			}
		case agentproto.MethodBrowserShow:
			// The user watches and takes over the persistent profile.
			if hasSession {
				t.Errorf("%s takes a session, but shows the persistent profile", tl.Name)
			}
		default:
			if !hasSession || slices.Contains(required, "session") {
				t.Errorf("%s takes no optional session", tl.Name)
			}
		}
	}
}
