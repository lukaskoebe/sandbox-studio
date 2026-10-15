package api

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestPrivateHostsCRUD(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	url := testOrigin + "/api/environments/" + env.ID + "/private-hosts"

	if list := decode[[]store.PrivateHost](t, do(h, "GET", url, "")); len(list) != 0 {
		t.Fatalf("new environment: %+v", list)
	}
	rec := do(h, "PUT", url, `{"hosts":[{"host":"GIT.company.local.","ports":[443,22]},{"host":"*.corp.local"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	want := []store.PrivateHost{{Host: "*.corp.local", Ports: []int{}}, {Host: "git.company.local", Ports: []int{22, 443}}}
	if got := decode[[]store.PrivateHost](t, do(h, "GET", url, "")); !reflect.DeepEqual(got, want) {
		t.Fatalf("list: %+v, want %+v", got, want)
	}

	for _, body := range []string{
		`{"hosts":[{"host":"*.com"}]}`,
		`{"hosts":[{"host":"*"}]}`,
		`{"hosts":[{"host":"10.0.0.5"}]}`,
		`{"hosts":[{"host":"a.corp.local"},{"host":"A.corp.local."}]}`,
		`{"hosts":[{"host":"a.corp.local","ports":[70000]}]}`,
	} {
		if rec := do(h, "PUT", url, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", body, rec.Code, rec.Body)
		}
	}
	if got := decode[[]store.PrivateHost](t, do(h, "GET", url, "")); !reflect.DeepEqual(got, want) {
		t.Fatalf("refused lists changed the list: %+v", got)
	}

	if rec := do(h, "GET", testOrigin+"/api/environments/missing/private-hosts", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown environment: %d", rec.Code)
	}
}

// TestApprovalsCannotAddPrivateHosts: an approval allows a host through the policy, but never
// lists it as a private host, whatever the answer.
func TestApprovalsCannotAddPrivateHosts(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID

	go func() {
		_, _ = s.Policy.Decide(ctx, policy.Request{EnvironmentID: env.ID, Host: "git.company.local", Port: 443})
	}()
	pending := awaitApproval(t, h, "git.company.local", 443)
	rec := do(h, "POST", base+"/approvals/"+pending.ID+"/decide", `{"action":"allow","scope":"environment","host":"*.company.local"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("allow: %d %s", rec.Code, rec.Body)
	}
	if rules := decode[[]store.Rule](t, do(h, "GET", base+"/rules", "")); len(rules) != 1 {
		t.Fatalf("the approval should create its rule: %+v", rules)
	}
	if list := decode[[]store.PrivateHost](t, do(h, "GET", base+"/private-hosts", "")); len(list) != 0 {
		t.Fatalf("an approval listed private hosts: %+v", list)
	}
}

// TestEnvironmentFileLeavesPrivateHostsOut: an export doesn't carry the private hosts, and an
// import creates an environment without any, so a file can't list a host. A file that names
// private hosts is refused, since its fields are checked strictly.
func TestEnvironmentFileLeavesPrivateHostsOut(t *testing.T) {
	h, s := newTestServer(t)
	work := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + work.ID
	if rec := do(h, "PUT", base+"/private-hosts", `{"hosts":[{"host":"git.company.local"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", base+"/export", "")
	exported := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(exported, "company.local") || strings.Contains(exported, "privateHosts") {
		t.Fatalf("export: %d %s", rec.Code, exported)
	}

	rec = do(h, "POST", environmentImportURL, exported)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	imported := testOrigin + "/api/environments/" + decode[ImportedEnvironment](t, rec).Environment.ID + "/private-hosts"
	if list := decode[[]store.PrivateHost](t, do(h, "GET", imported, "")); len(list) != 0 {
		t.Fatalf("import listed private hosts: %+v", list)
	}

	named := strings.Replace(exported, "{", `{"privateHosts":[{"host":"git.company.local","ports":[]}],`, 1)
	if rec := do(h, "POST", environmentImportURL, named); rec.Code == http.StatusCreated {
		t.Fatalf("a file naming private hosts was imported: %s", rec.Body)
	}
}
