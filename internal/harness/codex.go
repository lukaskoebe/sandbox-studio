package harness

import (
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/personas"
)

// Codex is the adapter of the Codex CLI (@openai/codex on npm).
//
// Config: https://learn.chatgpt.com/docs/config-file/config-reference (formerly
// https://developers.openai.com/codex/config-reference; ~/.codex/config.toml, model_providers
// with base_url, env_key and wire_api, approval_policy, sandbox_mode, projects trust),
// instructions: https://developers.openai.com/codex/guides/agents-md (global
// ~/.codex/AGENTS.md), hooks: https://learn.chatgpt.com/docs/hooks (~/.codex/hooks.json,
// stdin fields, hookSpecificOutput.additionalContext on SessionStart and UserPromptSubmit,
// SessionEnd timeout at most 3 s, trust by hash), MCP: config reference
// (mcp_servers.<id>.command and args).
type Codex struct{}

func (Codex) Name() string { return personas.HarnessCodex }

// codexProvider is the model_providers ID Studio uses. The built-in IDs (openai, ollama,
// lmstudio) are reserved.
const codexProvider = "studio"

func (Codex) Render(p Persona, s Sandbox, provider Provider) ([]GuestFile, error) {
	var b strings.Builder
	b.WriteString("# Managed by Sandbox Studio: rewritten when a session starts.\n")
	m := model(p, provider)
	if m != "" {
		fmt.Fprintf(&b, "model = %s\n", tomlString(m))
	}
	// The VM is the sandbox: Codex neither asks nor sandboxes commands itself.
	b.WriteString("approval_policy = \"never\"\n")
	b.WriteString("sandbox_mode = \"danger-full-access\"\n")
	// The binary is installed by the image; the agent user can't update it.
	b.WriteString("check_for_update_on_startup = false\n")

	var baseURL string
	switch provider.Kind {
	case personas.KindOpenAIAPI:
		baseURL = "https://api.openai.com/v1"
	case personas.KindOpenAICompatible:
		if provider.BaseURL == "" || m == "" {
			return nil, fmt.Errorf("an openai_compatible provider needs a base URL and a model")
		}
		baseURL = provider.BaseURL
	case personas.KindChatGPTSubscription:
		// The built-in openai provider with the ChatGPT login (S8).
	default:
		return nil, fmt.Errorf("codex can't use a %s provider", provider.Kind)
	}
	if baseURL != "" {
		// An own provider rather than the built-in one: it reads the key from env_key, so
		// no `codex login` is needed. verify (S7): the endpoint must speak the Responses
		// API, the only wire_api Codex supports.
		fmt.Fprintf(&b, "model_provider = %s\n", tomlString(codexProvider))
		fmt.Fprintf(&b, "\n[model_providers.%s]\n", codexProvider)
		b.WriteString("name = \"Sandbox Studio\"\n")
		fmt.Fprintf(&b, "base_url = %s\n", tomlString(baseURL))
		fmt.Fprintf(&b, "env_key = %s\n", tomlString(provider.EnvVar))
		b.WriteString("wire_api = \"responses\"\n")
	}
	b.WriteString("\n[notice]\nhide_full_access_warning = true\n")
	b.WriteString("\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n")
	// Memory tools.
	fmt.Fprintf(&b, "\n[mcp_servers.%s]\ncommand = %s\nargs = [\"mcp\"]\n", mcpServerName, tomlString(AgentPath))

	hooks, err := marshalJSON(map[string]any{
		"description": "Managed by Sandbox Studio: rewritten when a session starts. Forwards to Studio's memory.",
		"hooks":       claudeStyleHooks(personas.HarnessCodex),
	})
	if err != nil {
		return nil, err
	}

	agents := instructions(p, s)
	agents.Path = ".codex/AGENTS.md"
	return []GuestFile{
		{Path: ".codex/config.toml", Content: b.String(), Mode: 0o644},
		{Path: ".codex/hooks.json", Content: hooks, Mode: 0o644},
		agents,
	}, nil
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TUICommand skips the hook trust review: user-layer hooks run only once trusted by hash,
// and the managed layer (requirements.toml) is outside the home. Studio rewrites hooks.json
// on every start and the VM is the sandbox. verify (S7): the flag is accepted by the
// interactive TUI of 0.162.1.
func (Codex) TUICommand(SessionOpts) []string {
	return []string{"codex", "--dangerously-bypass-hook-trust"}
}

// ParseHook reads Codex's hook stdin, which uses Claude Code's field names. verify (S7):
// the payloads of 0.162.1.
func (Codex) ParseHook(event string, stdin []byte) (HookEvent, error) {
	return parseClaudeStyle(personas.HarnessCodex, event, stdin)
}

func (Codex) RenderHookResponse(r HookResult) []byte { return renderClaudeStyle(r) }

// ACPCommand is nil: Codex speaks ACP through a separate adapter (M6).
func (Codex) ACPCommand() []string { return nil }

// StateDirs: sessions, history and auth are in CODEX_HOME, which defaults to ~/.codex.
func (Codex) StateDirs() []string { return []string{".codex"} }
