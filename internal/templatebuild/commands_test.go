package templatebuild

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

func TestCommandsPlansRootAptThenAgentMiseAndSetup(t *testing.T) {
	setup := "printf '%s' 'literal'; touch /tmp/from-user-setup"
	spec := templatespec.Spec{
		Resources: resources.Defaults(),
		Tools: []templatespec.ToolPin{
			{Name: "python", Version: "3.13"},
			{Name: "go", Version: "1.25"},
			{Name: "node", Version: "22"},
		},
		Apt:   []string{"postgresql-client", "ca-certificates"},
		Setup: setup,
	}

	stages, err := Commands(spec)
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if len(stages) != 3 {
		t.Fatalf("stage count = %d, want 3", len(stages))
	}
	if got := []string{stages[0].Name, stages[1].Name, stages[2].Name}; !reflect.DeepEqual(got, []string{"apt", "mise", "setup"}) {
		t.Fatalf("stage order = %v", got)
	}

	apt := stages[0].Command
	if apt.Path != "/bin/bash" || apt.User != "root" || apt.Cwd != "/" || apt.Timeout != aptTimeout {
		t.Fatalf("apt command = %+v", apt)
	}
	if !reflect.DeepEqual(apt.Args[:5], []string{"--noprofile", "--norc", "-c", rootAptScript, "studio-template-apt"}) {
		t.Fatalf("apt wrapper argv = %#v", apt.Args)
	}
	if got := apt.Args[5:]; !reflect.DeepEqual(got, []string{"ca-certificates", "postgresql-client"}) {
		t.Fatalf("apt package argv = %v", got)
	}

	mise := stages[1].Command
	if mise.Path != "/bin/bash" || mise.User != "agent" || mise.Cwd != agentHome || mise.Timeout != miseTimeout {
		t.Fatalf("mise command = %+v", mise)
	}
	if !reflect.DeepEqual(mise.Args[:5], []string{"--noprofile", "--norc", "-c", miseScript, "studio-template-mise"}) {
		t.Fatalf("mise wrapper argv = %#v", mise.Args)
	}
	if got := mise.Args[5:]; !reflect.DeepEqual(got, []string{"go@1.25", "node@22", "python@3.13"}) {
		t.Fatalf("mise pin argv = %v", got)
	}

	userSetup := stages[2].Command
	if userSetup.Path != "/bin/bash" || userSetup.User != "agent" || userSetup.Cwd != agentHome || userSetup.Timeout != setupTimeout {
		t.Fatalf("setup command = %+v", userSetup)
	}
	if !reflect.DeepEqual(userSetup.Args[:4], []string{"--login", "-c", setupScript, "studio-template-setup"}) {
		t.Fatalf("setup wrapper argv = %#v", userSetup.Args)
	}
	if len(userSetup.Args) != 5 || userSetup.Args[4] != setup {
		t.Fatalf("setup must be one positional argument, got argv %#v", userSetup.Args)
	}
	if strings.Contains(userSetup.Args[2], setup) {
		t.Fatal("user setup was interpolated into the fixed Bash wrapper")
	}
}

func TestCommandsSetExplicitEnvironmentAndHomePaths(t *testing.T) {
	stages, err := Commands(templatespec.Spec{Resources: resources.Defaults()})
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}

	for _, tc := range []struct {
		name string
		env  map[string]string
		home string
		user string
		path string
	}{
		{name: "root", env: stages[0].Command.Env, home: rootHome, user: "root", path: rootPath},
		{name: "agent", env: stages[1].Command.Env, home: agentHome, user: "agent", path: agentPath},
		{name: "setup agent", env: stages[2].Command.Env, home: agentHome, user: "agent", path: agentPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, want := range map[string]string{
				"HOME": tc.home, "USER": tc.user, "LOGNAME": tc.user,
				"SHELL": "/bin/bash", "PATH": tc.path,
				"SSL_CERT_FILE": systemCABundle, "CURL_CA_BUNDLE": systemCABundle,
				"REQUESTS_CA_BUNDLE": systemCABundle, "GIT_SSL_CAINFO": systemCABundle,
				"NODE_EXTRA_CA_CERTS": systemCABundle, "PIP_CERT": systemCABundle,
			} {
				if got := tc.env[key]; got != want {
					t.Errorf("env[%q] = %q, want %q", key, got, want)
				}
			}
			for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "API_KEY"} {
				if _, exists := tc.env[key]; exists {
					t.Errorf("credential-like env %q was included in the command", key)
				}
			}
		})
	}

	for _, command := range []struct {
		name           string
		commandTimeout time.Duration
	}{
		{name: "apt", commandTimeout: stages[0].Command.Timeout},
		{name: "mise", commandTimeout: stages[1].Command.Timeout},
		{name: "setup", commandTimeout: stages[2].Command.Timeout},
	} {
		if command.commandTimeout <= 0 || command.commandTimeout > 30*time.Minute {
			t.Errorf("%s timeout = %s, want a positive bound of at most 30 minutes", command.name, command.commandTimeout)
		}
	}

	if stages[1].Command.Env["MISE_CONFIG_DIR"] != agentHome+"/.config/mise" ||
		stages[1].Command.Env["MISE_DATA_DIR"] != agentHome+"/.local/share/mise" ||
		stages[1].Command.Env["MISE_INSTALLS_DIR"] != agentHome+"/.local/share/mise/installs" ||
		stages[1].Command.Env["MISE_SHIMS_DIR"] != agentHome+"/.local/share/mise/shims" ||
		stages[1].Command.Env["MISE_CACHE_DIR"] != miseCacheDir ||
		stages[1].Command.Env["MISE_DOWNLOADS_DIR"] != miseDownloadsDir ||
		stages[1].Command.Env["MISE_STATE_DIR"] != miseStateDir {
		t.Fatalf("mise install/config and transient paths are misplaced: %#v", stages[1].Command.Env)
	}
	if strings.Contains(miseScript, workspaceDir) || !strings.Contains(miseScript, `"$HOME/.profile"`) ||
		!strings.Contains(miseScript, `"$HOME/.bash_profile"`) || !strings.Contains(miseScript, `"$HOME/.bash_login"`) {
		t.Fatal("mise config, toolchains, and login profile must be placed under the agent home")
	}
	hookIndex := strings.Index(setupScript, ". "+shellLiteral(studioHook))
	pathIndex := strings.LastIndex(setupScript, "export PATH="+shellLiteral(agentPath))
	execIndex := strings.Index(setupScript, `exec /bin/bash --noprofile --norc -c "$1"`)
	if hookIndex < 0 || pathIndex < hookIndex || execIndex < pathIndex {
		t.Fatal("setup wrapper must source the fixed Studio hook, restore PATH, then execute user code")
	}
	for _, tc := range []struct {
		name   string
		script string
		before string
	}{
		{name: "apt", script: rootAptScript, before: "apt-get update"},
		{name: "mise", script: miseScript, before: "/usr/local/bin/mise use --global"},
	} {
		hook := strings.Index(tc.script, ". "+shellLiteral(studioHook))
		clear := strings.Index(tc.script, "for name in ${!MISE_@}; do unset")
		export := strings.Index(tc.script, "export HOME=")
		network := strings.Index(tc.script, tc.before)
		if hook < 0 || clear < hook || export < clear || network < export {
			t.Errorf("%s stage must source the Studio hook and restore its controlled environment before network work", tc.name)
		}
	}
	useIndex := strings.Index(miseScript, `/usr/local/bin/mise use --global "$@"`)
	reshimIndex := strings.Index(miseScript, "/usr/local/bin/mise reshim")
	profileWriteIndex := strings.Index(miseScript, `cat >> "$profilePath" <<'STUDIO_MISE_PROFILE'`)
	if profileWriteIndex < 0 {
		t.Fatal("mise wrapper must append a persistent login profile")
	}
	profileMarkerOffset := strings.Index(miseScript[profileWriteIndex:], "# sandbox-studio managed mise login profile\n")
	profileMarkerIndex := profileWriteIndex + profileMarkerOffset
	if profileMarkerOffset < 0 {
		t.Fatal("managed profile block is missing from the profile heredoc")
	}
	profileHookIndex := strings.Index(miseScript[profileMarkerIndex:], ". "+shellLiteral(studioHook))
	profileClearIndex := strings.Index(miseScript[profileMarkerIndex:], "for name in ${!MISE_@}; do unset")
	profileEnvIndex := strings.Index(miseScript[profileMarkerIndex:], "export MISE_CONFIG_DIR=")
	activateIndex := strings.Index(miseScript, `eval "$(/usr/local/bin/mise activate bash)"`)
	profileHookIndex += profileMarkerIndex
	profileClearIndex += profileMarkerIndex
	profileEnvIndex += profileMarkerIndex
	if useIndex < 0 || reshimIndex <= useIndex || profileWriteIndex <= reshimIndex ||
		profileMarkerIndex <= profileWriteIndex || profileHookIndex <= profileMarkerIndex ||
		profileClearIndex <= profileHookIndex || profileEnvIndex <= profileClearIndex || activateIndex <= profileEnvIndex {
		t.Fatal("mise pins must be installed and resolved before the persistent login profile is written")
	}
	if !strings.Contains(miseScript, `profilePath="$HOME/.profile"`) || !strings.Contains(miseScript, `cat >> "$profilePath"`) {
		t.Fatal("mise login activation must append to an existing agent profile")
	}
}

func TestCommandsRejectInvalidSpecAndKeepSetupOutOfWrapper(t *testing.T) {
	setup := "printf '%s' \"$(touch /tmp/outer-shell-injection)\""
	spec := templatespec.Spec{Resources: resources.Defaults(), Setup: setup}
	stages, err := Commands(spec)
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	setupCommand := stages[2].Command
	if setupCommand.Args[len(setupCommand.Args)-1] != setup {
		t.Fatalf("setup argv = %#v, want setup as its own final positional argument", setupCommand.Args)
	}
	if strings.Contains(setupCommand.Args[2], setup) {
		t.Fatal("setup script was copied into the fixed wrapper")
	}

	invalid := templatespec.Spec{Resources: resources.Defaults(), Apt: []string{"git;touch /tmp/apt-injection"}}
	if _, err := Commands(invalid); !errors.Is(err, templatespec.ErrInvalid) {
		t.Fatalf("invalid apt package error = %v, want templatespec.ErrInvalid", err)
	}
	invalid = templatespec.Spec{Resources: resources.Defaults(), Tools: []templatespec.ToolPin{{Name: "node", Version: "22;touch"}}}
	if _, err := Commands(invalid); !errors.Is(err, templatespec.ErrInvalid) {
		t.Fatalf("invalid tool pin error = %v, want templatespec.ErrInvalid", err)
	}
}

func TestCommandsEmptySpecDoesNotRequestNetworkWork(t *testing.T) {
	stages, err := Commands(templatespec.Spec{Resources: resources.Defaults()})
	if err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if len(stages) != 3 {
		t.Fatalf("stage count = %d, want 3", len(stages))
	}
	if len(stages[0].Command.Args) != 5 || len(stages[1].Command.Args) != 5 {
		t.Fatalf("empty spec supplied package or tool arguments: apt=%#v mise=%#v", stages[0].Command.Args, stages[1].Command.Args)
	}
	if stages[2].Command.Cwd != agentHome || stages[2].Command.Args[len(stages[2].Command.Args)-1] != "" {
		t.Fatalf("empty setup command = %+v", stages[2].Command)
	}
	aptGuard := strings.Index(rootAptScript, `if [ "$#" -eq 0 ]; then`)
	aptNetwork := strings.Index(rootAptScript, "apt-get update")
	miseGuard := strings.Index(miseScript, `if [ "$#" -gt 0 ]; then`)
	miseNetwork := strings.Index(miseScript, `/usr/local/bin/mise use --global`)
	if aptGuard < 0 || aptNetwork <= aptGuard || miseGuard < 0 || miseNetwork <= miseGuard {
		t.Fatal("empty package and tool lists must skip apt and mise network commands")
	}
}
