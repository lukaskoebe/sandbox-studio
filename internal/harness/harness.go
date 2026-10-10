// Package harness renders the guest-side configuration of the agent harnesses Studio runs:
// OpenCode, Claude Code and Codex (PLAN.md §6.6, docs/harnesses.md).
//
// An adapter turns a persona and its provider into files in the agent user's home and the
// command that starts the harness's TUI. It never sees a secret: the provider's credential
// reaches the guest only as its placeholder in an environment variable, which the gateway
// swaps for the real key on the way to the provider's host.
//
// Every file lives in the agent user's home (/home/agent), which is on the sandbox's root
// disk: it survives stop and start and is captured by checkpoints, but a rebase or fork
// starts from a fresh root disk and keeps only /workspace. So the harnesses' sessions and
// history (StateDirs) survive stop/start but not a rebase; the config is rewritten on every
// session start anyway.
package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/personas"
)

// Persona is what an adapter needs to know about a persona.
type Persona struct {
	Name     string
	Role     string
	Soul     string // markdown
	Model    string // overrides Provider.Model when set
	GitName  string
	GitEmail string
}

// Provider is what an adapter needs to know about a persona's provider. It has no field
// for a credential on purpose: only the placeholder ever reaches the guest.
type Provider struct {
	Kind        string // personas.Kind*
	BaseURL     string // openai_compatible only
	Model       string // the provider's default model
	EnvVar      string // where the harness reads its credential from
	Placeholder string // the placeholder of the provider's secret
}

// Sandbox is what an adapter needs to know about the sandbox a session runs in.
type Sandbox struct {
	Name string
}

// GuestFile is a file in the agent user's home; Path is relative to it.
type GuestFile = agentproto.HomeFile

// SessionOpts are the options of one TUI session.
type SessionOpts struct {
	Workdir string
}

// Harness is an adapter for one agent harness (PLAN.md §6.6).
type Harness interface {
	Name() string
	// Render returns the harness's config files: the managed instructions block, the
	// provider and model, the auto-approve mode, the hooks that call `studio-agent hook`
	// and the registration of `studio-agent mcp`.
	Render(p Persona, s Sandbox, provider Provider) ([]GuestFile, error)
	// TUICommand is the command line of the interactive TUI, run in a login shell.
	TUICommand(opts SessionOpts) []string
	// ACPCommand is the command line of the harness's ACP agent, or nil if it has none
	// built in (the API surface is M6).
	ACPCommand() []string
	// StateDirs are the directories, relative to the home, where the harness keeps its
	// sessions and history.
	StateDirs() []string
	// ParseHook turns the harness's hook stdin for one of HookEvents into a HookEvent. It
	// runs in the guest, in `studio-agent hook`.
	ParseHook(event string, stdin []byte) (HookEvent, error)
	// RenderHookResponse is the hook's stdout in the harness's format.
	RenderHookResponse(HookResult) []byte
}

var all = []Harness{OpenCode{}, Claude{}, Codex{}}

// Lookup returns the adapter of a harness by its name (personas.Harness*).
func Lookup(name string) (Harness, bool) {
	for _, h := range all {
		if h.Name() == name {
			return h, true
		}
	}
	return nil, false
}

// Env is the environment a harness session gets: the provider's credential variable set
// to the placeholder. Subscription providers without a placeholder get nothing.
func Env(provider Provider) map[string]string {
	env := map[string]string{}
	if provider.EnvVar != "" && provider.Placeholder != "" {
		env[provider.EnvVar] = provider.Placeholder
	}
	return env
}

// Files are all files a session of h for p writes: the harness's own and the persona's
// git identity.
func Files(h Harness, p Persona, s Sandbox, provider Provider) ([]GuestFile, error) {
	if !personas.Supports(provider.Kind, h.Name()) {
		return nil, fmt.Errorf("%s can't use a %s provider", h.Name(), provider.Kind)
	}
	files, err := h.Render(p, s, provider)
	if err != nil {
		return nil, err
	}
	if p.GitName != "" && p.GitEmail != "" {
		files = append(files, GitConfig(p))
	}
	return files, nil
}

// model is the model a session uses: the persona's, else the provider's. It is empty for
// subscription providers without either, and then the harness picks its default.
func model(p Persona, provider Provider) string {
	if p.Model != "" {
		return p.Model
	}
	return provider.Model
}

// GitConfig is the persona's git identity as the agent user's global git config. It uses
// the XDG location, which git reads before ~/.gitconfig, so a ~/.gitconfig the user writes
// in the sandbox still wins. There are no credentials in it: pushes go through the
// gateway's placeholders like any other request.
func GitConfig(p Persona) GuestFile {
	// personas.CheckGitName and CheckGitEmail refuse quotes, backslashes and newlines.
	content := fmt.Sprintf("# Managed by Sandbox Studio: rewritten when a session starts.\n[user]\n\tname = %q\n\temail = %s\n",
		p.GitName, p.GitEmail)
	return GuestFile{Path: ".config/git/config", Content: content, Mode: 0o644}
}

// instructions is the managed block: who the persona is and how its memory works. It goes
// into the harness's global instructions file in the home, not into /workspace, so it never
// ends up in the project's repository. The soul and the persona's memory arrive as context
// at session start (PLAN §6.6), so the block stays small and the same across sessions.
func instructions(p Persona, s Sandbox) GuestFile {
	var b strings.Builder
	b.WriteString(agentproto.BlockBegin + "\n")
	b.WriteString("<!-- Managed by Sandbox Studio: this block is rewritten when a session starts. Edit the persona in Studio instead; text outside the block is kept. -->\n\n")
	fmt.Fprintf(&b, "# You are %s\n\n", p.Name)
	if p.Role != "" {
		fmt.Fprintf(&b, "Role: %s\n\n", p.Role)
	}
	fmt.Fprintf(&b, "You work in the Sandbox Studio sandbox %q. The project is in /workspace. "+
		"Network access goes through Studio's gateway and its policy. API keys in your environment are "+
		"placeholders that only work through the gateway; there is no need to look for real ones.\n", s.Name)
	b.WriteString(memoryInstructions)
	b.WriteString(agentproto.BlockEnd + "\n")
	return GuestFile{Content: defuseMarkers(b.String()), Mode: 0o644, Block: true}
}

// memoryInstructions tells the agent how Studio's memory works and when to use its tools.
const memoryInstructions = `
## Memory

Studio remembers for you across sessions and sandboxes. At session start you get your soul, your core memory and what changed in shared memory; with each prompt, memories relevant to it. When a session ends or compacts, Studio extracts lasting facts from the conversation on its own. The ` + "`studio`" + ` MCP server has tools for more:

- ` + "`memory_search`" + ` and ` + "`memory_get`" + `: look things up when the injected memories aren't enough.
- ` + "`remember`" + `: save something right away. Use it for preferences the user states (source ` + "`user`" + `) and for procedures you have verified by running them (source ` + "`verified`" + `, with the command and result as evidence).
- ` + "`correct`" + ` and ` + "`forget`" + `: fix or retract one of your own facts when it turns out wrong.
- ` + "`share`" + `: propose a fact for the shared memory of every persona; the user approves it first.

Memory marked disputed is part of an open conflict: prefer the shared fact and check before relying on it.
`

// defuseMarkers keeps text from the persona from closing or opening the managed block:
// only the outer markers stay intact.
func defuseMarkers(block string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(block, agentproto.BlockBegin), agentproto.BlockEnd+"\n")
	inner = strings.ReplaceAll(inner, "<!-- sandbox-studio:", "<!-- sandbox studio:")
	return agentproto.BlockBegin + inner + agentproto.BlockEnd + "\n"
}

// marshalJSON is v as indented JSON without HTML escaping, which harnesses don't need.
func marshalJSON(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return b.String(), err
}
