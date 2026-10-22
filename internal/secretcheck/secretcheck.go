// Package secretcheck tells the user when a secret looks wrong: a host keeps answering 401 to
// the requests the gateway swapped the secret's value into (docs/secrets.md). The sandbox
// can't tell, since it only ever sees the placeholder.
package secretcheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Kind is a card for a secret a host refused again and again. Allowing it means the user
// went to change the value; dismissing it keeps Studio from asking about the secret and host
// again for a while.
const Kind = "secret.rejected"

const (
	// threshold rejections within window raise the card: one 401 is often a probe without
	// credentials, or a token being refreshed.
	threshold = 3
	window    = 10 * time.Minute
	// snooze is how long "Not now" keeps Studio from asking about a secret and host again.
	snooze = 24 * time.Hour
)

// subscriptions are the provider kinds whose secret Studio refreshes itself; a 401 there is
// the sign-in's business, which the providers page shows.
var subscriptions = []string{"claude_subscription", "chatgpt_subscription"}

// RejectedPayload is what a secret.rejected card shows.
type RejectedPayload struct {
	SecretID     string `json:"secretId"`
	Name         string `json:"name" doc:"The secret's name, as the sandbox's environment variable"`
	Host         string `json:"host" doc:"The host that answered 401"`
	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	TeammateID   string `json:"teammateId,omitempty"`
	TeammateName string `json:"teammateName,omitempty"`
	ProviderID   string `json:"providerId,omitempty" doc:"The AI provider whose key the secret is; its key is changed in Settings → AI providers"`
	ProviderName string `json:"providerName,omitempty"`
}

// Service counts the rejections the gateway reports and raises the cards.
type Service struct {
	Store *store.Store
	Bus   *events.Bus
	Log   *slog.Logger
	Now   func() time.Time // time.Now when nil

	mu      sync.Mutex
	seen    map[string][]time.Time // recent rejections, by environment, sandbox, secret name and host
	snoozed map[string]time.Time   // until when "Not now" holds, by environment, secret and host
}

func (s *Service) ID() string                    { return "secret-check" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }
func (s *Service) Tools() []integrations.Tool    { return nil }
func (s *Service) Jobs() []integrations.Job      { return nil }
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: Kind, Decide: s.decide}}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Rejected is the gateway's Rejected hook: host answered 401 to a request of sb's carrying
// the secrets names. It returns at once; the card, if one is due, is raised in the background.
func (s *Service) Rejected(sb store.Sandbox, host string, names []string) {
	var due []string
	now := s.now()
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string][]time.Time{}
	}
	for _, name := range names {
		key := sb.EnvironmentID + "/" + sb.ID + "/" + name + "/" + host
		recent := slices.DeleteFunc(s.seen[key], func(t time.Time) bool { return now.Sub(t) > window })
		recent = append(recent, now)
		if len(recent) >= threshold {
			due = append(due, name)
			recent = nil // the next card takes as many rejections again
		}
		if len(recent) == 0 {
			delete(s.seen, key)
		} else {
			s.seen[key] = recent
		}
	}
	s.mu.Unlock()
	for _, name := range due {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.raise(ctx, sb, host, name); err != nil && s.Log != nil {
				s.Log.Error("secret check", "sandbox", sb.ID, "host", host, "secret", name, "err", err)
			}
		}()
	}
}

// raise posts the card for the secret name that sb sees, unless the user said not now or a
// subscription's sign-in owns it.
func (s *Service) raise(ctx context.Context, sb store.Sandbox, host, name string) error {
	list, err := s.Store.Secrets(ctx, sb.EnvironmentID)
	if err != nil {
		return err
	}
	visible := store.VisibleSecrets(list, sb.ID, sb.TeammateID)
	i := slices.IndexFunc(visible, func(sec store.Secret) bool { return sec.Name == name })
	if i < 0 {
		return nil // deleted since
	}
	sec := visible[i]
	s.mu.Lock()
	until, snoozed := s.snoozed[snoozeKey(sb.EnvironmentID, sec.ID, host)]
	s.mu.Unlock()
	if snoozed && s.now().Before(until) {
		return nil
	}
	p := RejectedPayload{SecretID: sec.ID, Name: sec.Name, Host: host, SandboxID: sb.ID, SandboxName: sb.Name}
	providers, err := s.Store.Providers(ctx, sb.EnvironmentID)
	if err != nil {
		return err
	}
	for _, pr := range providers {
		if pr.SecretID != sec.ID {
			continue
		}
		if slices.Contains(subscriptions, pr.Kind) {
			return nil
		}
		p.ProviderID, p.ProviderName = pr.ID, pr.Name
	}
	if sb.TeammateID != "" {
		tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil {
			p.TeammateID, p.TeammateName = tm.ID, tm.Name
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: Kind,
		Subject: fmt.Sprintf("%s refused the secret %s", host, sec.Name), Payload: raw,
	})
	if err != nil {
		return err
	}
	s.publish(sb.EnvironmentID, a.ID)
	return nil
}

// decide settles a secret.rejected card. Allowing it only clears it, since the web goes on to
// the secret; the other decisions also keep Studio from asking about the secret and host
// again for a day.
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
	var p RejectedPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	if st != store.StatusApproved {
		s.mu.Lock()
		if s.snoozed == nil {
			s.snoozed = map[string]time.Time{}
		}
		s.snoozed[snoozeKey(a.EnvironmentID, p.SecretID, p.Host)] = s.now().Add(snooze)
		s.mu.Unlock()
	}
	s.publish(a.EnvironmentID, a.ID)
	return nil
}

func snoozeKey(envID, secretID, host string) string { return envID + "/" + secretID + "/" + host }

func (s *Service) publish(envID, approvalID string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID, ID: approvalID})
	}
}
