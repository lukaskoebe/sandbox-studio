package policy

import "github.com/lukaskoebe/sandbox-studio/internal/store"

// systemHosts are the hosts each system sandbox kind may reach (store/system.go): what a login
// or a one-shot CLI call needs and nothing else. Nobody is asked about these hosts, and the
// VMs get no other network. verify (S8): the hosts the Claude Code login and the Codex ChatGPT
// login call, which the live check of the owner's accounts confirms.
var systemHosts = map[string][]string{
	store.SandboxKindClaudeLogin:   {"console.anthropic.com", "api.anthropic.com"},
	store.SandboxKindCodexLogin:    {"auth.openai.com"},
	store.SandboxKindClaudeUtility: {"api.anthropic.com"},
	store.SandboxKindCodexUtility:  {"chatgpt.com"},
}

// SystemHosts returns the hosts a sandbox of kind may reach without asking, or nil for an
// ordinary sandbox. Pass it as Request.Allowed.
func SystemHosts(kind string) []string { return systemHosts[kind] }
