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

// buildHosts are the hosts a template build's own steps reach (internal/templatebuild): the
// base image's apt sources, and what mise and the Nix installer download for the tools a spec
// may pin. A build VM reaches them on ports 80 and 443 without anyone being asked, at every
// security level; anything else it connects to, such as the hosts of a spec's setup script, is
// asked as for any sandbox. Explicit deny rules still win.
var buildHosts = []string{
	// apt: the Debian archives and the base image's Docker repository.
	"deb.debian.org", "security.debian.org", "download.docker.com",
	// mise: its version lists, Node.js, Go, and the precompiled Python builds on GitHub.
	"mise-versions.jdx.dev", "nodejs.org", "go.dev", "dl.google.com",
	"github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com",
	// The pinned Nix release.
	"releases.nixos.org",
}

// BuildHosts returns the hosts a sandbox may reach without asking because it runs a template
// build, or nil for any other sandbox. Pass it as Request.Preapproved.
func BuildHosts(sb store.Sandbox) []string {
	if sb.BuildJobID == "" {
		return nil
	}
	return buildHosts
}

// SystemHosts returns the hosts a sandbox of kind may reach without asking, or nil for an
// ordinary sandbox. Pass it as Request.Allowed.
func SystemHosts(kind string) []string { return systemHosts[kind] }
