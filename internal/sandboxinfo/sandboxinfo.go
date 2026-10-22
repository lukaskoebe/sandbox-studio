// Package sandboxinfo serves the sandbox_info tool: an agent's sandbox describing itself, so the
// agent doesn't guess its ports or its network policy (docs/harnesses.md).
package sandboxinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// maxRules bounds the rules one answer lists; the rest are only counted.
const maxRules = 50

// Service answers sandbox_info from the store, the guest's ports and the policy's level.
type Service struct {
	Store *store.Store
	// Ports lists the TCP ports listening in a sandbox (agentchan.Hub.Ports).
	Ports func(ctx context.Context, id string) ([]agentproto.Port, error)
	// Level is the security level for a teammate's sandboxes (policy.Engine.Level).
	Level func(ctx context.Context, envID, teammateID string) (string, error)
	// Now is the clock that decides which rules have expired; time.Now when nil.
	Now func() time.Time
}

func (s *Service) ID() string                                 { return "sandbox-info" }
func (s *Service) Routes() []gateway.VirtualHost              { return nil }
func (s *Service) ApprovalKinds() []integrations.ApprovalKind { return nil }
func (s *Service) Jobs() []integrations.Job                   { return nil }
func (s *Service) Tools() []integrations.Tool {
	return []integrations.Tool{{Def: agentcall.SandboxInfoTool, Handle: s.info}}
}

// Info is what sandbox_info returns.
type Info struct {
	Sandbox  Named   `json:"sandbox"`
	Teammate *Named  `json:"teammate,omitempty"`
	Ports    []Port  `json:"ports"`
	Previews string  `json:"previews"`
	Network  Network `json:"network"`
}

// Named is a sandbox or teammate.
type Named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Port is a listening port and the link that opens it for the user.
type Port struct {
	Port int    `json:"port"`
	Link string `json:"link"`
}

// Network is the policy that decides the sandbox's connections.
type Network struct {
	Level   string `json:"level"`
	Meaning string `json:"meaning"`
	Rules   []Rule `json:"rules"`
	// MoreRules counts the rules left out of Rules.
	MoreRules int    `json:"moreRules,omitempty"`
	Waiting   string `json:"waiting"`
}

// Rule is a rule that applies to the sandbox, without its configuration or note.
type Rule struct {
	Host      string     `json:"host"`
	Ports     []int      `json:"ports,omitempty"`
	Action    string     `json:"action"`
	Method    string     `json:"method,omitempty"`
	Path      string     `json:"path,omitempty"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

var meanings = map[string]string{
	store.LevelLocked:   "Every host no rule covers waits for the user's approval.",
	store.LevelGuarded:  "Curated development hosts (package registries, toolchain downloads, read access to forges) connect; other hosts no rule covers wait for the user's approval.",
	store.LevelAssisted: "Curated development hosts connect; an AI reviewer decides other hosts no rule covers, and asks the user when it is unsure.",
	store.LevelOpen:     "Public hosts connect and are logged. Hosts on the local network and this computer still wait for the user's approval.",
}

var actions = map[string]string{
	store.ActionAllow: "allow",
	store.ActionDeny:  "deny",
	store.ActionProxy: "allow, through Studio, which may add headers",
	store.ActionCaddy: "answered by Studio",
}

const (
	previews = "Servers you start listen in this sandbox, not on the user's computer, so localhost doesn't reach them for the user. " +
		"Give the user a port's link as it is: in Studio's chat it becomes a button that opens the port."
	waiting = "A connection that hangs for up to a minute is waiting for the user's approval: don't retry it in a loop. " +
		"A 403 with X-Sandbox-Studio: refused means the policy refused it."
)

func (s *Service) info(ctx context.Context, sb store.Sandbox, _ json.RawMessage) (any, error) {
	out := Info{Sandbox: Named{sb.ID, sb.Name}, Ports: []Port{}, Previews: previews}
	if sb.TeammateID != "" {
		tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			out.Teammate = &Named{tm.ID, tm.Name}
		}
	}
	ports, err := s.Ports(ctx, sb.ID)
	if err != nil {
		return nil, fmt.Errorf("ports: %w", err)
	}
	for _, p := range ports {
		out.Ports = append(out.Ports, Port{p.Port, fmt.Sprintf("http://localhost:%d/", p.Port)})
	}
	level, err := s.Level(ctx, sb.EnvironmentID, sb.TeammateID)
	if err != nil {
		return nil, fmt.Errorf("level: %w", err)
	}
	out.Network = Network{Level: level, Meaning: meanings[level], Rules: []Rule{}, Waiting: waiting}
	rules, err := s.Store.Rules(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	// The sandbox's own rules first, then its teammate's, then the environment's: the order a
	// reader narrows down in.
	for _, scope := range []string{"sandbox", "teammate", "environment"} {
		for _, r := range rules {
			if r.BrowserOnly || (r.ExpiresAt != nil && !r.ExpiresAt.After(now())) || scopeOf(r, sb) != scope {
				continue
			}
			if len(out.Network.Rules) == maxRules {
				out.Network.MoreRules++
				continue
			}
			action := actions[r.Action]
			if action == "" {
				action = r.Action
			}
			out.Network.Rules = append(out.Network.Rules, Rule{
				Host: r.Host, Ports: r.Ports, Action: action, Method: r.Method, Path: r.PathPrefix,
				Scope: scope, ExpiresAt: r.ExpiresAt,
			})
		}
	}
	return out, nil
}

// scopeOf is which of the sandbox's scopes a rule covers, or "" when it covers another sandbox
// or teammate.
func scopeOf(r store.Rule, sb store.Sandbox) string {
	switch {
	case r.SandboxID != "":
		if r.SandboxID == sb.ID {
			return "sandbox"
		}
	case r.TeammateID != "":
		if r.TeammateID == sb.TeammateID {
			return "teammate"
		}
	default:
		return "environment"
	}
	return ""
}
