package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// refreshLead is how long before its expiry Studio refreshes a ChatGPT access token.
const refreshLead = 5 * time.Minute

// The OAuth token endpoint of ChatGPT logins and the client ID the Codex CLI refreshes with.
// verify (S8): both, against the pinned Codex.
const (
	codexTokenURL = "https://auth.openai.com/oauth/token"
	codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

// ErrSignInAgain is a refresh the vendor refused: the login was revoked or its refresh token has
// expired, and the user has to sign in again.
var ErrSignInAgain = errors.New("the ChatGPT sign-in was refused: sign in again")

// publicClient dials public addresses only, as the gateway's upstreams do, and uses the system's
// roots: Studio's own calls never go through the environment's CA.
func publicClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{DialContext: gateway.DialPublic, TLSHandshakeTimeout: 10 * time.Second},
	}
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return publicClient()
}

func (s *Service) tokenURL() string {
	if s.TokenURL != "" {
		return s.TokenURL
	}
	return codexTokenURL
}

// refreshCodex trades a ChatGPT login's refresh token for new tokens. The answer's tokens replace
// the login's; a token the answer leaves out is kept. A refusal is ErrSignInAgain.
func (s *Service) refreshCodex(ctx context.Context, l codexLogin) (codexLogin, error) {
	body, err := json.Marshal(map[string]string{
		"client_id": codexClientID, "grant_type": "refresh_token", "refresh_token": l.RefreshToken, "scope": "openid profile email",
	})
	if err != nil {
		return codexLogin{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL(), bytes.NewReader(body))
	if err != nil {
		return codexLogin{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client().Do(req)
	if err != nil {
		return codexLogin{}, fmt.Errorf("refreshing the ChatGPT sign-in: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return codexLogin{}, fmt.Errorf("refreshing the ChatGPT sign-in: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return codexLogin{}, ErrSignInAgain
	case resp.StatusCode/100 != 2:
		// The body is not shown: it may echo what was sent.
		return codexLogin{}, fmt.Errorf("refreshing the ChatGPT sign-in: %s", resp.Status)
	}
	var out struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return codexLogin{}, fmt.Errorf("the refresh answer is not JSON: %w", err)
	}
	next := l
	if out.IDToken != "" {
		next.IDToken = out.IDToken
	}
	if out.AccessToken != "" {
		next.AccessToken = out.AccessToken
	}
	if out.RefreshToken != "" {
		next.RefreshToken = out.RefreshToken
	}
	next.LastRefresh = s.now().UTC().Format(time.RFC3339Nano)
	if err := checkCodexLogin(next); err != nil {
		return codexLogin{}, fmt.Errorf("the refreshed login: %w", err)
	}
	return next, nil
}

// Run runs the service's background work until ctx ends: it removes the login VMs an earlier
// process left, and every minute refreshes the ChatGPT logins whose access tokens expire soon.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.bg = ctx
	s.mu.Unlock()
	s.sweep(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshDue(ctx)
		}
	}
}

// refreshDue refreshes every ChatGPT login that is due.
func (s *Service) refreshDue(ctx context.Context) {
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		s.Log.Warn("listing environments for refreshes", "err", err)
		return
	}
	for _, env := range envs {
		providers, err := s.Store.Providers(ctx, env.ID)
		if err != nil {
			s.Log.Warn("listing providers for refreshes", "environment", env.ID, "err", err)
			continue
		}
		for _, p := range providers {
			if p.Kind != teammates.KindChatGPTSubscription || p.SecretID == "" {
				continue
			}
			if err := s.refreshProvider(ctx, p); err != nil && ctx.Err() == nil {
				s.Log.Warn("refreshing a ChatGPT sign-in", "provider", p.ID, "err", err)
			}
		}
	}
}

// refreshProvider refreshes one provider's ChatGPT login if its access token expires within
// refreshLead. The new tokens go to the provider's key and its login, and the running sandboxes
// of its teammates get the new auth file, so their Codex keeps working.
func (s *Service) refreshProvider(ctx context.Context, p store.Provider) error {
	l, _, ok, err := s.login(ctx, p)
	if err != nil || !ok {
		return err
	}
	if exp, err := jwtExpiry(l.AccessToken); err == nil && exp.After(s.now().Add(refreshLead)) {
		return nil
	}
	next, err := s.refreshCodex(ctx, l)
	if err != nil {
		return err
	}
	if err := s.storeCodex(ctx, p, next); err != nil {
		return err
	}
	s.pushCodexAuth(ctx, p)
	return nil
}

// pushCodexAuth writes the provider's auth file into the running sandboxes of its teammates. A
// sandbox that isn't connected gets its file when its next session starts.
func (s *Service) pushCodexAuth(ctx context.Context, p store.Provider) {
	files, err := s.GuestFiles(ctx, p)
	if err != nil {
		s.Log.Warn("the new auth file", "provider", p.ID, "err", err)
		return
	}
	list, err := s.Store.Teammates(ctx, p.EnvironmentID)
	if err != nil {
		return
	}
	mine := map[string]bool{}
	for _, t := range list {
		if t.ProviderID == p.ID {
			mine[t.ID] = true
		}
	}
	sandboxes, err := s.Store.Sandboxes(ctx, p.EnvironmentID)
	if err != nil {
		return
	}
	for _, sb := range sandboxes {
		if sb.Kind != "" || !mine[sb.TeammateID] {
			continue
		}
		if _, ok := s.Guest.Connected(sb.ID); !ok {
			continue
		}
		if err := s.Guest.WriteHomeFiles(ctx, sb.ID, files); err != nil {
			s.Log.Warn("writing the new auth file", "sandbox", sb.ID, "err", err)
		}
	}
}
