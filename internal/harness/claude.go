package harness

import (
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/personas"
)

// Claude is the adapter of Claude Code (@anthropic-ai/claude-code on npm).
//
// Settings: https://code.claude.com/docs/en/settings (user settings ~/.claude/settings.json,
// user memory ~/.claude/CLAUDE.md), permission modes:
// https://code.claude.com/docs/en/settings-reference (permissions.defaultMode
// "bypassPermissions" and skipDangerousModePermissionPrompt are honored in user settings),
// environment variables: https://code.claude.com/docs/en/env-vars (ANTHROPIC_API_KEY,
// CLAUDE_CODE_OAUTH_TOKEN, DISABLE_AUTOUPDATER).
type Claude struct{ noHooks }

func (Claude) Name() string { return personas.HarnessClaude }

func (Claude) Render(p Persona, s Sandbox, provider Provider) ([]GuestFile, error) {
	switch provider.Kind {
	case personas.KindAnthropicAPI, personas.KindClaudeSubscription:
	default:
		return nil, fmt.Errorf("claude can't use a %s provider", provider.Kind)
	}
	settings := map[string]any{
		"$schema": "https://json.schemastore.org/claude-code-settings.json",
		// The VM is the sandbox: every tool runs without asking.
		"permissions":                       map[string]any{"defaultMode": "bypassPermissions"},
		"skipDangerousModePermissionPrompt": true,
		// The binary is installed by the image; the agent user can't update it.
		"env": map[string]any{"DISABLE_AUTOUPDATER": "1"},
		// TODO(M5): "hooks" that forward to the guest agent.
	}
	// verify (S7): the first start with ANTHROPIC_API_KEY set may ask once whether to use
	// the key, and a fresh home shows the onboarding screens. Both are answered in the TUI
	// and remembered in ~/.claude.json, which Claude Code owns and Studio doesn't write.
	if m := model(p, provider); m != "" {
		settings["model"] = m
	}
	b, err := marshalJSON(settings)
	if err != nil {
		return nil, err
	}
	memory := instructions(p, s)
	memory.Path = ".claude/CLAUDE.md"
	return []GuestFile{
		{Path: ".claude/settings.json", Content: b, Mode: 0o644},
		memory,
	}, nil
}

func (Claude) TUICommand(SessionOpts) []string { return []string{"claude"} }

// ACPCommand is nil: Claude Code speaks ACP through a separate adapter (M6).
func (Claude) ACPCommand() []string { return nil }

// StateDirs: sessions and history are in ~/.claude (projects/, history.jsonl); account
// and onboarding state is in the file ~/.claude.json next to it.
func (Claude) StateDirs() []string { return []string{".claude"} }
