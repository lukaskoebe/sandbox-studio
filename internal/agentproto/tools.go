package agentproto

import "regexp"

// KindTools (host → guest) asks what the sandbox has installed: no request line, and the guest
// replies with a Tools line, or an Error. A guest older than v0.30.0 replies with an Error.
const KindTools = "tools"

// KindHarnessUpdate (host → guest): the host writes a HarnessUpdate line; the guest installs
// that version of the harness's CLI from npm, as root, over the one in /usr/local, and replies
// with the Tools it then has, or an Error. Only the harnesses in HarnessPackages update.
const KindHarnessUpdate = "harness-update"

// Limits of a tools reply.
const (
	MaxMiseTools    = 500
	MaxToolsLine    = 1 << 20
	MaxUpdateOutput = 4 << 10 // the end of npm's output an Error carries
)

// HarnessPackages are the npm packages of the harnesses the base image installs, by harness.
var HarnessPackages = map[string]string{
	"claude":   "@anthropic-ai/claude-code",
	"codex":    "@openai/codex",
	"opencode": "opencode-ai",
}

// UpdatableHarnesses are the harnesses KindHarnessUpdate installs. Codex and OpenCode stay as
// the base image has them: their chat adapters are pinned to them (docs/harnesses.md).
var UpdatableHarnesses = []string{"claude"}

// HarnessVersionPattern is a version KindHarnessUpdate installs: an exact npm version, not a
// range or a tag.
var HarnessVersionPattern = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(-[0-9A-Za-z.]{1,40})?$`)

// Tools is what a sandbox has installed: the harnesses' CLIs and what mise manages.
type Tools struct {
	Harnesses []HarnessVersion `json:"harnesses"`
	Mise      []MiseTool       `json:"mise"`
	// MiseError says why mise didn't list its tools; the harnesses are still listed.
	MiseError string `json:"miseError,omitempty"`
}

// HarnessVersion is the version of a harness's CLI in /usr/local; a harness that isn't
// installed is left out.
type HarnessVersion struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
}

// MiseTool is one version of a tool mise has, as `mise ls` lists it.
type MiseTool struct {
	Name    string `json:"name"`    // "node", "npm:prettier"
	Version string `json:"version"` // "24.11.0"
	// Requested is what a config asks for, when it pins the tool: "24", "latest".
	Requested string `json:"requested,omitempty"`
	// Config is the config file that pins it: /opt/mise/config/config.toml for the
	// template's and `mise use -g`'s, conf.d/studio-kept-tools.toml for those a rebase kept.
	Config    string `json:"config,omitempty"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
}

// HarnessUpdate is the body of a KindHarnessUpdate stream.
type HarnessUpdate struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
}
