package harness

import (
	_ "embed"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// OpenCode is the adapter of OpenCode (opencode-ai on npm).
//
// Config: https://opencode.ai/docs/config/ (global config ~/.config/opencode/opencode.json),
// providers: https://opencode.ai/docs/providers/ (custom OpenAI-compatible providers via
// @ai-sdk/openai-compatible, "{env:VAR}" substitution), permissions:
// https://opencode.ai/docs/permissions/ ("permission": "allow"), rules:
// https://opencode.ai/docs/rules/ (global ~/.config/opencode/AGENTS.md), MCP:
// https://opencode.ai/docs/mcp-servers/ ("mcp" with type local), plugins:
// https://opencode.ai/docs/plugins/ (global ~/.config/opencode/plugins/).
type OpenCode struct{}

// opencodePlugin forwards OpenCode's events to `studio-agent hook`.
//
//go:embed opencode-plugin.js
var opencodePlugin string

func (OpenCode) Name() string { return teammates.HarnessOpenCode }

// compatibleID is the provider ID Studio gives an openai_compatible endpoint in OpenCode.
const compatibleID = "studio"

func (OpenCode) Render(p Teammate, s Sandbox, provider Provider) ([]GuestFile, error) {
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		// The VM is the sandbox: every tool runs without asking.
		"permission": "allow",
		// The binary is installed by the image; the agent user can't update it.
		"autoupdate": false,
		// Memory tools; the hooks are the plugin in plugins/.
		"mcp": map[string]any{mcpServerName: map[string]any{
			"type": "local", "command": []string{AgentPath, "mcp"}, "enabled": true,
		}},
	}
	apiKey := map[string]any{"apiKey": "{env:" + provider.EnvVar + "}"}
	m := model(p, provider)
	switch provider.Kind {
	case teammates.KindAnthropicAPI, teammates.KindOpenAIAPI:
		id := "anthropic"
		if provider.Kind == teammates.KindOpenAIAPI {
			id = "openai"
		}
		cfg["provider"] = map[string]any{id: map[string]any{"options": apiKey}}
		if m != "" {
			cfg["model"] = id + "/" + m
		}
	case teammates.KindOpenAICompatible:
		if provider.BaseURL == "" || m == "" {
			return nil, fmt.Errorf("an openai_compatible provider needs a base URL and a model")
		}
		cfg["provider"] = map[string]any{compatibleID: map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "Sandbox Studio",
			"options": map[string]any{
				"baseURL": provider.BaseURL,
				"apiKey":  "{env:" + provider.EnvVar + "}",
			},
			"models": map[string]any{m: compatibleModel(m, provider.Images)},
		}}
		cfg["model"] = compatibleID + "/" + m
	default:
		return nil, fmt.Errorf("opencode can't use a %s provider", provider.Kind)
	}
	b, err := marshalJSON(cfg)
	if err != nil {
		return nil, err
	}
	rules := instructions(p, s)
	rules.Path = ".config/opencode/AGENTS.md"
	return []GuestFile{
		{Path: ".config/opencode/opencode.json", Content: b, Mode: 0o644},
		{Path: ".config/opencode/plugins/studio-memory.js", Content: opencodePlugin, Mode: 0o644},
		rules,
	}, nil
}

// compatibleModel is the entry of an OpenAI-compatible endpoint's model. OpenCode takes a model
// it doesn't know for text only; one that takes images says so, so that OpenCode passes them on
// rather than replace them with an error for the model.
func compatibleModel(m string, images bool) map[string]any {
	entry := map[string]any{"name": m}
	if images {
		entry["attachment"] = true
		entry["modalities"] = map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}}
	}
	return entry
}

// ParseHook reads what Studio's plugin writes (opencode-plugin.js).
func (OpenCode) ParseHook(event string, stdin []byte) (HookEvent, error) {
	return hookFormat(teammates.HarnessOpenCode).Parse(event, stdin)
}

func (OpenCode) RenderHookResponse(r HookResult) []byte {
	return hookFormat(teammates.HarnessOpenCode).Render(r)
}

func (OpenCode) TUICommand(opts SessionOpts) []string {
	if opts.Resume != "" {
		return []string{"opencode", "--session", opts.Resume}
	}
	return []string{"opencode"}
}

// ACPCommand: https://opencode.ai/docs/acp/.
func (OpenCode) ACPCommand(Provider) []string { return []string{"opencode", "acp"} }

// ACPEnv is empty: `opencode acp` reads the same config as the TUI.
func (OpenCode) ACPEnv(Provider) map[string]string { return nil }

// ACPMCPServers is empty: OpenCode reads its mcp section from opencode.json.
func (OpenCode) ACPMCPServers() []acp.MCPServer { return nil }

// StateDirs: sessions, auth and logs are under the XDG data dir, the TUI's state under the
// XDG state dir.
func (OpenCode) StateDirs() []string { return hookFormat(teammates.HarnessOpenCode).StateDirs }
