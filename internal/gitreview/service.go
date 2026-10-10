// Package gitreview is the virtual git remote. Sandboxes clone, fetch and push through
// https://git.studio.internal/<forge>/<owner>/<repo>.git, which the gateway serves in
// process. Fetches are proxied to the forge with its token added on the host; pushes are
// staged in a bare repository on the host and go upstream only once the user approves the
// git.push approval they raise. The token never reaches the sandbox.
package gitreview

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Host is the virtual remote's name.
const Host = "git.studio.internal"

// Approval kinds.
const (
	KindPush = "git.push"
	KindPR   = "git.pr"
)

// Defaults for Limits.
const (
	DefaultMaxPack     = 100 << 20 // bytes in one push
	DefaultMaxRequest  = 16 << 20  // bytes in one fetch negotiation
	DefaultMaxCommands = 32        // ref updates in one push
	DefaultMaxCommits  = 50        // commits listed in a review
	DefaultMaxDiff     = 512 << 10 // bytes of diff in a review
	DefaultMaxBlob     = 1 << 20   // files larger than this are summarized, not diffed
)

// Limits bound what a sandbox can make Studio do. Zero fields take the defaults.
type Limits struct {
	MaxPack, MaxRequest, MaxDiff, MaxBlob int64
	MaxCommands, MaxCommits               int
}

// Secrets unseals forge tokens.
type Secrets interface {
	Value(ctx context.Context, envID, id string) (string, error)
}

// Service serves the virtual remote and settles git approvals.
type Service struct {
	Store   *store.Store
	Secrets Secrets
	Bus     *events.Bus
	Dir     string       // staging repositories live here
	Client  *http.Client // reaches forges; nil means public addresses only, no redirects
	Log     *slog.Logger
	Limits  Limits

	once   sync.Once
	client *http.Client
	locks  sync.Map // staging path -> *sync.Mutex
}

func (s *Service) httpClient() *http.Client {
	s.once.Do(func() {
		s.client = s.Client
		if s.client == nil {
			s.client = &http.Client{
				Transport: &http.Transport{
					DialContext:         gateway.DialPublic,
					ForceAttemptHTTP2:   true,
					TLSHandshakeTimeout: 15 * time.Second,
					IdleConnTimeout:     90 * time.Second,
				},
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
		}
		if s.Limits.MaxPack == 0 {
			s.Limits.MaxPack = DefaultMaxPack
		}
		if s.Limits.MaxRequest == 0 {
			s.Limits.MaxRequest = DefaultMaxRequest
		}
		if s.Limits.MaxDiff == 0 {
			s.Limits.MaxDiff = DefaultMaxDiff
		}
		if s.Limits.MaxBlob == 0 {
			s.Limits.MaxBlob = DefaultMaxBlob
		}
		if s.Limits.MaxCommands == 0 {
			s.Limits.MaxCommands = DefaultMaxCommands
		}
		if s.Limits.MaxCommits == 0 {
			s.Limits.MaxCommits = DefaultMaxCommits
		}
		if s.Log == nil {
			s.Log = slog.New(slog.DiscardHandler)
		}
	})
	return s.client
}

func (s *Service) lock(path string) func() {
	m, _ := s.locks.LoadOrStore(path, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ID names the integration.
func (s *Service) ID() string { return "git" }

// Routes are the virtual hosts the gateway serves for the integration.
func (s *Service) Routes() []gateway.VirtualHost {
	return []gateway.VirtualHost{{Name: Host, Serve: s.Serve}}
}

// --- the virtual remote ------------------------------------------------------------------

var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`)
	services = map[string]bool{"git-upload-pack": true, "git-receive-pack": true}
)

// target is a parsed request to the virtual remote.
type target struct {
	sb          store.Sandbox
	forge       store.Forge
	owner, repo string
	op          string // info/refs, git-upload-pack or git-receive-pack
	service     string // the service asked for
}

// Serve returns the handler for one sandbox's connections to the virtual remote.
func (s *Service) Serve(sb store.Sandbox) http.Handler {
	s.httpClient()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, code, msg := s.parse(r, sb)
		if code != 0 {
			refuse(w, code, msg)
			return
		}
		switch t.service {
		case "git-upload-pack":
			s.uploadPack(w, r, t)
		case "git-receive-pack":
			s.receivePack(w, r, t)
		}
	})
}

func (s *Service) parse(r *http.Request, sb store.Sandbox) (target, int, string) {
	t := target{sb: sb}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		return t, http.StatusNotFound, "Use https://" + Host + "/<forge>/<owner>/<repo>.git"
	}
	forge, owner, repo := parts[0], parts[1], strings.TrimSuffix(parts[2], ".git")
	t.op = strings.Join(parts[3:], "/")
	if !forgeName.MatchString(forge) || !repoName.MatchString(owner) || !repoName.MatchString(repo) ||
		strings.Contains(owner, "..") || strings.Contains(repo, "..") {
		return t, http.StatusNotFound, "Use https://" + Host + "/<forge>/<owner>/<repo>.git"
	}
	t.owner, t.repo = owner, repo
	switch {
	case t.op == "info/refs" && r.Method == http.MethodGet:
		t.service = r.URL.Query().Get("service")
		if !services[t.service] {
			return t, http.StatusForbidden, "Only smart HTTP is supported; use a git client from this decade."
		}
	case services[t.op] && r.Method == http.MethodPost:
		t.service = t.op
	default:
		return t, http.StatusNotFound, "Not a git smart HTTP request."
	}
	f, err := s.Store.ForgeByName(r.Context(), sb.EnvironmentID, forge)
	if errors.Is(err, store.ErrNotFound) {
		return t, http.StatusNotFound, fmt.Sprintf("This environment has no forge %q. Add it on the Forges page in Sandbox Studio.", forge)
	}
	if err != nil {
		s.Log.Error("git remote: forge", "err", err)
		return t, http.StatusInternalServerError, "Sandbox Studio could not look up the forge."
	}
	t.forge = f
	return t, 0, ""
}

// refuse answers a git client with a message it shows its user as "remote: ..." lines.
// Never 401: git would ask the sandbox for credentials it must not have.
func refuse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Sandbox-Studio", "git")
	w.WriteHeader(code)
	io.WriteString(w, msg+"\n")
}

func (t target) upstreamURL(rest string) string {
	return t.forge.BaseURL + "/" + t.owner + "/" + t.repo + ".git" + rest
}

func (s *Service) token(ctx context.Context, f store.Forge) (string, error) {
	return s.Secrets.Value(ctx, f.EnvironmentID, f.SecretID)
}

// uploadPack proxies a fetch to the forge, adding the token. A rejected or failed push
// the sandbox hasn't heard about yet fails its next fetch once, with the reason.
func (s *Service) uploadPack(w http.ResponseWriter, r *http.Request, t target) {
	ctx := r.Context()
	if t.op == "info/refs" {
		if msg := s.undelivered(ctx, t); msg != "" {
			refuse(w, http.StatusConflict, msg)
			return
		}
	}
	token, err := s.token(ctx, t.forge)
	if err != nil {
		s.Log.Error("git remote: forge token", "forge", t.forge.Name, "err", err)
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not read the forge's token.")
		return
	}
	ad, err := adapter(t.forge, s.httpClient())
	if err != nil {
		refuse(w, http.StatusNotImplemented, err.Error())
		return
	}
	url := t.upstreamURL("/" + t.op)
	var body io.Reader
	if r.Method == http.MethodPost {
		body = http.MaxBytesReader(w, r.Body, s.Limits.MaxRequest)
	} else {
		url += "?service=git-upload-pack"
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, url, body)
	if err != nil {
		refuse(w, http.StatusBadGateway, "Sandbox Studio could not build the request to the forge.")
		return
	}
	for _, h := range []string{"Content-Type", "Content-Encoding", "Accept", "Git-Protocol"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("User-Agent", "git/2 (sandbox-studio)")
	user, pass := ad.GitAuth(token)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	resp, err := s.httpClient().Do(req)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			refuse(w, http.StatusRequestEntityTooLarge, "The fetch request is too large.")
			return
		}
		refuse(w, http.StatusBadGateway, "Sandbox Studio could not reach the forge: "+mask(err.Error(), token))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		code := http.StatusBadGateway
		msg := fmt.Sprintf("The forge answered %s", resp.Status)
		switch resp.StatusCode {
		case http.StatusNotFound:
			code, msg = http.StatusNotFound, fmt.Sprintf("The forge has no repository %s/%s (or the token can't see it).", t.owner, t.repo)
		case http.StatusUnauthorized, http.StatusForbidden:
			msg = "The forge refused Sandbox Studio's token. Check the forge on the Forges page."
		}
		if text := strings.TrimSpace(string(data)); text != "" && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
			msg += "\n" + mask(text, token)
		}
		refuse(w, code, msg)
		return
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "Expires", "Pragma"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// undelivered tells the sandbox about its pushes to t's repository that were rejected or
// failed since it last fetched, once each.
func (s *Service) undelivered(ctx context.Context, t target) string {
	list, err := s.Store.UndeliveredGitPushes(ctx, t.sb.EnvironmentID, t.sb.ID, t.forge.ID, t.owner, t.repo)
	if err != nil || len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Sandbox Studio: some of your pushes did not go upstream.\n")
	for _, p := range list {
		branch := strings.TrimPrefix(p.Ref, "refs/heads/")
		switch p.State {
		case store.PushRejected:
			fmt.Fprintf(&b, "- the push of %s to %s was rejected by the reviewer", short(p.NewSHA), branch)
			if p.Note != "" {
				fmt.Fprintf(&b, ": %s", oneLine(p.Note))
			}
		default:
			fmt.Fprintf(&b, "- the approved push of %s to %s failed: %s", short(p.NewSHA), branch, oneLine(p.Result))
		}
		b.WriteString("\n")
		if err := s.Store.MarkGitPushDelivered(ctx, p.EnvironmentID, p.ID); err != nil {
			s.Log.Error("git remote: mark delivered", "push", p.ID, "err", err)
		}
	}
	b.WriteString("This message is shown once; fetch again to continue.")
	// TODO(M5): also notify the agent through its MCP channel when the hooks exist.
	return b.String()
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}

// stagingPath is the bare repository holding one sandbox's pushes to one repository.
func (s *Service) stagingPath(t target) string {
	return filepath.Join(s.Dir, t.sb.EnvironmentID, t.sb.ID, t.forge.ID, t.owner, t.repo+".git")
}

func (s *Service) publish(envID, approvalID string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID, ID: approvalID})
	}
}
