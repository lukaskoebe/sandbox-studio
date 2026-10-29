package browser

import (
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fakeConns map[string][]gateway.Conn // by sandbox ID, newest first

func (c fakeConns) List(sandboxID string) []gateway.Conn { return c[sandboxID] }

// A page whose host's newest connection a deny rule refused names the rule; an allowed newer
// connection, another host, a deleted rule or another VM's log name none.
func TestBlocked(t *testing.T) {
	f := newFixture(t)
	vm, err := f.vms.EnsureBrowser(f.ctx, f.env, f.ada.ID, "browser:test")
	if err != nil {
		t.Fatal(err)
	}
	deny, err := f.st.CreateRule(f.ctx, store.Rule{EnvironmentID: f.env, Host: "*.shop.test", Action: store.ActionDeny})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := f.st.CreateRule(f.ctx, store.Rule{EnvironmentID: f.env, Host: "old.test", Action: store.ActionDeny})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteRule(f.ctx, f.env, gone.ID); err != nil {
		t.Fatal(err)
	}
	conns := fakeConns{vm.ID: {
		{Host: "cdn.shop.test", Port: 443, Verdict: gateway.VerdictDenied, RuleID: deny.ID},
		{Host: "www.shop.test", Port: 443, Verdict: gateway.VerdictDenied, RuleID: deny.ID},
		{Host: "again.shop.test", Port: 443, Verdict: gateway.VerdictAllowed},
		{Host: "again.shop.test", Port: 443, Verdict: gateway.VerdictDenied, RuleID: deny.ID},
		{Host: "old.test", Port: 443, Verdict: gateway.VerdictDenied, RuleID: gone.ID},
		{Host: "asked.test", Port: 443, Verdict: gateway.VerdictDenied},
	}}
	f.svc.Conns = conns
	got := f.svc.blocked(f.ctx, vm, "https://WWW.shop.test/cart?x=1")
	if got == nil || *got != (Blocked{Host: "www.shop.test", RuleID: deny.ID, RuleHost: "*.shop.test"}) {
		t.Errorf("www.shop.test: %+v", got)
	}
	for _, page := range []string{"https://again.shop.test/", "https://old.test/", "https://asked.test/", "https://other.test/", "about:blank", ""} {
		if got := f.svc.blocked(f.ctx, vm, page); got != nil {
			t.Errorf("%q: %+v", page, got)
		}
	}
	if got := f.svc.blocked(f.ctx, store.Sandbox{ID: "other", EnvironmentID: f.env}, "https://www.shop.test/"); got != nil {
		t.Errorf("another VM: %+v", got)
	}
}
