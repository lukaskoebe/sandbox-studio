package browser

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// browser_show opens the page under the browser's rules and posts a card without waiting for
// it. A newer card replaces the older one, Watch (a dismissal) only clears it, and allowing it
// hands the user the wheel.
func TestShowPostsACardForThePage(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.AddBrowserPattern(f.ctx, store.BrowserPattern{EnvironmentID: f.env, TeammateID: f.ada.ID, Action: "*", Origin: "https://example.com", Verdict: store.BrowserAllow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserShow, `{"url":"ftp://example.com/"}`); err == nil {
		t.Error("a URL that is not http was shown")
	}
	res := f.mustCall(f.sbAda, agentproto.MethodBrowserShow, `{"url":"https://example.com/login","title":"Login page","note":"Try a wrong password"}`)
	if text, _ := res.(string); !strings.Contains(text, "Showing https://example.com/login") || !strings.Contains(text, "without waiting") {
		t.Fatalf("show = %v", res)
	}
	first := f.pending(KindShow)
	var p ShowPayload
	if err := json.Unmarshal(first.Payload, &p); err != nil || p.URL != "https://example.com/login" || p.Title != "Login page" ||
		p.Note != "Try a wrong password" || p.TeammateName != f.ada.Name || p.SandboxName != f.sbAda.Name {
		t.Fatalf("payload %+v, %v", p, err)
	}
	if !strings.Contains(first.Subject, "Login page") {
		t.Errorf("subject %q", first.Subject)
	}

	f.mustCall(f.sbAda, agentproto.MethodBrowserShow, `{"url":"https://example.com/signup"}`)
	second := f.pending(KindShow)
	if second.ID == first.ID || !strings.Contains(second.Subject, "https://example.com/signup") {
		t.Fatalf("second card %+v", second)
	}
	list, err := f.st.Approvals(f.ctx, f.env, store.StatusPending, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.ID == first.ID {
			t.Error("the older card is still pending")
		}
	}

	f.decide(second, integrations.Dismiss)
	if f.driving() {
		t.Error("watching took the wheel")
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserShow, `{"url":"https://example.com/"}`)
	f.decide(f.pending(KindShow), integrations.Allow)
	if !f.driving() {
		t.Error("taking the wheel left the agent driving")
	}
}
