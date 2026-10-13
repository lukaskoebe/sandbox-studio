package login

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// codexAuth is the file `codex login` writes to ~/.codex/auth.json for a ChatGPT login.
// verify (S8): the field names and shapes of the pinned Codex version.
type codexAuth struct {
	OpenAIAPIKey *string    `json:"OPENAI_API_KEY"`
	Tokens       codexToken `json:"tokens"`
	LastRefresh  string     `json:"last_refresh,omitempty"`
}

// codexToken holds the tokens of a ChatGPT login. The access token is a JWT, and so is the id
// token; the refresh token is opaque.
type codexToken struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

// codexLogin is what Studio keeps of a ChatGPT login in the vault, as studio-only JSON. It holds
// the real tokens, so it never reaches a sandbox.
type codexLogin struct {
	codexToken
	LastRefresh string `json:"last_refresh,omitempty"`
}

// parseCodexAuth reads the auth.json a `codex login` wrote. It refuses a file without the two
// JWTs and a refresh token, since the rest of the login depends on them.
func parseCodexAuth(data []byte) (codexLogin, error) {
	var a codexAuth
	if err := json.Unmarshal(data, &a); err != nil {
		return codexLogin{}, fmt.Errorf("the Codex login file is not JSON: %w", err)
	}
	l := codexLogin{codexToken: a.Tokens, LastRefresh: a.LastRefresh}
	if err := checkCodexLogin(l); err != nil {
		return codexLogin{}, err
	}
	return l, nil
}

// checkCodexLogin refuses tokens Studio can't keep: JWTs that don't have three parts, and an
// empty refresh token.
func checkCodexLogin(l codexLogin) error {
	for name, tok := range map[string]string{"access token": l.AccessToken, "id token": l.IDToken} {
		if _, _, _, err := jwtParts(tok); err != nil {
			return fmt.Errorf("the Codex login's %s: %w", name, err)
		}
	}
	if l.RefreshToken == "" || strings.ContainsAny(l.RefreshToken, " \t\r\n") {
		return errors.New("the Codex login has no refresh token")
	}
	return nil
}

// jwtParts splits a JWT into its header, payload and signature.
func jwtParts(token string) (header, payload, signature string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", errors.New("not a JWT")
	}
	return parts[0], parts[1], parts[2], nil
}

// jwtExpiry returns the exp claim of a JWT. The signature is not checked: Studio only uses the
// claim to know when to refresh, and the API checks the token itself.
func jwtExpiry(token string) (time.Time, error) {
	_, payload, _, err := jwtParts(token)
	if err != nil {
		return time.Time{}, err
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return time.Time{}, fmt.Errorf("the JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(b, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, errors.New("the JWT has no expiry")
	}
	return time.Unix(claims.Exp, 0), nil
}

// codexGuestAuth is the auth.json a teammate's Codex reads. The tokens keep their headers and
// payloads, so Codex sees the same claims and expiry, but their signatures are the placeholders:
// the access token's signature is the provider's secret, which the gateway swaps in on the
// ChatGPT host, so the guest sends the real token there. The refresh token and the id token's
// signature are placeholders nothing swaps: Studio refreshes the login itself (refresh.go), and a
// refresh the guest attempts fails rather than handing new tokens to the sandbox.
// verify (S8): that Codex reads only the claims of the id token and the access token it has here.
func codexGuestAuth(l codexLogin, accessPlaceholder, studioPlaceholder string) ([]byte, error) {
	if err := checkCodexLogin(l); err != nil {
		return nil, err
	}
	accessHead, accessBody, _, _ := jwtParts(l.AccessToken)
	idHead, idBody, _, _ := jwtParts(l.IDToken)
	return json.MarshalIndent(codexAuth{
		Tokens: codexToken{
			IDToken:      idHead + "." + idBody + "." + studioPlaceholder,
			AccessToken:  accessHead + "." + accessBody + "." + accessPlaceholder,
			RefreshToken: studioPlaceholder,
			AccountID:    l.AccountID,
		},
		LastRefresh: l.LastRefresh,
	}, "", "  ")
}

// accessSignature is what the vault keeps as the provider's secret: the access token's
// signature, which is the part the guest's placeholder stands for.
func accessSignature(l codexLogin) (string, error) {
	_, _, sig, err := jwtParts(l.AccessToken)
	return sig, err
}
