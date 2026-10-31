package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type values map[string]string

func (v values) Value(_ context.Context, _, id string) (string, error) { return v[id], nil }

type harness struct {
	ctx    context.Context
	st     *store.Store
	env    store.Environment
	sb     store.Sandbox
	svc    *Service
	values values
	sts    *httptest.Server
	calls  atomic.Int32
	form   atomic.Value // the last request's url.Values
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1_800_000_000, 0)
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	sb.StartedAt = &started
	h := &harness{ctx: ctx, st: st, env: env, sb: sb, values: values{}}
	h.sts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.form.Store(r.PostForm)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLEKEY000/") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<ErrorResponse><Error><Code>InvalidClientTokenId</Code><Message>bad key</Message></Error></ErrorResponse>`)
			return
		}
		fmt.Fprintf(w, `<%[1]sResponse><%[1]sResult><Credentials><AccessKeyId>ASIATEMP</AccessKeyId><SecretAccessKey>temp-secret</SecretAccessKey>
<SessionToken>temp-token</SessionToken><Expiration>%[2]s</Expiration></Credentials></%[1]sResult></%[1]sResponse>`,
			r.PostForm.Get("Action"), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}))
	t.Cleanup(h.sts.Close)
	old := stsEndpoint
	stsEndpoint = func(string) string { return h.sts.URL + "/" }
	t.Cleanup(func() { stsEndpoint = old })
	h.svc = &Service{Store: st, Secrets: h.values, Client: h.sts.Client(), Hold: 2 * time.Second}
	return h
}

func (h *harness) secret(t *testing.T, sec store.Secret, value string) store.Secret {
	t.Helper()
	sec.ID, sec.EnvironmentID, sec.Placeholder, sec.Sealed = store.NewID(), h.env.ID, "studio-"+sec.Name, []byte("sealed")
	if sec.Kind == "" {
		sec.Kind = "aws"
	}
	if sec.Fields == nil {
		sec.Fields = map[string]string{"accessKeyId": "AKIAEXAMPLEKEY000"}
	}
	sec.Hosts = []string{"*.amazonaws.com"}
	out, err := h.st.CreateSecret(h.ctx, sec)
	if err != nil {
		t.Fatal(err)
	}
	h.values[out.ID] = value
	return out
}

func (h *harness) call(secret, format string) (string, error) {
	raw, _ := json.Marshal(map[string]string{"secret": secret, "format": format})
	out, err := h.svc.call(h.ctx, h.sb, raw)
	if err != nil {
		return "", err
	}
	return out.(agentcall.CredentialAnswer).Output, nil
}

// pending returns the one pending credential.issue card.
func (h *harness) pending(t *testing.T) store.Approval {
	t.Helper()
	for range 200 {
		list, err := h.st.Approvals(h.ctx, h.env.ID, store.StatusPending, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) == 1 && list[0].Kind == Kind {
			return list[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no credential.issue card")
	return store.Approval{}
}

// The helper's call waits for the user; allowed, it gets session credentials in the format
// AWS's credential_process reads, from the cache until they're close to expiring.
func TestIssueAfterApproval(t *testing.T) {
	h := newHarness(t)
	h.secret(t, store.Secret{Name: "AWS_PROD"}, "long-lived-secret")

	type result struct {
		out string
		err error
	}
	got := make(chan result, 1)
	go func() {
		out, err := h.call("AWS_PROD", "process")
		got <- result{out, err}
	}()
	a := h.pending(t)
	var p IssuePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "AWS_PROD" || p.Kind != "aws" || p.KindTitle != "AWS access key" || p.SandboxID != h.sb.ID || p.Lifetime != "1h0m0s" {
		t.Errorf("payload %+v", p)
	}
	if err := h.svc.decide(h.ctx, a, integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.err != nil {
		t.Fatal(r.err)
	}
	var answer map[string]any
	if err := json.Unmarshal([]byte(r.out), &answer); err != nil {
		t.Fatalf("%v: %q", err, r.out)
	}
	if answer["Version"] != float64(1) || answer["AccessKeyId"] != "ASIATEMP" || answer["SecretAccessKey"] != "temp-secret" ||
		answer["SessionToken"] != "temp-token" || answer["Expiration"] == "" {
		t.Errorf("answer %v", answer)
	}
	if strings.Contains(r.out, "long-lived-secret") {
		t.Error("the answer holds the secret itself")
	}
	form := h.form.Load().(url.Values)
	if form.Get("Action") != "GetSessionToken" || form.Get("DurationSeconds") != "3600" {
		t.Errorf("STS was asked %v", form)
	}

	// Cached, and allowed for the run: no card, no second STS call.
	if _, err := h.call("AWS_PROD", "process"); err != nil {
		t.Fatal(err)
	}
	if n := h.calls.Load(); n != 1 {
		t.Errorf("STS was called %d times", n)
	}
	// The next run asks again.
	later := h.sb.StartedAt.Add(time.Hour)
	h.sb.StartedAt = &later
	h.svc.cache = nil
	go func() {
		_, err := h.call("AWS_PROD", "process")
		got <- result{err: err}
	}()
	a = h.pending(t)
	if err := h.svc.decide(h.ctx, a, integrations.Decision{Action: integrations.Deny}); err != nil {
		t.Fatal(err)
	}
	if r := <-got; r.err == nil || !integrations.IsToolError(r.err) || !strings.Contains(r.err.Error(), "didn't let") {
		t.Errorf("denied: %v", r.err)
	}
}

// "Always" holds for every run, and a role makes it AssumeRole's credentials.
func TestIssueAlwaysWithRole(t *testing.T) {
	h := newHarness(t)
	h.secret(t, store.Secret{Name: "AWS_ROLE", Fields: map[string]string{"accessKeyId": "AKIAEXAMPLEKEY000", "roleArn": "arn:aws:iam::123456789012:role/dev"}}, "s")
	go func() {
		a := h.pending(t)
		if err := h.svc.decide(h.ctx, a, integrations.Decision{Action: integrations.Allow, Remember: true}); err != nil {
			t.Error(err)
		}
	}()
	if _, err := h.call("AWS_ROLE", "process"); err != nil {
		t.Fatal(err)
	}
	form := h.form.Load().(url.Values)
	if form.Get("Action") != "AssumeRole" || form.Get("RoleArn") != "arn:aws:iam::123456789012:role/dev" || form.Get("RoleSessionName") != "studio-api" {
		t.Errorf("STS was asked %v", form)
	}
	later := h.sb.StartedAt.Add(time.Hour)
	h.sb.StartedAt = &later
	h.svc.cache = nil
	if _, err := h.call("AWS_ROLE", "process"); err != nil {
		t.Fatalf("always didn't hold for the next run: %v", err)
	}
}

// Unknown secrets and formats, a user who doesn't answer, and STS refusing the key are told to
// the helper, which prints it for the tool.
func TestIssueRefusals(t *testing.T) {
	h := newHarness(t)
	h.secret(t, store.Secret{Name: "AWS_BAD", Fields: map[string]string{"accessKeyId": "AKIAWRONGKEY0000"}}, "s")
	h.svc.Hold = 50 * time.Millisecond
	for _, c := range []struct{ secret, format, want string }{
		{"NOPE", "process", "sees no secret NOPE"},
		{"AWS_BAD", "yaml", `no answer of the format "yaml"`},
		{"AWS_BAD", "process", "waiting for the user"},
	} {
		if _, err := h.call(c.secret, c.format); err == nil || !integrations.IsToolError(err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v", c.secret, c.format, err)
		}
	}
	// Approved after the call gave up, the next call goes on.
	if err := h.svc.decide(h.ctx, h.pending(t), integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.call("AWS_BAD", "process"); err == nil || !strings.Contains(err.Error(), "InvalidClientTokenId: bad key") {
		t.Errorf("STS's refusal: %v", err)
	}
}

// A sandbox gets a profile of each AWS secret it sees in one block of ~/.aws/config, and
// AWS_PROFILE when it sees one; with none, the block is empty, which removes it.
func TestConfig(t *testing.T) {
	h := newHarness(t)
	files, env, err := h.svc.Config(h.ctx, h.sb)
	if err != nil {
		t.Fatal(err)
	}
	empty := "# " + agentproto.BlockBegin + "\n# " + agentproto.BlockEnd + "\n"
	if len(files) != 1 || files[0].Path != ".aws/config" || files[0].Content != empty || files[0].Comment != "# " || !files[0].Block || len(env) != 0 {
		t.Fatalf("no secrets: %+v %v", files, env)
	}
	h.secret(t, store.Secret{Name: "AWS_PROD"}, "s")
	_, env, _ = h.svc.Config(h.ctx, h.sb)
	if env["AWS_PROFILE"] != "aws_prod" {
		t.Errorf("env %v", env)
	}
	h.secret(t, store.Secret{Name: "AWS_DEV", SandboxID: h.sb.ID}, "s")
	other, _ := h.st.CreateSandbox(h.ctx, store.Sandbox{EnvironmentID: h.env.ID, Name: "other"})
	h.secret(t, store.Secret{Name: "AWS_ELSE", SandboxID: other.ID}, "s")
	files, env, _ = h.svc.Config(h.ctx, h.sb)
	want := "# " + agentproto.BlockBegin + "\n" +
		"[profile aws_dev]\ncredential_process = /opt/studio/bin/studio-agent credential AWS_DEV process\n\n" +
		"[profile aws_prod]\ncredential_process = /opt/studio/bin/studio-agent credential AWS_PROD process\n" +
		"# " + agentproto.BlockEnd + "\n"
	if files[0].Content != want || len(env) != 0 {
		t.Errorf("two secrets: %q %v", files[0].Content, env)
	}
}

func TestRecipeChecks(t *testing.T) {
	r := Recipes["aws"]
	if _, err := r.CheckFields(map[string]string{"accessKeyId": "AKIAEXAMPLEKEY000", "roleArn": " "}); err != nil {
		t.Errorf("an empty optional field: %v", err)
	}
	for _, bad := range []map[string]string{
		{},
		{"accessKeyId": "not a key"},
		{"accessKeyId": "AKIAEXAMPLEKEY000", "roleArn": "arn:aws:s3:::bucket"},
		{"accessKeyId": "AKIAEXAMPLEKEY000", "secretAccessKey": "x"},
	} {
		if _, err := r.CheckFields(bad); err == nil {
			t.Errorf("took %v", bad)
		}
	}
	recipe := func(body string) fstest.MapFS {
		return fstest.MapFS{"recipes/x.yaml": {Data: []byte("name: x\ntitle: X\nissuer: aws-sts\nlifetime: 1h\n" + body)}}
	}
	secret := "fields: [{name: key, title: Key, secret: true}]\n"
	for name, body := range map[string]string{
		"no secret field":       "fields: [{name: id, title: ID}]\n",
		"unknown issuer":        secret + "issuer: magic\n",
		"secret in a file":      secret + "files: [{path: .x, content: '{{field.key}}'}]\n",
		"credential in env":     secret + "env: {X: '{{cred.token}}'}\n",
		"path out of the home":  secret + "files: [{path: ../x, content: x}]\n",
		"broken template":       secret + "env: {X: '{{secret'}\n",
		"unknown key":           secret + "script: rm -rf /\n",
		"lifetime too long":     strings.Replace(secret, "fields", "lifetime: 48h\nfields", 1),
		"a marker in a content": secret + "files: [{path: .x, content: '" + agentproto.BlockEnd + "'}]\n",
	} {
		if _, err := load(recipe(body)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}
