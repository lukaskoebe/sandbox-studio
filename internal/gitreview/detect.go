package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// detectTimeout bounds asking a host which forge it runs.
const detectTimeout = 5 * time.Second

// probe is one question Detect asks a host: an endpoint a forge answers without a token, and
// whether an answer is that forge's.
type probe struct {
	kind string
	path string
	is   func(status int, h http.Header, body map[string]any) bool
}

var probes = []probe{
	// Forgejo and Gitea: {"version":"…"}.
	{KindForgejo, "/api/v1/version", func(status int, _ http.Header, body map[string]any) bool {
		_, ok := body["version"].(string)
		return status == http.StatusOK && ok
	}},
	// GitLab wants a token for its version and says so in its own words.
	{KindGitLab, "/api/v4/version", func(status int, h http.Header, body map[string]any) bool {
		if h.Get("X-Gitlab-Meta") != "" {
			return true
		}
		_, revision := body["revision"].(string)
		return status == http.StatusUnauthorized && body["message"] == "401 Unauthorized" || status == http.StatusOK && revision
	}},
	// GitHub Enterprise Server.
	{KindGitHub, "/api/v3/meta", func(status int, h http.Header, body map[string]any) bool {
		_, ok := body["verifiable_password_authentication"]
		return h.Get("X-GitHub-Enterprise-Version") != "" || status == http.StatusOK && ok
	}},
}

// Detect asks the host at baseURL, https://host[:port][/path], which API it has: Forgejo's
// or Gitea's, GitLab's or GitHub Enterprise's, by endpoints they answer without a token. It
// returns "" for a host that answers as none of them or doesn't answer.
func (s *Service) Detect(ctx context.Context, baseURL string) string {
	base, host, err := publicBase(baseURL)
	if err != nil {
		return ""
	}
	if k, ok := apiHosts[host]; ok {
		return k
	}
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()
	found := make([]bool, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Go(func() { found[i] = s.ask(ctx, base+p.path, p.is) })
	}
	wg.Wait()
	for i, p := range probes {
		if found[i] {
			return p.kind
		}
	}
	return ""
}

func (s *Service) ask(ctx context.Context, url string, is func(int, http.Header, map[string]any) bool) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body map[string]any
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(data, &body)
	return is(resp.StatusCode, resp.Header, body)
}

// DetectRepoURL is ParseRepoURL for hosts Studio doesn't know: it asks the host (Detect)
// which forge it runs and reads the URL as one of that forge's, so an HTTPS URL of a
// self-hosted Forgejo makes a forgejo forge and its SSH URL an ssh forge with Forgejo's API.
func (s *Service) DetectRepoURL(ctx context.Context, raw string) (RepoURL, error) {
	r, err := ParseRepoURL(raw)
	if err != nil {
		return r, err
	}
	var web, host string
	switch {
	case r.Kind == KindHTTPS:
		if _, host, err = publicBase(r.BaseURL); err != nil {
			return r, nil
		}
		web = r.BaseURL
	case r.Kind == KindSSH && r.APIKind == "":
		u, err := ParseSSHURL(r.BaseURL)
		if err != nil {
			return r, nil
		}
		host, web = u.Host, "https://"+sshWebHost(u.Host)
	default:
		return r, nil
	}
	kind := s.Detect(ctx, web)
	if kind == "" {
		return r, nil
	}
	return parseRepoURL(raw, map[string]string{host: kind})
}

// sshWebHost is the host of the web address an SSH host's forge likely has: the same name,
// an IPv6 address in brackets.
func sshWebHost(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "[" + host + "]"
	}
	return host
}

// CheckToken checks that the API of kind at baseURL answers and accepts token, as a forge's
// test does, before a forge with them is added. It returns the user the token belongs to.
func (s *Service) CheckToken(ctx context.Context, kind, baseURL, token string) (Account, error) {
	ad, err := adapter(store.Forge{Kind: kind, BaseURL: baseURL}, s.httpClient())
	if err != nil {
		return Account{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	a, err := ad.Check(ctx, token)
	if err != nil {
		var syntax *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &typ) {
			return a, fmt.Errorf("the answer isn't JSON from the forge's API; is %s the forge's address?", baseURL)
		}
		return a, err
	}
	if a.User == "" {
		return a, fmt.Errorf("the answer names no user; is %s the forge's address?", baseURL)
	}
	return a, nil
}
