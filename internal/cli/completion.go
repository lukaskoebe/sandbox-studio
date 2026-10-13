package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// completeCommand is the hidden command the completion scripts call with the words typed so far.
const completeCommand = "__complete"

// completeTimeout bounds the names completion asks the running Studio for. Without a Studio,
// completion offers commands and flags only, and never starts one.
const completeTimeout = 1500 * time.Millisecond

// sandboxFirst are the commands whose first argument is a sandbox.
var sandboxFirst = []string{"show", "start", "stop", "suspend", "resume", "fork", "rm", "shell", "ask", "checkpoints", "checkpoint", "restore"}

var scripts = map[string]string{
	"bash": `# studio completion for bash: source <(studio completion bash)
_studio() {
  local IFS=$'\n'
  COMPREPLY=($(studio __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null))
}
complete -o default -F _studio studio
`,
	"zsh": `#compdef studio
# studio completion for zsh: source <(studio completion zsh)
_studio() {
  local -a candidates
  candidates=("${(@f)$(studio __complete "${(@)words[2,CURRENT]}" 2>/dev/null)}")
  compadd -a candidates
}
compdef _studio studio
`,
	"fish": `# studio completion for fish: studio completion fish | source
complete -c studio -f -a '(studio __complete (commandline -opc)[2..-1] (commandline -ct) 2>/dev/null)'
`,
}

func (c *call) completion(rest []string) error {
	if err := args(rest, "shell"); err != nil {
		return err
	}
	script, ok := scripts[rest[0]]
	if !ok {
		return usagef("no completion for %q: bash, zsh or fish", rest[0])
	}
	_, err := io.WriteString(c.opts.Stdout, script)
	return err
}

// complete prints the candidates for the last of words, one per line, filtered by what is typed.
func complete(ctx context.Context, opts Options, words []string) int {
	if len(words) == 0 {
		words = []string{""}
	}
	// A leading -addr is the server's flag and says which Studio to ask.
	if len(words) > 2 && strings.TrimLeft(words[0], "-") == "addr" {
		opts.Addr, words = words[1], words[2:]
	}
	partial := words[len(words)-1]
	var out []string
	if len(words) == 1 {
		for _, cmd := range commands {
			out = append(out, cmd.name)
		}
		out = append(out, "help")
	} else {
		out = candidates(ctx, opts, words[0], words[1:len(words)-1], partial)
	}
	for _, s := range out {
		if strings.HasPrefix(s, partial) && !strings.ContainsAny(s, "\n\r") {
			fmt.Fprintln(opts.Stdout, s)
		}
	}
	return ExitOK
}

func candidates(ctx context.Context, opts Options, name string, before []string, partial string) []string {
	if name == "help" {
		return commandNames()
	}
	cmd := lookup(name)
	if cmd == nil {
		return nil
	}
	if strings.HasPrefix(partial, "-") {
		fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
		c := &call{}
		if !cmd.offline {
			fs.String("env", "", "")
			fs.Bool("json", false, "")
		}
		if cmd.flags != nil {
			cmd.flags(fs, c)
		}
		var out []string
		fs.VisitAll(func(f *flag.Flag) { out = append(out, "--"+f.Name) })
		return out
	}
	if cmd.name == "completion" {
		return []string{"bash", "fish", "zsh"}
	}
	// The value of a flag that takes one.
	if n := len(before); n > 0 && strings.HasPrefix(before[n-1], "-") && !strings.Contains(before[n-1], "=") {
		switch strings.TrimLeft(before[n-1], "-") {
		case "env":
			return names(ctx, opts, "", func(c *call) ([]string, error) {
				var list []store.Environment
				err := c.client.Do(c.ctx, http.MethodGet, "/api/environments", nil, &list)
				return pluck(list, func(e store.Environment) string { return e.Name }), err
			})
		case "teammate":
			return names(ctx, opts, envOf(before), func(c *call) ([]string, error) {
				team, err := c.teammates()
				var out []string
				for _, t := range team {
					out = append(out, t.Name)
				}
				return out, err
			})
		case "json", "yes", "once", "quiet", "no-wait":
		default:
			return nil
		}
	}
	positional := 0
	for i, w := range before {
		if !strings.HasPrefix(w, "-") && (i == 0 || !takesValue(before[i-1])) {
			positional++
		}
	}
	switch {
	case positional == 0 && slices.Contains(sandboxFirst, cmd.name):
		return names(ctx, opts, envOf(before), func(c *call) ([]string, error) {
			list, err := c.sandboxes()
			var out []string
			for _, sb := range list {
				out = append(out, sb.Name)
			}
			return out, err
		})
	case positional == 0 && (cmd.name == "approve" || cmd.name == "deny"):
		return names(ctx, opts, "", func(c *call) ([]string, error) {
			var list []api.ApprovalView
			err := c.client.Do(c.ctx, http.MethodGet, "/api/approvals", nil, &list)
			return pluck(list, func(a api.ApprovalView) string { return a.ID }), err
		})
	}
	return nil
}

// names asks the running Studio, briefly, and gives nothing when it doesn't answer in time.
func names(ctx context.Context, opts Options, env string, get func(c *call) ([]string, error)) []string {
	ctx, cancel := context.WithTimeout(ctx, completeTimeout)
	defer cancel()
	client, err := opts.Connect(ctx, opts.Addr)
	if err != nil {
		return nil
	}
	defer client.Close(context.WithoutCancel(ctx))
	if env == "" && opts.Getenv != nil {
		env = opts.Getenv("STUDIO_ENV")
	}
	out, err := get(&call{ctx: ctx, opts: opts, client: client, env: env})
	if err != nil {
		return nil
	}
	slices.Sort(out)
	return out
}

// envOf is the --env typed so far.
func envOf(words []string) string {
	for i, w := range words {
		name, value, hasValue := strings.Cut(strings.TrimLeft(w, "-"), "=")
		if !strings.HasPrefix(w, "-") || name != "env" {
			continue
		}
		if hasValue {
			return value
		}
		if i+1 < len(words) {
			return words[i+1]
		}
	}
	return ""
}

func commandNames() []string {
	var out []string
	for _, cmd := range commands {
		out = append(out, cmd.name)
	}
	return out
}

func pluck[T any](list []T, f func(T) string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, f(v))
	}
	return out
}

// takesValue reports whether w is a flag whose value is the next word.
func takesValue(w string) bool {
	if !strings.HasPrefix(w, "-") || strings.Contains(w, "=") {
		return false
	}
	return slices.Contains([]string{"env", "teammate", "template", "cpus", "memory", "chat", "scope"}, strings.TrimLeft(w, "-"))
}
