package browser

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestPolicyTable(t *testing.T) {
	pat := func(verdict, action, origin, role, label string) store.BrowserPattern {
		return store.BrowserPattern{Verdict: verdict, Action: action, Origin: origin, Role: role, Label: label}
	}
	patterns := []store.BrowserPattern{
		pat(store.BrowserAllow, "click", "https://shop.example.com", "", ""),
		pat(store.BrowserAllow, "*", "https://*.docs.example.org", "", ""),
		pat(store.BrowserAllow, "fill", "app.example.net", "textbox", "search*"),
		pat(store.BrowserDeny, "click", "https://shop.example.com", "button", "*buy now*"),
		pat(store.BrowserDeny, "open", "https://evil.example", "", ""),
		pat(store.BrowserAllow, "upload", "https://shop.example.com", "", ""),
		pat(store.BrowserAllow, "eval", "*", "", ""),
		pat(store.BrowserSensitive, "*", "*", "textbox", "secret*"),
	}
	shop := "https://shop.example.com"
	cases := []struct {
		name string
		r    Request
		want Verdict
	}{
		{"eval never", Request{Action: "eval", Origin: shop}, Deny},
		{"addscript never", Request{Action: "addscript", Origin: shop}, Deny},
		{"setcontent never", Request{Action: "setcontent", Origin: shop}, Deny},
		{"network routes never", Request{Action: "route", Origin: shop}, Deny},
		{"har never", Request{Action: "har", Origin: shop}, Deny},
		{"cookies never", Request{Action: "cookies", Origin: shop}, Deny},
		{"storage never", Request{Action: "storage", Origin: shop}, Deny},
		{"get value of a password field", Request{Action: "get_value", Origin: shop, Role: "textbox", Label: "Password", Sensitive: true}, Deny},
		{"get value of a plain field", Request{Action: "get_value", Origin: shop, Role: "textbox", Label: "Name"}, Allow},
		{"snapshot", Request{Action: "snapshot", Origin: "https://new.example"}, Allow},
		{"scroll", Request{Action: "scroll", Origin: "https://new.example"}, Allow},
		{"get_text", Request{Action: "get_text", Origin: "https://new.example"}, Allow},
		{"click on a new origin", Request{Action: "click", Origin: "https://new.example", Role: "button", Label: "Go"}, Ask},
		{"fill on a new origin", Request{Action: "fill", Origin: "https://new.example", Role: "textbox", Label: "q"}, Ask},
		{"type on a new origin", Request{Action: "type", Origin: "https://new.example", Role: "textbox", Label: "q"}, Ask},
		{"click on an allowed origin", Request{Action: "click", Origin: shop, Role: "link", Label: "Shoes"}, Allow},
		{"allowed for click only", Request{Action: "fill", Origin: shop, Role: "textbox", Label: "Name"}, Ask},
		{"other scheme", Request{Action: "click", Origin: "http://shop.example.com", Role: "link", Label: "Shoes"}, Ask},
		{"deny pattern on label", Request{Action: "click", Origin: shop, Role: "button", Label: "Buy Now!"}, Deny},
		{"wildcard host and action", Request{Action: "fill", Origin: "https://api.docs.example.org", Role: "textbox", Label: "x"}, Allow},
		{"wildcard host covers the apex", Request{Action: "click", Origin: "https://docs.example.org"}, Allow},
		{"wildcard host, other domain", Request{Action: "click", Origin: "https://docs.example.org.evil.example"}, Ask},
		{"origin without scheme, role and label glob", Request{Action: "fill", Origin: "http://app.example.net:8080", Role: "textbox", Label: "Search products"}, Allow},
		{"role must match", Request{Action: "fill", Origin: "https://app.example.net", Role: "combobox", Label: "Search"}, Ask},
		{"upload always asks", Request{Action: "upload", Origin: shop}, Ask},
		{"download always asks", Request{Action: "download", Origin: "https://new.example"}, Ask},
		{"open is the gateway's", Request{Action: "open", Origin: "https://new.example"}, Allow},
		{"open denied by pattern", Request{Action: "open", Origin: "https://evil.example"}, Deny},
		{"unknown action", Request{Action: "frobnicate", Origin: shop}, Deny},
		{"credential fill asks", Request{Action: "fill_credential", Origin: shop}, Ask},
		{"sensitive patterns decide nothing", Request{Action: "click", Origin: "https://x.example", Role: "textbox", Label: "secret answer"}, Ask},
	}
	for _, c := range cases {
		if got, _ := Decide(c.r, patterns); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCheckPattern(t *testing.T) {
	good := []store.BrowserPattern{
		{Verdict: "allow", Action: "click", Origin: "https://example.com"},
		{Verdict: "deny", Action: "*", Origin: "*.example.com"},
		{Verdict: "sensitive", Action: "", Origin: "*", Label: "Security*"},
	}
	for _, p := range good {
		if _, err := CheckPattern(p); err != nil {
			t.Errorf("%+v refused: %v", p, err)
		}
	}
	bad := []store.BrowserPattern{
		{Verdict: "maybe", Action: "click", Origin: "https://example.com"},
		{Verdict: "allow", Action: "", Origin: "https://example.com"},
		{Verdict: "allow", Action: "click", Origin: "javascript://example.com"},
		{Verdict: "allow", Action: "click", Origin: "https://exa mple.com"},
		{Verdict: "sensitive", Action: "*", Origin: "*"},
	}
	for _, p := range bad {
		if _, err := CheckPattern(p); err == nil {
			t.Errorf("%+v allowed", p)
		}
	}
}

// Fixtures in agent-browser 0.39's snapshot format (render_tree in snapshot.rs).
const fixtureInteractive = `- textbox "Email" [ref=e1]: ada@example.com
- textbox "Password" [required, ref=e2]: hunter2hunter2
- textbox "One-time code" [ref=e3]: 492817
- textbox "Card number" [ref=e4]: 4111 1111 1111 1111
- textbox "Name on card" [ref=e5]: Ada Lovelace
- textbox "Security answer" [ref=e6]: fluffy the cat
- textbox "Notes" [ref=e7]: first line
second line
- textbox "Unprobed" [ref=e8]: unknown value
- checkbox "Remember me" [checked=true, ref=e9]
- button "Sign in" [ref=e10]
- link "Help: FAQ" [ref=e11, url=https://example.com/help]
- textbox "New password" [ref=e12]: correct horse
line two of a secret`

const fixtureFull = `- main
  - heading "Sign in" [level=1]
  - form "Login"
    - textbox "Password" [ref=e2]: hunter2hunter2
      - text: hunter2hunter2
    - textbox "Email" [ref=e1]: ada@example.com
      - text: ada@example.com
    - paragraph
      - text: Forgot your password?
  - text: Hello`

func fixtureFields() map[string]Field {
	return map[string]Field{
		"e1":  {Ref: "e1", Role: "textbox", Name: "Email", Type: "email", Autocomplete: "username", Probed: true},
		"e2":  {Ref: "e2", Role: "textbox", Name: "Password", Type: "password", Probed: true},
		"e3":  {Ref: "e3", Role: "textbox", Name: "One-time code", Type: "text", Autocomplete: "one-time-code", Probed: true},
		"e4":  {Ref: "e4", Role: "textbox", Name: "Card number", Type: "text", Autocomplete: "billing cc-number", Probed: true},
		"e5":  {Ref: "e5", Role: "textbox", Name: "Name on card", Type: "text", Autocomplete: "cc-name", Probed: true},
		"e6":  {Ref: "e6", Role: "textbox", Name: "Security answer", Type: "text", Probed: true},
		"e7":  {Ref: "e7", Role: "textbox", Name: "Notes", Probed: true},
		"e8":  {Ref: "e8", Role: "textbox", Name: "Unprobed"},
		"e12": {Ref: "e12", Role: "textbox", Name: "New password", Type: "text", Autocomplete: "new-password", Probed: true},
	}
}

func TestParseLine(t *testing.T) {
	l := ParseLine(`  - link "Help: FAQ" [ref=e11, url=https://example.com/help]`)
	if l.Role != "link" || l.Name != "Help: FAQ" || l.Ref != "e11" || l.Value != "" || l.Indent != 2 {
		t.Errorf("link: %+v", l)
	}
	l = ParseLine(`- textbox "Say \"hi\": now" [required, ref=e2]: a: b`)
	if l.Name != `Say "hi": now` || l.Ref != "e2" || l.Value != "a: b" {
		t.Errorf("textbox: %+v", l)
	}
	l = ParseLine(`- text: Hello`)
	if l.Role != "text" || l.Value != "Hello" || l.Ref != "" {
		t.Errorf("text: %+v", l)
	}
	l = ParseLine(`- checkbox "Remember me" [checked=false, ref=e1]`)
	if l.Ref != "e1" || l.Value != "" {
		t.Errorf("checkbox: %+v", l)
	}
	l = ParseLine(`- generic [ref=e5] clickable [cursor:pointer, onclick]: Open`)
	if l.Ref != "e5" || l.Value != "Open" {
		t.Errorf("cursor: %+v", l)
	}
	if l := ParseLine("second line"); l.Role != "" {
		t.Errorf("continuation parsed as a node: %+v", l)
	}
}

func TestRedactFixtures(t *testing.T) {
	patterns := []store.BrowserPattern{{Verdict: store.BrowserSensitive, Action: "*", Origin: "https://example.com", Label: "security*"}}
	got := Redact(fixtureInteractive, "https://example.com", fixtureFields(), patterns)
	for _, secret := range []string{"hunter2", "492817", "4111", "Ada Lovelace", "fluffy", "unknown value", "correct horse", "line two of a secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q survived redaction:\n%s", secret, got)
		}
	}
	for _, keep := range []string{"ada@example.com", "first line\nsecond line", `- link "Help: FAQ" [ref=e11, url=https://example.com/help]`,
		`- textbox "Password" [required, ref=e2]: ` + RedactedValue, `- checkbox "Remember me" [checked=true, ref=e9]`} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q missing:\n%s", keep, got)
		}
	}
	// The sensitive pattern is per origin.
	if other := Redact(fixtureInteractive, "https://other.example", fixtureFields(), patterns); !strings.Contains(other, "fluffy") {
		t.Error("a sensitive pattern applied on another origin")
	}

	full := Redact(fixtureFull, "https://example.com", fixtureFields(), nil)
	if strings.Contains(full, "hunter2") {
		t.Errorf("a password survived in a child node:\n%s", full)
	}
	for _, keep := range []string{"- text: ada@example.com", "Forgot your password?", "- text: Hello", `- heading "Sign in" [level=1]`} {
		if !strings.Contains(full, keep) {
			t.Errorf("%q missing:\n%s", keep, full)
		}
	}

	var refs []string
	for _, l := range Probes(fixtureFull) {
		refs = append(refs, l.Ref)
	}
	if strings.Join(refs, ",") != "e2,e1" {
		t.Errorf("probes %v", refs)
	}
}

func TestScrub(t *testing.T) {
	if got := Scrub("token abc and abcdef1234!", []string{"abcdef1234", "abc"}); got != "token abc and "+RedactedValue+"!" {
		t.Errorf("Scrub = %q", got)
	}
}

// The browser and persona come from the channel: the payload cannot name another, browser
// VMs and sandboxes without a persona get no browser.
func TestIdentityFromChannel(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	adaVM, err := f.st.BrowserSandbox(f.ctx, f.env, f.ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{"persona":"`+f.bob.ID+`","sandbox":"`+f.sbBob.ID+`"}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("identity in the payload: %v", err)
	}
	f.mustCall(f.sbBob, agentproto.MethodBrowserSnapshot, `{}`)
	bobVM, err := f.st.BrowserSandbox(f.ctx, f.env, f.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.fb.mu.Lock()
	vms := append([]string(nil), f.fb.vms...)
	f.fb.mu.Unlock()
	if vms[0] != adaVM.ID || vms[len(vms)-1] != bobVM.ID || adaVM.ID == bobVM.ID {
		t.Errorf("commands went to %v; ada %s, bob %s", vms, adaVM.ID, bobVM.ID)
	}
	if _, err := f.call(adaVM, agentproto.MethodBrowserSnapshot, `{}`); !errors.Is(err, ErrNoCalls) {
		t.Errorf("a call from the browser VM: %v", err)
	}
	if _, err := f.call(f.sbPlain, agentproto.MethodBrowserSnapshot, `{}`); !errors.Is(err, ErrNoPersona) {
		t.Errorf("a call without persona: %v", err)
	}
	if _, err := f.svc.HandleCall(f.ctx, "nope", agentproto.MethodBrowserSnapshot, nil); err == nil {
		t.Error("a call from an unknown sandbox")
	}

	// The gateway decides the browser's connections with the driving agent's sandbox.
	if d := f.svc.Driver(f.ctx, adaVM); d.ID != f.sbAda.ID {
		t.Errorf("driver %s, want ada-dev", d.Name)
	}
	f.mustCall(f.sbAda2, agentproto.MethodBrowserSnapshot, `{}`)
	if d := f.svc.Driver(f.ctx, adaVM); d.ID != f.sbAda2.ID {
		t.Errorf("driver %s, want ada-two", d.Name)
	}
	if d := f.svc.Driver(f.ctx, f.sbBob); d.ID != f.sbBob.ID {
		t.Error("Driver changed an agent sandbox")
	}
}

func TestPausedGate(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	adaVM, _ := f.st.BrowserSandbox(f.ctx, f.env, f.ada.ID)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, true); err != nil {
		t.Fatal(err)
	}
	// While the user drives, the persona's rules decide the browser's connections.
	if d := f.svc.Driver(f.ctx, adaVM); d.ID != adaVM.ID {
		t.Errorf("driver during takeover: %s", d.Name)
	}
	before := len(f.fb.allCommands())
	start := time.Now()
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`); !errors.Is(err, ErrPaused) {
		t.Fatalf("call during takeover: %v", err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Error("the call did not wait for the user")
	}
	if len(f.fb.allCommands()) != before {
		t.Error("a command ran while the user drove")
	}
	// Bob's browser is not paused.
	f.mustCall(f.sbBob, agentproto.MethodBrowserSnapshot, `{}`)

	// A call waiting when the user hands back goes on.
	done := make(chan error, 1)
	go func() {
		_, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("call after handback: %v", err)
	}
	acts, _ := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, "", 10)
	var kinds []string
	for _, a := range acts {
		if a.Actor == "user" {
			kinds = append(kinds, a.Action)
		}
	}
	if strings.Join(kinds, ",") != "handback,takeover" {
		t.Errorf("user actions %v", kinds)
	}
}

func TestActionApprovalAndRemember(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	kinds := f.svc.ApprovalKinds()
	decide := func(a store.Approval, d integrations.Decision) {
		for _, k := range kinds {
			if k.Kind == a.Kind {
				if err := k.Decide(f.ctx, a, d); err != nil {
					t.Error(err)
				}
			}
		}
	}

	// Denied.
	go func() { decide(f.pending(KindAction), integrations.Decision{Action: integrations.Deny}) }()
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`); err == nil || !strings.Contains(err.Error(), "did not allow") {
		t.Fatalf("denied click: %v", err)
	}
	for _, c := range f.fb.allCommands() {
		if c.Args[0] == "click" {
			t.Fatal("a denied click ran")
		}
	}

	// Allowed once: the next click asks again.
	go func() {
		a := f.pending(KindAction)
		var p ActionPayload
		json.Unmarshal(a.Payload, &p)
		if p.Action != "click" || p.Origin != "https://example.com" || p.Label != "Sign in" || p.SandboxName != "ada-dev" || p.PersonaName != "ada" {
			t.Errorf("payload %+v", p)
		}
		decide(a, integrations.Decision{Action: integrations.Allow})
	}()
	f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`)

	// Not answered within the hold: the agent is told to retry, and the retry after the
	// user allowed goes through without asking again.
	f.svc.Hold = 100 * time.Millisecond
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`); err == nil || !strings.Contains(err.Error(), "call again") {
		t.Fatalf("unanswered: %v", err)
	}
	// Allowed for this origin from now on.
	decide(f.pending(KindAction), integrations.Decision{Action: integrations.Allow, Remember: true})
	f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`)
	f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`)
	pats, _ := f.st.BrowserPatterns(f.ctx, f.env, f.ada.ID)
	if len(pats) != 1 || pats[0].Action != "click" || pats[0].Origin != "https://example.com" || pats[0].Verdict != store.BrowserAllow {
		t.Errorf("patterns %+v", pats)
	}
	if list, _ := f.st.Approvals(f.ctx, f.env, store.StatusPending, 10); len(list) != 0 {
		t.Errorf("pending approvals left: %v", list)
	}
	// Fill on the same origin still asks: the pattern is for click.
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFill, `{"ref":"e1","text":"x"}`); err == nil {
		t.Error("fill went through without approval")
	}
	// Bob's browser knows nothing of ada's pattern.
	f.mustCall(f.sbBob, agentproto.MethodBrowserSnapshot, `{}`)
	if _, err := f.call(f.sbBob, agentproto.MethodBrowserClick, `{"ref":"e4"}`); err == nil {
		t.Error("ada's pattern allowed bob's click")
	}
	// Unknown refs are refused before anything runs.
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e99"}`); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("unknown ref: %v", err)
	}
}

const sentinel = "S3NT1NEL-hunter-7731"

// The credential's value reaches the page and nothing else: not the agent's results, the
// logs, the action log, its screenshots, approvals or snapshots.
func TestCredentialNeverLeaks(t *testing.T) {
	f := newFixture(t)
	sec, err := f.st.CreateSecret(f.ctx, store.Secret{EnvironmentID: f.env, Name: "example-login", Sealed: []byte("sealed"), Hosts: []string{"example.com"}, Placeholder: "studio-cred"})
	if err != nil {
		t.Fatal(err)
	}
	f.keys[sec.ID] = sentinel
	if _, err := f.st.AddBrowserPattern(f.ctx, store.BrowserPattern{EnvironmentID: f.env, PersonaID: f.ada.ID, Action: "*", Origin: "https://example.com", Verdict: store.BrowserAllow}); err != nil {
		t.Fatal(err)
	}
	var results []any
	keep := func(v any, err error) {
		if err != nil {
			results = append(results, err.Error())
			return
		}
		results = append(results, v)
	}
	keep(f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`))

	fill := func(ref string) {
		go func() {
			a := f.pending(KindCredential)
			var p CredentialPayload
			json.Unmarshal(a.Payload, &p)
			if p.Credential != "example-login" || p.Ref != ref || p.Origin != "https://example.com" {
				t.Errorf("credential payload %+v", p)
			}
			for _, k := range f.svc.ApprovalKinds() {
				if k.Kind == KindCredential {
					if err := k.Decide(f.ctx, a, integrations.Decision{Action: integrations.Allow, Remember: true}); err != nil {
						t.Error(err)
					}
				}
			}
		}()
		res, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"`+ref+`","name":"example-login"}`)
		if err != nil {
			t.Fatalf("fill_credential %s: %v", ref, err)
		}
		if m, _ := res.(map[string]string); m["credential"] != "example-login" || m["filled"] != ref {
			t.Errorf("result %v", res)
		}
		results = append(results, res)
	}
	fill("e2") // a password field
	fill("e3") // a text field: the value shows

	keep(f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{"full":true}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserGetText, `{"ref":"e3"}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserScreenshot, `{}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserScroll, `{"direction":"down"}`))
	f.fb.mu.Lock()
	f.fb.failArgs = "click @e3"
	f.fb.values["e1"] = sentinel // the page puts it somewhere else too
	f.fb.mu.Unlock()
	keep(f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e3"}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserFill, `{"ref":"e1","text":"`+sentinel+`x"}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`))
	// On a new page screenshots are back.
	keep(f.call(f.sbAda, agentproto.MethodBrowserOpen, `{"url":"https://example.com/account"}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`))
	keep(f.call(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`))

	out, _ := json.Marshal(results)
	if strings.Contains(string(out), sentinel) {
		t.Errorf("the credential reached the agent: %s", out)
	}
	if !strings.Contains(string(out), "Welcome") {
		t.Errorf("get_text result missing: %s", out)
	}
	if strings.Contains(f.logs.String(), sentinel) {
		t.Errorf("the credential reached the logs: %s", f.logs.String())
	}
	acts, _ := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, "", 1000)
	if len(acts) == 0 {
		t.Fatal("no history")
	}
	hist, _ := json.Marshal(acts)
	if strings.Contains(string(hist), sentinel) {
		t.Errorf("the credential reached the action log: %s", hist)
	}
	if !strings.Contains(string(hist), "credential example-login") {
		t.Errorf("credential fill not logged by name: %s", hist)
	}
	shots := 0
	filepath.WalkDir(f.svc.Dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			shots++
			if b, _ := os.ReadFile(path); strings.Contains(string(b), sentinel) {
				t.Errorf("the credential is visible in screenshot %s", d.Name())
			}
		}
		return nil
	})
	if shots == 0 {
		t.Error("no screenshots at all")
	}
	approvals, _ := f.st.Approvals(f.ctx, f.env, "", 100)
	if b, _ := json.Marshal(approvals); strings.Contains(string(b), sentinel) {
		t.Error("the credential is in an approval")
	}
	for _, a := range approvals {
		if strings.Contains(string(a.Payload), sentinel) {
			t.Error("the credential is in an approval payload")
		}
	}
	// It went to the browser only on a batch's stdin.
	reached := false
	for _, c := range f.fb.allCommands() {
		if strings.Contains(strings.Join(c.Args, " "), sentinel) && !strings.HasPrefix(strings.Join(c.Args, " "), "fill @e1 ") {
			t.Errorf("the credential was a command argument: %v", c.Args[0])
		}
		if c.Args[0] == "batch" && strings.Contains(c.Stdin, sentinel) {
			reached = true
		}
	}
	if !reached {
		t.Error("the credential never reached the page")
	}
}

func TestCredentialRefusals(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.CreateSecret(f.ctx, store.Secret{EnvironmentID: f.env, Name: "other-site", Sealed: []byte("x"), Hosts: []string{"other.example"}, Placeholder: "p1"}); err != nil {
		t.Fatal(err)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"other-site"}`); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Errorf("unbound host: %v", err)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"missing"}`); err == nil {
		t.Error("missing credential")
	}
	f.fb.mu.Lock()
	f.fb.url = "http://other.example/login"
	f.fb.mu.Unlock()
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"other-site"}`); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http: %v", err)
	}
	if list, _ := f.st.Approvals(f.ctx, f.env, "", 10); len(list) != 0 {
		t.Errorf("approvals raised: %v", list)
	}
}

func TestHistoryBound(t *testing.T) {
	f := newFixture(t)
	f.svc.Keep = 5
	f.svc.ShotBudget = 3 * 64 // about three fake screenshots
	if _, err := f.st.AddBrowserPattern(f.ctx, store.BrowserPattern{EnvironmentID: f.env, PersonaID: f.ada.ID, Action: "*", Origin: "https://example.com", Verdict: store.BrowserAllow}); err != nil {
		t.Fatal(err)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	for range 12 {
		f.mustCall(f.sbAda, agentproto.MethodBrowserClick, `{"ref":"e4"}`)
	}
	acts, _ := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, "", 100)
	if len(acts) != 5 {
		t.Errorf("%d actions kept, want 5", len(acts))
	}
	entries, _ := os.ReadDir(f.svc.Dir)
	var total int64
	for _, e := range entries {
		fi, _ := e.Info()
		total += fi.Size()
	}
	if total > f.svc.ShotBudget || len(entries) == 0 {
		t.Errorf("%d screenshots, %d bytes; budget %d", len(entries), total, f.svc.ShotBudget)
	}
	withShot := 0
	for _, a := range acts {
		if a.Screenshot != "" {
			withShot++
			if _, err := f.svc.ShotPath(f.ctx, f.env, f.ada.ID, a.ID); err != nil {
				t.Error(err)
			}
		}
	}
	if withShot != len(entries) {
		t.Errorf("%d log entries with a screenshot, %d files", withShot, len(entries))
	}
	// Orphans go at startup.
	os.WriteFile(filepath.Join(f.svc.Dir, strings.Repeat("a", 32)+".jpg"), []byte("x"), 0o600)
	f.svc.sweepScreenshots(f.ctx)
	if after, _ := os.ReadDir(f.svc.Dir); len(after) != len(entries) {
		t.Errorf("orphan kept: %d files", len(after))
	}
	if err := f.svc.DeleteHistory(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadDir(f.svc.Dir); len(after) != 0 {
		t.Errorf("%d screenshots left after deleting the history", len(after))
	}
}

func TestIdleStop(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	f.svc.Now = func() time.Time { return now }
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	f.mustCall(f.sbBob, agentproto.MethodBrowserSnapshot, `{}`)
	if err := f.svc.SetTakeover(f.ctx, f.env, f.bob.ID, true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	f.svc.stopIdle(f.ctx, 10*time.Minute)
	if f.vms.stops != 1 || !f.vms.stopped[f.ada.ID] {
		t.Errorf("stops %d %v; only ada's idle browser should stop", f.vms.stops, f.vms.stopped)
	}
	// The next call starts it again in a new session.
	st, _ := f.svc.Status(f.ctx, f.env, f.ada.ID)
	if st.VM != "stopped" || st.SessionID != "" {
		t.Errorf("status %+v", st)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if st, _ := f.svc.Status(f.ctx, f.env, f.ada.ID); st.VM != "running" || st.SessionID == "" {
		t.Errorf("status %+v", st)
	}
}

func TestFilterInput(t *testing.T) {
	click := `{"type":"input_mouse","eventType":"mousePressed","x":10,"y":20,"button":"left","clickCount":1,"evil":"x"}`
	if FilterInput([]byte(click), false) != nil {
		t.Error("input passed while the agent drives")
	}
	got := FilterInput([]byte(click), true)
	var m map[string]any
	json.Unmarshal(got, &m)
	if m["eventType"] != "mousePressed" || m["x"] != 10.0 || m["button"] != "left" || m["evil"] != nil {
		t.Errorf("mouse %s", got)
	}
	key := `{"type":"input_keyboard","eventType":"char","key":"a","code":"KeyA","text":"a"}`
	if FilterInput([]byte(key), true) == nil || FilterInput([]byte(key), false) != nil {
		t.Error("keyboard gating")
	}
	for _, msg := range []string{`{"type":"ack","seq":3}`, `{"type":"config","maxFps":10}`} {
		if FilterInput([]byte(msg), false) == nil {
			t.Errorf("%s dropped", msg)
		}
	}
	for _, msg := range []string{`{"type":"input_touch","eventType":"touchStart"}`, `{"type":"eval"}`, `not json`, `{"type":"input_mouse","eventType":"drag"}`} {
		if FilterInput([]byte(msg), true) != nil {
			t.Errorf("%s passed", msg)
		}
	}
}
