// Package templatebuild plans the guest commands used to build templates.
package templatebuild

import (
	"sort"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

const (
	rootHome         = "/root"
	agentHome        = "/home/agent"
	workspaceDir     = "/workspace"
	studioHook       = "/etc/profile.d/sandbox-studio-env.sh"
	systemCABundle   = "/etc/ssl/certs/ca-certificates.crt"
	miseCacheDir     = "/tmp/studio-template-mise-cache"
	miseDownloadsDir = "/tmp/studio-template-mise-downloads"
	miseStateDir     = "/tmp/studio-template-mise-state"
	// miseDir holds the template's toolchains and mise config. The base image creates it,
	// owned by agent, and its /etc/profile.d/studio-mise.sh points login shells at it. It
	// is on the root layer: sandboxes mount their home disk over /home/agent.
	miseDir = "/opt/mise"
	// homeSkelDir is the skeleton a sandbox's home disk is seeded from (guest.HomeSkel).
	homeSkelDir = "/usr/share/sandbox-studio/home-skel"

	rootPath  = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	agentPath = miseDir + "/shims:/home/agent/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

	aptTimeout   = 15 * time.Minute
	miseTimeout  = 30 * time.Minute
	setupTimeout = 15 * time.Minute
	homeTimeout  = 15 * time.Minute
)

// Stage is one bounded command in a template build.
type Stage struct {
	Name    string
	Command runtime.RunCommand
}

// Commands validates spec and returns the ordered guest command plan. User data is kept in
// argv: package names and tool pins are separate arguments, and setup is one positional
// argument to a fixed Bash wrapper.
func Commands(spec templatespec.Spec) ([]Stage, error) {
	if _, err := templatespec.CanonicalJSON(spec); err != nil {
		return nil, err
	}

	packages := append([]string(nil), spec.Apt...)
	sort.Strings(packages)
	toolPins := append([]templatespec.ToolPin(nil), spec.Tools...)
	sort.Slice(toolPins, func(i, j int) bool { return toolPins[i].Name < toolPins[j].Name })
	tools := make([]string, 0, len(toolPins))
	for _, tool := range toolPins {
		tools = append(tools, tool.Name+"@"+tool.Version)
	}

	return []Stage{
		{
			Name: "apt",
			Command: runtime.RunCommand{
				Path:    "/bin/bash",
				Args:    bashArgs(rootAptScript, "studio-template-apt", packages),
				User:    "root",
				Cwd:     "/",
				Env:     rootEnvironment(),
				Timeout: aptTimeout,
			},
		},
		{
			Name: "mise",
			Command: runtime.RunCommand{
				Path:    "/bin/bash",
				Args:    bashArgs(miseScript, "studio-template-mise", tools),
				User:    "agent",
				Cwd:     agentHome,
				Env:     agentEnvironment(),
				Timeout: miseTimeout,
			},
		},
		{
			Name: "setup",
			Command: runtime.RunCommand{
				Path:    "/bin/bash",
				Args:    []string{"--login", "-c", setupScript, "studio-template-setup", spec.Setup},
				User:    "agent",
				Cwd:     agentHome,
				Env:     agentEnvironment(),
				Timeout: setupTimeout,
			},
		},
		{
			Name: "home",
			Command: runtime.RunCommand{
				Path:    "/bin/bash",
				Args:    bashArgs(homeScript, "studio-template-home", []string{homeSkelDir}),
				User:    "root",
				Cwd:     "/",
				Env:     rootEnvironment(),
				Timeout: homeTimeout,
			},
		},
	}, nil
}

func bashArgs(script, commandName string, args []string) []string {
	result := []string{"--noprofile", "--norc", "-c", script, commandName}
	return append(result, args...)
}

func rootEnvironment() map[string]string {
	env := trustedEnvironment()
	env["HOME"] = rootHome
	env["USER"] = "root"
	env["LOGNAME"] = "root"
	env["SHELL"] = "/bin/bash"
	env["PATH"] = rootPath
	env["DEBIAN_FRONTEND"] = "noninteractive"
	return env
}

func agentEnvironment() map[string]string {
	env := trustedEnvironment()
	env["HOME"] = agentHome
	env["USER"] = "agent"
	env["LOGNAME"] = "agent"
	env["SHELL"] = "/bin/bash"
	env["PATH"] = agentPath
	env["MISE_DATA_DIR"] = miseDir
	env["MISE_CONFIG_DIR"] = miseDir + "/config"
	env["MISE_CACHE_DIR"] = miseCacheDir
	env["MISE_DOWNLOADS_DIR"] = miseDownloadsDir
	env["MISE_STATE_DIR"] = miseStateDir
	env["MISE_INSTALLS_DIR"] = miseDir + "/installs"
	env["MISE_SHIMS_DIR"] = miseDir + "/shims"
	env["MISE_YES"] = "1"
	return env
}

func trustedEnvironment() map[string]string {
	return map[string]string{
		"SSL_CERT_FILE":       systemCABundle,
		"CURL_CA_BUNDLE":      systemCABundle,
		"REQUESTS_CA_BUNDLE":  systemCABundle,
		"GIT_SSL_CAINFO":      systemCABundle,
		"NODE_EXTRA_CA_CERTS": systemCABundle,
		"PIP_CERT":            systemCABundle,
	}
}

// fixedWrapper sources the Studio placeholders before a stage and then restores every
// controlled value. A placeholder may legally be named MISE_*; clearing those names before
// exporting the command plan prevents one from redirecting mise's config or installs.
func fixedWrapper(env map[string]string, body string) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("if [ -r ")
	script.WriteString(shellLiteral(studioHook))
	script.WriteString(" ]; then\n    . ")
	script.WriteString(shellLiteral(studioHook))
	script.WriteString("\nfi\n")
	script.WriteString("for name in ${!MISE_@}; do unset \"$name\"; done\n")
	script.WriteString(environmentExports(env))
	script.WriteString(body)
	return script.String()
}

func environmentExports(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var script strings.Builder
	for _, key := range keys {
		script.WriteString("export ")
		script.WriteString(key)
		script.WriteByte('=')
		script.WriteString(shellLiteral(env[key]))
		script.WriteByte('\n')
	}
	return script.String()
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

const rootAptBody = `if [ "$#" -eq 0 ]; then
    exit 0
fi
apt-get update
apt-get install -y --no-install-recommends "$@"
apt-get clean
rm -rf /var/lib/apt/lists/*
`

// miseBody installs the pins into miseDir. Login shells find them through the base
// image's profile script, not through the home, which a sandbox's home disk replaces.
const miseBody = `if [ ! -d "$MISE_DATA_DIR" ] || [ ! -w "$MISE_DATA_DIR" ]; then
    echo "$MISE_DATA_DIR is missing or not writable: the base image predates it, rebuild the base image" >&2
    exit 1
fi
mkdir -p "$MISE_CONFIG_DIR" "$MISE_DATA_DIR" "$MISE_CACHE_DIR" "$MISE_DOWNLOADS_DIR" "$MISE_STATE_DIR" "$MISE_INSTALLS_DIR" "$MISE_SHIMS_DIR"
if [ "$#" -gt 0 ]; then
    /usr/local/bin/mise use --global "$@"
    /usr/local/bin/mise reshim
fi
`

const setupBody = `exec /bin/bash --noprofile --norc -c "$1" studio-template-user-setup
`

// homeBody replaces the skeleton with the agent's home as setup left it. It runs last, so
// the skeleton holds everything the template wrote there.
const homeBody = `skel="$1"
rm -rf "$skel"
mkdir -p "$(dirname "$skel")"
cp -a /home/agent "$skel"
chown -Rh agent:agent "$skel"
`

var (
	rootAptScript = fixedWrapper(rootEnvironment(), rootAptBody)
	miseScript    = fixedWrapper(agentEnvironment(), miseBody)
	setupScript   = fixedWrapper(agentEnvironment(), setupBody)
	homeScript    = fixedWrapper(rootEnvironment(), homeBody)
)
