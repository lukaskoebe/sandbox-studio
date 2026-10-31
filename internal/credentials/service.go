// Package credentials issues sandboxes short-lived credentials made from secrets with a kind
// (docs/network.md#credentials). The secret stays in Studio; a sandbox's tools ask for what
// it issues through the helper interface they already have, such as AWS's
// credential_process, which runs `studio-agent credential`. The kinds are recipes, data
// embedded in Studio (recipes/*.yaml); the issuers they name are code.
package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Kind is the card asking the user to let a sandbox have a secret's credentials.
const Kind = "credential.issue"

const (
	// defaultHold is how long a helper's call waits for the user's answer. The guest relays a
	// call for two minutes, so the answer that it's still waiting gets back in time.
	defaultHold = 100 * time.Second
	// renewBefore is how long before a cached credential expires a new one is issued, so a
	// tool never gets one about to expire.
	renewBefore = 5 * time.Minute
)

// IssuePayload is what a credential.issue card shows.
type IssuePayload struct {
	SecretID     string `json:"secretId"`
	Name         string `json:"name" doc:"The secret's name"`
	Kind         string `json:"kind" doc:"The secret's kind, such as aws"`
	KindTitle    string `json:"kindTitle" doc:"The kind as the user reads it, such as AWS access key"`
	Lifetime     string `json:"lifetime" doc:"How long each credential lasts, such as 1h0m0s"`
	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	TeammateID   string `json:"teammateId,omitempty"`
	TeammateName string `json:"teammateName,omitempty"`
	// StartedAt is the sandbox's start the answer holds for, without "always".
	StartedAt int64 `json:"startedAt,omitempty"`
}

// Secrets is what the service needs of the vault.
type Secrets interface {
	Value(ctx context.Context, envID, id string) (string, error)
}

// Service is the credentials integration: the helper's call, the approval and the sandbox's
// files and environment.
type Service struct {
	Store   *store.Store
	Secrets Secrets
	Bus     *events.Bus
	Log     *slog.Logger
	Client  *http.Client  // http.DefaultClient when nil
	Hold    time.Duration // defaultHold when zero
	Now     func() time.Time

	mu      sync.Mutex
	granted map[string]bool            // answers for a sandbox's run, by run and secret
	cache   map[string]Issued          // by sandbox, secret and its version
	waiters map[string][]chan<- string // calls waiting for an approval, by its ID
}

func (s *Service) ID() string                    { return "credentials" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }
func (s *Service) Jobs() []integrations.Job      { return nil }
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: Kind, Decide: s.decide}}
}
func (s *Service) Tools() []integrations.Tool {
	return []integrations.Tool{{Def: agentcall.CredentialCall, Handle: s.call}}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// KindInfo is a secret kind as the secret form shows it.
type KindInfo struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Fields      []Field  `json:"fields"`
	Hosts       []string `json:"hosts"`
	Lifetime    string   `json:"lifetime" doc:"How long each credential lasts, such as 1h0m0s"`
}

// Kinds lists the kinds, by title.
func Kinds() []KindInfo {
	out := []KindInfo{}
	for _, r := range Recipes {
		out = append(out, KindInfo{Name: r.Name, Title: r.Title, Description: r.Description, Fields: r.Fields, Hosts: r.Hosts, Lifetime: r.Lifetime.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// CheckSecret validates a new or changed secret of a kind: its fields, normalized, and the
// secret field's value, unless it is kept.
func CheckSecret(kind string, fields map[string]string) (map[string]string, error) {
	r, ok := Recipes[kind]
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	return r.CheckFields(fields)
}

// visible returns the secrets with a kind that sb sees, the most specific of each name.
func (s *Service) visible(ctx context.Context, sb store.Sandbox) ([]store.Secret, error) {
	list, err := s.Store.Secrets(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(sec store.Secret) bool { return Recipes[sec.Kind] == nil })
	return store.VisibleSecrets(list, sb.ID, sb.TeammateID), nil
}

// Config is the sandbox's part of the credentials: each kind's managed file blocks, with an
// entry for every secret of the kind it sees, and the environment of a kind it sees one secret
// of. A kind it sees none of gets empty blocks, which remove those of a deleted secret.
func (s *Service) Config(ctx context.Context, sb store.Sandbox) ([]agentproto.HomeFile, map[string]string, error) {
	visible, err := s.visible(ctx, sb)
	if err != nil {
		return nil, nil, err
	}
	byKind := map[string][]store.Secret{}
	for _, sec := range visible {
		byKind[sec.Kind] = append(byKind[sec.Kind], sec)
	}
	names := slices.Sorted(maps.Keys(Recipes))
	var files []agentproto.HomeFile
	env := map[string]string{}
	for _, name := range names {
		r := Recipes[name]
		secs := byKind[name]
		for _, f := range r.Files {
			var b strings.Builder
			b.WriteString(f.Comment + agentproto.BlockBegin + "\n")
			for i, sec := range secs {
				if i > 0 {
					b.WriteString("\n")
				}
				b.WriteString(strings.TrimRight(render(f.Content, templateVars(sec, nil)), "\n") + "\n")
			}
			b.WriteString(f.Comment + agentproto.BlockEnd + "\n")
			files = append(files, agentproto.HomeFile{Path: f.Path, Content: b.String(), Mode: 0o600, Block: true, Comment: f.Comment})
		}
		if len(secs) == 1 {
			for k, v := range r.Env {
				if v := render(v, templateVars(secs[0], nil)); envValue.MatchString(v) {
					env[k] = v
				}
			}
		}
	}
	return files, env, nil
}

// envValue is what the guest takes as an environment variable's value.
var envValue = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)

func templateVars(sec store.Secret, issued *Issued) map[string]string {
	vars := map[string]string{"secret": sec.Name}
	for k, v := range sec.Fields {
		vars["field."+k] = v
	}
	if issued != nil {
		for k, v := range issued.Fields {
			vars["cred."+k] = v
		}
		vars["cred.expires"] = issued.Expires.UTC().Format(time.RFC3339)
	}
	return vars
}

// call answers `studio-agent credential <secret> <format>`.
func (s *Service) call(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Secret string `json:"secret"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, integrations.ToolError("invalid arguments: %v", err)
	}
	visible, err := s.visible(ctx, sb)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(visible, func(sec store.Secret) bool { return sec.Name == in.Secret })
	if i < 0 {
		return nil, integrations.ToolError("this sandbox sees no secret %s with a kind; the user adds one in Settings → Secrets", in.Secret)
	}
	sec := visible[i]
	r := Recipes[sec.Kind]
	answer, ok := r.Answers[in.Format]
	if !ok {
		return nil, integrations.ToolError("a %s has no answer of the format %q", r.Title, in.Format)
	}
	issued, err := s.issue(ctx, sb, sec, r)
	if err != nil {
		return nil, err
	}
	out := renderValue(answer, templateVars(sec, &issued))
	if text, ok := out.(string); ok {
		return agentcall.CredentialAnswer{Output: text}, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return agentcall.CredentialAnswer{Output: string(raw) + "\n"}, nil
}

// issue returns a credential of sec for sb: the cached one, or a new one once the user
// allows it.
func (s *Service) issue(ctx context.Context, sb store.Sandbox, sec store.Secret, r *Recipe) (Issued, error) {
	key := sb.ID + "/" + sec.ID + "/" + sec.UpdatedAt.String()
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok && s.now().Add(renewBefore).Before(cached.Expires) {
		return cached, nil
	}
	if err := s.allowed(ctx, sb, sec, r); err != nil {
		return Issued{}, err
	}
	value, err := s.Secrets.Value(ctx, sb.EnvironmentID, sec.ID)
	if err != nil {
		return Issued{}, err
	}
	issued, err := issuers[r.Issuer].Issue(ctx, IssueInput{Fields: sec.Fields, Secret: value, Lifetime: r.Lifetime.Duration, Session: sb.Name, Client: s.Client})
	if err != nil {
		return Issued{}, integrations.ToolError("Studio couldn't issue credentials from %s: %v", sec.Name, err)
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]Issued{}
	}
	for k, c := range s.cache {
		if strings.HasPrefix(k, sb.ID+"/"+sec.ID+"/") || !s.now().Before(c.Expires) {
			delete(s.cache, k)
		}
	}
	s.cache[key] = issued
	s.mu.Unlock()
	if s.Log != nil {
		s.Log.Info("issued credentials", "sandbox", sb.ID, "secret", sec.Name, "kind", sec.Kind, "expires", issued.Expires)
	}
	return issued, nil
}

// runKey names the sandbox's run, which an answer without "always" holds for.
func runKey(sb store.Sandbox, secretID string) string {
	run := int64(0)
	if sb.StartedAt != nil {
		run = sb.StartedAt.Unix()
	}
	return fmt.Sprintf("%s/%d/%s", sb.ID, run, secretID)
}

// allowed returns nil once the user lets sb have sec's credentials: they did for its run or
// always, or do now. Meanwhile the call waits, for a while.
func (s *Service) allowed(ctx context.Context, sb store.Sandbox, sec store.Secret, r *Recipe) error {
	s.mu.Lock()
	ok := s.granted[runKey(sb, sec.ID)]
	s.mu.Unlock()
	if ok {
		return nil
	}
	if ok, err := s.Store.CredentialGranted(ctx, sb.EnvironmentID, sec.ID, sb.ID, sb.TeammateID); err != nil || ok {
		return err
	}
	p := IssuePayload{SecretID: sec.ID, Name: sec.Name, Kind: sec.Kind, KindTitle: r.Title, Lifetime: r.Lifetime.String(), SandboxID: sb.ID, SandboxName: sb.Name}
	if sb.StartedAt != nil {
		p.StartedAt = sb.StartedAt.Unix()
	}
	who := sb.Name
	if sb.TeammateID != "" {
		tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil {
			p.TeammateID, p.TeammateName, who = tm.ID, tm.Name, tm.Name
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	a, created, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: Kind,
		Subject: fmt.Sprintf("%s asks for credentials from %s", who, sec.Name), Payload: raw,
	})
	if err != nil {
		return err
	}
	done := make(chan string, 1)
	s.mu.Lock()
	if s.waiters == nil {
		s.waiters = map[string][]chan<- string{}
	}
	s.waiters[a.ID] = append(s.waiters[a.ID], done)
	s.mu.Unlock()
	defer s.forget(a.ID, done)
	if created && s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: sb.EnvironmentID, ID: a.ID})
	}
	// It may have been decided between the request and the waiter.
	if cur, err := s.Store.Approval(ctx, sb.EnvironmentID, a.ID); err == nil && cur.Status != store.StatusPending {
		select {
		case done <- cur.Status:
		default:
		}
	}
	hold := s.Hold
	if hold == 0 {
		hold = defaultHold
	}
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case st := <-done:
		if st == store.StatusApproved {
			return nil
		}
		return integrations.ToolError("the user didn't let this sandbox have credentials from %s", sec.Name)
	case <-timer.C:
		return integrations.ToolError("waiting for the user to allow credentials from %s in Sandbox Studio; run the command again once they have", sec.Name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) forget(id string, done chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiters[id] = slices.DeleteFunc(s.waiters[id], func(c chan<- string) bool { return c == done })
	if len(s.waiters[id]) == 0 {
		delete(s.waiters, id)
	}
}

// decide settles a credential.issue card. Allowing it lets the sandbox have the secret's
// credentials until it stops, or with Remember always: every sandbox of the teammate, or the
// sandbox if no teammate owns it.
func (s *Service) decide(ctx context.Context, a store.Approval, d integrations.Decision) error {
	var st string
	switch d.Action {
	case integrations.Allow:
		st = store.StatusApproved
	case integrations.Deny:
		st = store.StatusDenied
	case integrations.Dismiss:
		st = store.StatusDismissed
	default:
		return fmt.Errorf("%w %q", integrations.ErrAction, d.Action)
	}
	var p IssuePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if st == store.StatusApproved && d.Remember {
		g := store.CredentialGrant{EnvironmentID: a.EnvironmentID, SecretID: p.SecretID, TeammateID: p.TeammateID}
		if g.TeammateID == "" {
			g.SandboxID = p.SandboxID
		}
		if err := s.Store.GrantCredential(ctx, g); err != nil {
			return err
		}
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	s.mu.Lock()
	if st == store.StatusApproved {
		if s.granted == nil {
			s.granted = map[string]bool{}
		}
		var started *time.Time
		if p.StartedAt != 0 {
			t := time.Unix(p.StartedAt, 0)
			started = &t
		}
		s.granted[runKey(store.Sandbox{ID: p.SandboxID, StartedAt: started}, p.SecretID)] = true
	}
	for _, c := range s.waiters[a.ID] {
		select {
		case c <- st:
		default:
		}
	}
	s.mu.Unlock()
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: a.EnvironmentID, ID: a.ID})
	}
	return nil
}
