package harness

import (
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/personas"
)

// OpenCode is the adapter of OpenCode (opencode-ai on npm).
//
// Config: https://opencode.ai/docs/config/ (global config ~/.config/opencode/opencode.json),
// providers: https://opencode.ai/docs/providers/ (custom OpenAI-compatible providers via
// @ai-sdk/openai-compatible, "{env:VAR}" substitution), permissions:
// https://opencode.ai/docs/permissions/ ("permission": "allow"), rules:
// https://opencode.ai/docs/rules/ (global ~/.config/opencode/AGENTS.md).
type OpenCode struct{ noHooks }

func (OpenCode) Name() string { return personas.HarnessOpenCode }

// compatibleID is the provider ID Studio gives an openai_compatible endpoint in OpenCode.
const compatibleID = "studio"

func (OpenCode) Render(p Persona, s Sandbox, provider Provider) ([]GuestFile, error) {
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		// The VM is the sandbox: every tool runs without asking.
		"permission": "allow",
		// The binary is installed by the image; the agent user can't update it.
		"autoupdate": false,
		// TODO(M5): "mcp" servers and the plugin that forwards hooks.
	}
	apiKey := map[string]any{"apiKey": "{env:" + provider.EnvVar + "}"}
	m := model(p, provider)
	switch provider.Kind {
	case personas.KindAnthropicAPI, personas.KindOpenAIAPI:
		id := "anthropic"
		if provider.Kind == personas.KindOpenAIAPI {
			id = "openai"
		}
		cfg["provider"] = map[string]any{id: map[string]any{"options": apiKey}}
		if m != "" {
			cfg["model"] = id + "/" + m
		}
	case personas.KindOpenAICompatible:
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
			"models": map[string]any{m: map[string]any{"name": m}},
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
		rules,
	}, nil
}

func (OpenCode) TUICommand(SessionOpts) []string { return []string{"opencode"} }

// ACPCommand: https://opencode.ai/docs/acp/.
func (OpenCode) ACPCommand() []string { return []string{"opencode", "acp"} }

// StateDirs: sessions, auth and logs are under the XDG data dir, the TUI's state under the
// XDG state dir.
func (OpenCode) StateDirs() []string {
	return []string{".local/share/opencode", ".local/state/opencode"}
}
