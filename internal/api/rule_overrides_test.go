package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// testCA returns a PEM-encoded self-signed certificate, as a private CA would issue.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Company CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestProxyRuleOverrides(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"
	ca := testCA(t)

	body, _ := json.Marshal(map[string]any{
		"host": "api.example.com", "ports": []int{443}, "action": "proxy",
		"config": map[string]any{
			"upstream": "API.Internal.Example.net:8443", "hostHeader": "api.example.com.",
			"sni": "api.internal.example.net", "ca": ca,
		},
	})
	rec := do(h, "POST", rules, string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decode[store.Rule](t, rec)
	want := store.RuleConfig{
		Upstream: "api.internal.example.net:8443", HostHeader: "api.example.com",
		SNI: "api.internal.example.net", CA: ca,
	}
	if !reflect.DeepEqual(created.Config, want) {
		t.Fatalf("config: %+v, want %+v", created.Config, want)
	}
	if stored := decode[[]store.Rule](t, do(h, "GET", rules, "")); len(stored) != 1 || !reflect.DeepEqual(stored[0].Config, want) {
		t.Fatalf("stored: %+v", stored)
	}

	// An upstream can be a host alone, an IP address, or an IPv6 address with a port.
	for _, c := range []struct{ in, want string }{
		{"git.example.net", "git.example.net"},
		{"203.0.113.9:443", "203.0.113.9:443"},
		{"[2001:db8::9]:8443", "[2001:db8::9]:8443"},
	} {
		if got, err := upstreamFrom(c.in); err != nil || got != c.want {
			t.Errorf("upstream %q: %q, %v; want %q", c.in, got, err, c.want)
		}
	}

	refused := []struct{ name, config string }{
		{"a wildcard upstream", `{"upstream":"*.example.net"}`},
		{"an upstream port past 65535", `{"upstream":"api.example.net:70000"}`},
		{"a wildcard Host header", `{"hostHeader":"*.example.com"}`},
		{"a private key as the CA", `{"ca":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}`},
		{"text as the CA", `{"ca":"not a certificate"}`},
		{"a broken certificate", `{"ca":"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"}`},
	}
	for _, c := range refused {
		body := `{"host":"api.example.com","action":"proxy","config":` + c.config + `}`
		if rec := do(h, "POST", rules, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", c.name, rec.Code, rec.Body)
		}
	}
	for _, c := range []struct{ name, body string }{
		{"on an allow rule", `{"host":"api.example.com","action":"allow","config":{"upstream":"other.example.net"}}`},
		{"on a caddy rule", `{"host":"api.example.com","action":"caddy","config":{"caddyfile":"respond 200","sni":"api.example.net"}}`},
	} {
		if rec := do(h, "POST", rules, c.body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("overrides %s: %d %s, want 422", c.name, rec.Code, rec.Body)
		}
	}
}

// TestEnvironmentFileCarriesRuleOverrides: the export keeps where a proxy rule sends its requests,
// and the import checks it like any rule.
func TestEnvironmentFileCarriesRuleOverrides(t *testing.T) {
	h, s := newTestServer(t)
	work := newEnvironment(t, s, "work")
	ca := testCA(t)
	body, _ := json.Marshal(map[string]any{
		"host": "api.example.com", "action": "proxy",
		"config": map[string]any{"upstream": "api.internal.example.net", "sni": "api.internal.example.net", "ca": ca},
	})
	if rec := do(h, "POST", testOrigin+"/api/environments/"+work.ID+"/rules", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("rule: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", testOrigin+"/api/environments/"+work.ID+"/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	file := decode[EnvironmentFile](t, rec)
	if len(file.Rules) != 1 || file.Rules[0].Upstream != "api.internal.example.net" || file.Rules[0].SNI != "api.internal.example.net" || file.Rules[0].CA != ca {
		t.Fatalf("exported rules: %+v", file.Rules)
	}
	rec = do(h, "POST", environmentImportURL, rec.Body.String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	imported := testOrigin + "/api/environments/" + decode[ImportedEnvironment](t, rec).Environment.ID + "/rules"
	if rules := decode[[]store.Rule](t, do(h, "GET", imported, "")); len(rules) != 1 || rules[0].Config.Upstream != "api.internal.example.net" || rules[0].Config.CA != ca {
		t.Fatalf("imported rules: %+v", rules)
	}
}
