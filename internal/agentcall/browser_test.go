package agentcall

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestBrowserTools(t *testing.T) {
	want := []string{agentproto.MethodBrowserOpen, agentproto.MethodBrowserSnapshot, agentproto.MethodBrowserClick,
		agentproto.MethodBrowserFill, agentproto.MethodBrowserType, agentproto.MethodBrowserPress, agentproto.MethodBrowserSelect,
		agentproto.MethodBrowserScroll, agentproto.MethodBrowserGetText, agentproto.MethodBrowserScreenshot,
		agentproto.MethodBrowserWait, agentproto.MethodBrowserBack, agentproto.MethodBrowserFillCredential}
	names := map[string]bool{}
	for _, tl := range Tools {
		if names[tl.Name] {
			t.Errorf("duplicate tool %s", tl.Name)
		}
		names[tl.Name] = true
		// No tool takes an identity: Studio knows the caller from the channel.
		props, _ := tl.InputSchema["properties"].(map[string]any)
		for _, k := range []string{"persona", "sandbox", "persona_id", "sandbox_id", "environment"} {
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
	if !strings.Contains(mcpInstructions, "browser_fill_credential") {
		t.Error("instructions do not mention the browser")
	}
}

func TestToolImageResult(t *testing.T) {
	fc := &fakeCaller{reply: func(method string, params json.RawMessage) (any, error) {
		return map[string]any{"image": map[string]any{"mimeType": "image/jpeg", "data": "AAAA"}, "text": "the page at https://example.com"}, nil
	}}
	r := callTool(context.Background(), fc, rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{"name":"browser_screenshot","arguments":{}}`)})
	b, _ := json.Marshal(r.Result)
	if !strings.Contains(string(b), `{"data":"AAAA","mimeType":"image/jpeg","type":"image"}`) || !strings.Contains(string(b), "https://example.com") {
		t.Errorf("result %s", b)
	}
	if fc.calls[0].Method != agentproto.MethodBrowserScreenshot || string(fc.calls[0].Params) != "{}" {
		t.Errorf("call %+v", fc.calls)
	}
}
