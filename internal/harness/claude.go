package harness

import (
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// Claude is the adapter of Claude Code (@anthropic-ai/claude-code on npm).
//
// Settings: https://code.claude.com/docs/en/settings (user settings ~/.claude/settings.json,
// user memory ~/.claude/CLAUDE.md), permission modes:
// https://code.claude.com/docs/en/settings-reference (permissions.defaultMode
// "bypassPermissions" and skipDangerousModePermissionPrompt are honored in user settings),
// environment variables: https://code.claude.com/docs/en/env-vars (ANTHROPIC_API_KEY,
// CLAUDE_CODE_OAUTH_TOKEN, DISABLE_AUTOUPDATER), hooks:
// https://code.claude.com/docs/en/hooks (settings "hooks", stdin fields, hookSpecificOutput),
// MCP: https://code.claude.com/docs/en/mcp (--mcp-config with an "mcpServers" file).
type Claude struct{}

// claudeMCPConfig is the MCP config file the TUI loads with --mcp-config. User-scope MCP
// servers live in ~/.claude.json, which Claude Code owns, so Studio passes its own file.
const claudeMCPConfig = ".claude/studio-mcp.json"

func (Claude) Name() string { return teammates.HarnessClaude }

func (Claude) Render(p Teammate, s Sandbox, provider Provider) ([]GuestFile, error) {
	switch provider.Kind {
	case teammates.KindAnthropicAPI, teammates.KindClaudeSubscription:
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
		// Memory: every hook forwards to Studio through the guest agent.
		"hooks": claudeStyleHooks(teammates.HarnessClaude),
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
	mcp, err := marshalJSON(map[string]any{"mcpServers": map[string]any{
		mcpServerName: map[string]any{"type": "stdio", "command": AgentPath, "args": []string{"mcp"}},
	}})
	if err != nil {
		return nil, err
	}
	memory := instructions(p, s)
	memory.Path = ".claude/CLAUDE.md"
	return []GuestFile{
		{Path: ".claude/settings.json", Content: b, Mode: 0o644},
		{Path: claudeMCPConfig, Content: mcp, Mode: 0o644},
		memory,
	}, nil
}

// TUICommand loads Studio's MCP server from its own file. verify (S7): servers passed with
// --mcp-config need no approval prompt, unlike a project's .mcp.json.
func (Claude) TUICommand(opts SessionOpts) []string {
	if opts.Resume != "" {
		return []string{"claude", "--resume", opts.Resume, "--mcp-config", Home + "/" + claudeMCPConfig}
	}
	return []string{"claude", "--mcp-config", Home + "/" + claudeMCPConfig}
}

// ParseHook reads Claude Code's hook stdin. verify (S7): the field names and that
// SessionStart fires with source "compact" after compaction, against 2.1.287.
func (Claude) ParseHook(event string, stdin []byte) (HookEvent, error) {
	return hookFormat(teammates.HarnessClaude).Parse(event, stdin)
}

func (Claude) RenderHookResponse(r HookResult) []byte {
	return hookFormat(teammates.HarnessClaude).Render(r)
}

// ACPCommand is the Claude Agent adapter (@agentclientprotocol/claude-agent-acp), which
// runs the Claude Code binary through the Claude Agent SDK. It reads the user settings, so
// the managed settings.json (model, bypassPermissions, hooks) applies to chats too.
func (Claude) ACPCommand() []string { return []string{"claude-agent-acp"} }

// ACPEnv points the SDK at the image's Claude Code: the adapter is installed without the
// SDK's own copy of the binary, so chat and TUI run the same pinned version.
func (Claude) ACPEnv(Provider) map[string]string {
	return map[string]string{"CLAUDE_CODE_EXECUTABLE": "/usr/local/bin/claude"}
}

// ACPMCPServers: the TUI gets Studio's MCP server from --mcp-config, which the adapter has
// no way to pass, so a chat session gets it in session/new.
func (Claude) ACPMCPServers() []acp.MCPServer {
	return []acp.MCPServer{{Name: mcpServerName, Command: AgentPath, Args: []string{"mcp"}, Env: []acp.EnvVariable{}}}
}

// StateDirs: sessions and history are in ~/.claude (projects/, history.jsonl); account
// and onboarding state is in the file ~/.claude.json next to it.
func (Claude) StateDirs() []string { return hookFormat(teammates.HarnessClaude).StateDirs }
