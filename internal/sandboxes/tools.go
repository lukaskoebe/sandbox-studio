package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

var (
	// ErrGuestTooOld means the sandbox runs a guest agent older than what was asked of it, as
	// a sandbox started before Studio was upgraded does until it restarts.
	ErrGuestTooOld = errors.New("the computer runs an older guest agent: restart it to use this")
	// ErrHarnessUpdate wraps why the guest couldn't install a harness's version.
	ErrHarnessUpdate = errors.New("the update failed")
	// ErrInvalidHarnessUpdate means a harness that can't update here, or a version that isn't
	// an exact one.
	ErrInvalidHarnessUpdate = errors.New("invalid harness update")
)

const (
	toolsAsk  = 30 * time.Second
	updateAsk = 12 * time.Minute // the guest gives npm ten
)

// Where a mise tool came from (Tool.Origin).
const (
	ToolTemplate = "template" // the sandbox's template pins it, at this version
	ToolAdded    = "added"    // `mise use -g` pinned it in the sandbox
	ToolKept     = "kept"     // a rebase kept it from the sandbox's old VM
	ToolProject  = "project"  // a project's mise.toml asks for it
	ToolUnpinned = "unpinned" // installed, but no config asks for this version
)

// globalConfig and keptConfig are where the guest's mise keeps the global pins (guest.MiseDir).
const (
	globalConfig = "/opt/mise/config/config.toml"
	keptConfig   = "/opt/mise/config/conf.d/studio-kept-tools.toml"
)

// Tools is what a sandbox has installed.
type Tools struct {
	Harnesses []agentproto.HarnessVersion
	Mise      []Tool
	MiseError string
}

// Tool is one version of a tool mise has, and where it came from.
type Tool struct {
	agentproto.MiseTool
	Origin string
}

// Tools asks a running sandbox's guest what it has installed: the harnesses' versions, and
// each tool mise has with what pinned it.
func (m *Manager) Tools(ctx context.Context, envID, id string) (Tools, error) {
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return Tools{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, toolsAsk)
	defer cancel()
	t, err := m.Hub.Tools(ctx, id)
	if err != nil {
		return Tools{}, guestError(err)
	}
	return withOrigins(t, m.templatePins(ctx, sb)), nil
}

// UpdateHarness has a running sandbox install version of harness over the base image's, and
// returns its tools after.
func (m *Manager) UpdateHarness(ctx context.Context, envID, id, harness, version string) (Tools, error) {
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return Tools{}, err
	}
	if !slices.Contains(agentproto.UpdatableHarnesses, harness) {
		return Tools{}, fmt.Errorf("%w: %s can't be updated here", ErrInvalidHarnessUpdate, harness)
	}
	if !agentproto.HarnessVersionPattern.MatchString(version) {
		return Tools{}, fmt.Errorf("%w: %q is not an exact version", ErrInvalidHarnessUpdate, version)
	}
	ctx, cancel := context.WithTimeout(ctx, updateAsk)
	defer cancel()
	t, err := m.Hub.UpdateHarness(ctx, id, agentproto.HarnessUpdate{Harness: harness, Version: version})
	if err != nil {
		if errors.Is(guestError(err), ErrGuestTooOld) {
			return Tools{}, ErrGuestTooOld
		}
		if ctx.Err() == nil && !errors.Is(err, agentchan.ErrNotConnected) {
			return Tools{}, fmt.Errorf("%w: %v", ErrHarnessUpdate, err)
		}
		return Tools{}, err
	}
	m.Log.Info("harness updated", "sandbox", id, "harness", harness, "version", version)
	return withOrigins(t, m.templatePins(ctx, sb)), nil
}

// guestError tells a guest that doesn't know the request from other failures.
func guestError(err error) error {
	if err != nil && strings.Contains(err.Error(), "unknown stream kind") {
		return ErrGuestTooOld
	}
	return err
}

// templatePins are the tools the sandbox's template pins, by name, at the versions it asks
// for; none when the template can't be read.
func (m *Manager) templatePins(ctx context.Context, sb store.Sandbox) map[string]string {
	if sb.TemplateID == "" {
		return nil
	}
	tpl, err := m.Store.Template(ctx, sb.EnvironmentID, sb.TemplateID)
	if err != nil {
		return nil
	}
	spec, err := templatespec.ParseCanonicalJSON([]byte(tpl.Spec))
	if err != nil {
		return nil
	}
	pins := make(map[string]string, len(spec.Tools))
	for _, p := range spec.Tools {
		pins[p.Name] = p.Version
	}
	return pins
}

// withOrigins says where each of t's mise tools came from, given the template's pins.
func withOrigins(t agentproto.Tools, pins map[string]string) Tools {
	out := Tools{Harnesses: t.Harnesses, MiseError: t.MiseError, Mise: make([]Tool, 0, len(t.Mise))}
	if out.Harnesses == nil {
		out.Harnesses = []agentproto.HarnessVersion{}
	}
	for _, mt := range t.Mise {
		out.Mise = append(out.Mise, Tool{MiseTool: mt, Origin: origin(mt, pins)})
	}
	return out
}

func origin(t agentproto.MiseTool, pins map[string]string) string {
	switch {
	case t.Config == "":
		return ToolUnpinned
	case t.Config == keptConfig:
		return ToolKept
	case t.Config != globalConfig:
		return ToolProject
	}
	if v, ok := pins[t.Name]; ok && v == t.Requested {
		return ToolTemplate
	}
	return ToolAdded
}
