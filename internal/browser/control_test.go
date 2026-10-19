package browser

import (
	"encoding/json"
	"time"

	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func (f *fixture) decide(a store.Approval, action string) {
	f.t.Helper()
	for _, k := range f.svc.ApprovalKinds() {
		if k.Kind == a.Kind {
			if err := k.Decide(f.ctx, a, integrations.Decision{Action: action}); err != nil {
				f.t.Error(err)
			}
			return
		}
	}
	f.t.Fatalf("no kind %s", a.Kind)
}

func (f *fixture) requestControl(params string) string {
	f.t.Helper()
	res, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserRequestControl, params)
	if err != nil {
		f.t.Fatal(err)
	}
	s, _ := res.(string)
	return s
}

func (f *fixture) driving() bool {
	f.t.Helper()
	st, err := f.svc.Status(f.ctx, f.env, f.ada.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Takeover
}

// An agent asks the user, who drives, for the browser back: allowing it hands the browser
// back, denying it keeps the user driving, and a handback by hand settles the request.
func TestRequestControl(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if got := f.requestControl(`{"reason":"check the order"}`); !strings.Contains(got, "not driving") {
		t.Fatalf("nobody drives: %q", got)
	}
	if _, err := f.callDownloadTool(f.sbAda, agentproto.MethodBrowserRequestControl, `{"reason":"  "}`); err == nil {
		t.Error("an empty reason was taken")
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}

	// Denied: the user keeps driving.
	go func() { f.decide(f.pending(KindControl), integrations.Deny) }()
	if got := f.requestControl(`{"reason":"check the order"}`); !strings.Contains(got, "keeps control") || !f.driving() {
		t.Fatalf("denied: %q, driving %v", got, f.driving())
	}

	// Allowed: the browser is the agent's again.
	go func() {
		a := f.pending(KindControl)
		var p ControlPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil || p.Reason != "check the order" || p.TeammateName != f.ada.Name || p.SandboxName != f.sbAda.Name {
			t.Errorf("payload %+v, %v", p, err)
		}
		f.decide(a, integrations.Allow)
	}()
	if got := f.requestControl(`{"reason":"check the order"}`); !strings.Contains(got, "handed the browser back") || f.driving() {
		t.Fatalf("allowed: %q, driving %v", got, f.driving())
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)

	// Unanswered, then handed back with the page's button: the request is settled.
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}
	f.svc.Hold = 100 * time.Millisecond
	if got := f.requestControl(`{"reason":"again"}`); !strings.Contains(got, "not answered yet") {
		t.Fatalf("unanswered: %q", got)
	}
	a := f.pending(KindControl)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if got, err := f.st.Approval(f.ctx, f.env, a.ID); err != nil || got.Status != store.StatusApproved {
		t.Fatalf("request after a handback: %s, %v", got.Status, err)
	}
}

// While the user drives, the pause error tells the agent the user still has control and how
// to get it back.
func TestPausedCallsSayTheUserHasControl(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}
	_, err := f.call(f.sbAda, agentproto.MethodBrowserScreenshot, `{}`)
	if err == nil || !strings.Contains(err.Error(), "still has control") || !strings.Contains(err.Error(), "browser_request_control") ||
		!strings.Contains(err.Error(), "Tell the user") {
		t.Fatalf("paused call: %v", err)
	}
}
