package login

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// jwt builds an unsigned-looking JWT with the claims, as the tests need them.
func jwt(t *testing.T, claims map[string]any, sig string) string {
	t.Helper()
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + base64.RawURLEncoding.EncodeToString(b) + "." + sig
}

func testLogin(t *testing.T, expires time.Time) codexLogin {
	t.Helper()
	return codexLogin{
		codexToken: codexToken{
			IDToken:      jwt(t, map[string]any{"email": "ada@example.com", "chatgpt_account_id": "acct-1"}, "idsig"),
			AccessToken:  jwt(t, map[string]any{"exp": expires.Unix(), "sub": "user-1"}, "accesssig"),
			RefreshToken: "rt_secretrefresh",
			AccountID:    "acct-1",
		},
		LastRefresh: "2026-10-13T10:00:00Z",
	}
}

func TestParseCodexAuth(t *testing.T) {
	l := testLogin(t, time.Unix(2000000000, 0))
	data, err := json.Marshal(codexAuth{OpenAIAPIKey: nil, Tokens: l.codexToken, LastRefresh: l.LastRefresh})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseCodexAuth(data)
	if err != nil || got != l {
		t.Fatalf("parse: %+v, %v", got, err)
	}
	for name, body := range map[string]string{
		"not JSON":       "{",
		"no tokens":      `{"tokens":{}}`,
		"opaque access":  `{"tokens":{"access_token":"abc","id_token":"` + l.IDToken + `","refresh_token":"r"}}`,
		"no refresh":     `{"tokens":{"access_token":"` + l.AccessToken + `","id_token":"` + l.IDToken + `"}}`,
		"spaced refresh": `{"tokens":{"access_token":"` + l.AccessToken + `","id_token":"` + l.IDToken + `","refresh_token":"a b"}}`,
	} {
		if _, err := parseCodexAuth([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJWTExpiry(t *testing.T) {
	at := time.Unix(1900000000, 0)
	got, err := jwtExpiry(testLogin(t, at).AccessToken)
	if err != nil || !got.Equal(at) {
		t.Fatalf("expiry %v, %v", got, err)
	}
	if _, err := jwtExpiry("a.b"); err == nil {
		t.Error("a two-part token has an expiry")
	}
	if _, err := jwtExpiry(jwt(t, map[string]any{"sub": "x"}, "s")); err == nil {
		t.Error("a token without exp has an expiry")
	}
}

// The guest's copy keeps the claims and expiry, carries no real signature, and holds only
// placeholders where the secrets are.
func TestGuestCodexAuthHoldsNoSecret(t *testing.T) {
	l := testLogin(t, time.Unix(1900000000, 0))
	data, err := codexGuestAuth(l, "studio-accessplaceholder", "studio-otherplaceholder")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"accesssig", "idsig", "rt_secretrefresh"} {
		if strings.Contains(text, secret) {
			t.Fatalf("the guest's auth file holds %q:\n%s", secret, text)
		}
	}
	var got codexAuth
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	accessHead, accessBody, _, _ := jwtParts(l.AccessToken)
	if got.Tokens.AccessToken != accessHead+"."+accessBody+".studio-accessplaceholder" {
		t.Fatalf("access token %q", got.Tokens.AccessToken)
	}
	if got.Tokens.RefreshToken != "studio-otherplaceholder" || got.Tokens.AccountID != "acct-1" || got.LastRefresh != l.LastRefresh {
		t.Fatalf("guest auth %+v", got)
	}
	if exp, err := jwtExpiry(got.Tokens.AccessToken); err != nil || !exp.Equal(time.Unix(1900000000, 0)) {
		t.Fatalf("the guest's access token expiry %v, %v", exp, err)
	}
	if sig, err := accessSignature(l); err != nil || sig != "accesssig" {
		t.Fatalf("the provider's secret %q, %v", sig, err)
	}
}
