package agentcall

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The `studio` command (docs/agent-cli.md): the tools of the studio MCP server as commands an
// agent, a script or the user over SSH can run, pipe and loop over. It is a front end on Tools:
// each subcommand's flags come from its tool's schema, and a call goes through the call
// socket with the same identity, approvals and logs as the MCP tool's.

// Exit codes: 0 done, 1 refused or failed, 2 a usage mistake, 75 waiting for the user. `pr
// checks` exits 1 when a check failed and 8 while one runs, as gh does.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	ExitPending = 8
	ExitWaiting = 75
)

// CLIEnv is what the command takes from its surroundings, so that tests can stand in for it.
type CLIEnv struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Git runs git in the working directory and returns its trimmed output; it finds the
	// repository and branch a pull request command is about.
	Git func(ctx context.Context, args ...string) (string, error)
	// ReadFile reads a --<flag>-file argument.
	ReadFile func(name string) ([]byte, error)
	// WriteFile writes an image a tool returned.
	WriteFile func(name string, data []byte) error
	// TempDir is where an image goes without -o.
	TempDir string
	// Now names the image files.
	Now func() time.Time
	// Poll is how long --wait waits between asking again; `pr checks --wait` waits
	// checksPolls times as long, sparing the forge.
	Poll time.Duration
}

// OSEnv is the command's real surroundings.
func OSEnv() CLIEnv {
	return CLIEnv{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Git: func(ctx context.Context, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, "git", args...).Output()
			return strings.TrimSpace(string(out)), err
		},
		ReadFile:  os.ReadFile,
		WriteFile: func(name string, data []byte) error { return os.WriteFile(name, data, 0o644) },
		TempDir:   os.TempDir(),
		Now:       time.Now,
		Poll:      5 * time.Second,
	}
}

// command is one subcommand: the words that name it, its tool, and the tool's arguments that
// come as positional arguments, in order.
type command struct {
	words []string
	tool  Tool
	args  []string
}

// commandNames name the tools whose command isn't simply their name split at the first _.
var commandNames = map[string]struct {
	words string
	args  []string
}{
	agentproto.MethodMemorySearch:               {"memory search", []string{"query"}},
	agentproto.MethodMemoryGet:                  {"memory get", nil},
	agentproto.MethodRemember:                   {"memory remember", []string{"text"}},
	agentproto.MethodShare:                      {"memory share", nil},
	agentproto.MethodCorrect:                    {"memory correct", []string{"fact_id", "text"}},
	agentproto.MethodForget:                     {"memory forget", []string{"fact_id"}},
	agentproto.MethodMemoryPage:                 {"memory page", []string{"slug"}},
	agentproto.MethodTasksList:                  {"task list", nil},
	agentproto.MethodTaskCreate:                 {"task create", []string{"title"}},
	agentproto.MethodTaskUpdateStatus:           {"task status", []string{"task_id", "status"}},
	agentproto.MethodTaskAddNote:                {"task note", []string{"task_id", "note"}},
	agentproto.MethodGitPushStatus:              {"git pushes", nil},
	agentproto.MethodGitRepositories:            {"git repos", []string{"query"}},
	agentproto.MethodGitPullRequestStatus:       {"pr status", []string{"branch"}},
	agentproto.MethodGitPullRequestCreate:       {"pr create", []string{"title"}},
	agentproto.MethodGitPullRequestList:         {"pr list", nil},
	agentproto.MethodGitPullRequestRead:         {"pr read", []string{"number"}},
	agentproto.MethodGitPullRequestReview:       {"pr review", []string{"number"}},
	agentproto.MethodGitReviewStatus:            {"pr review-status", []string{"number"}},
	agentproto.MethodGitPullRequestChecks:       {"pr checks", []string{"number"}},
	agentproto.MethodGitPullRequestComment:      {"pr comment", []string{"number"}},
	agentproto.MethodSandboxInfo:                {"info", nil},
	agentproto.MethodApprovalStatus:             {"wait", []string{"approval"}},
	agentproto.MethodBrowserOpen:                {"browser open", []string{"url"}},
	agentproto.MethodBrowserClick:               {"browser click", []string{"ref"}},
	agentproto.MethodBrowserFill:                {"browser fill", []string{"ref", "text"}},
	agentproto.MethodBrowserType:                {"browser type", []string{"ref", "text"}},
	agentproto.MethodBrowserPress:               {"browser press", []string{"key"}},
	agentproto.MethodBrowserSelect:              {"browser select", []string{"ref", "value"}},
	agentproto.MethodBrowserScroll:              {"browser scroll", []string{"direction", "amount"}},
	agentproto.MethodBrowserGetText:             {"browser get-text", []string{"ref"}},
	agentproto.MethodBrowserFillCredential:      {"browser fill-credential", []string{"ref", "name"}},
	agentproto.MethodBrowserClose:               {"browser close", []string{"session"}},
	agentproto.MethodBrowserShow:                {"browser show", []string{"url"}},
	agentproto.MethodBrowserDownloadToWorkspace: {"browser download-to-workspace", []string{"name"}},
	agentproto.MethodBrowserRequestControl:      {"browser request-control", []string{"reason"}},
}

// commandTools are the tools the command runs: the MCP tools, the browser's, and its own.
func commandTools() []Tool {
	return slices.Concat(Tools, BrowserTools, []Tool{ForgeSyncCall})
}

// ForgeSyncCall asks Studio to fetch ~/forge again now. It is the command's only, and stays out
// of Tools, so MCP doesn't list it.
var ForgeSyncCall = Tool{agentproto.MethodForgeSync,
	"Fetch ~/forge again now: the pull requests of this sandbox's repositories, from their forges. It answers once the folder is written.",
	obj(nil, map[string]any{})}

// commands are the subcommands, one per tool and the command's own.
func commands() []command {
	tools := commandTools()
	out := make([]command, 0, len(tools))
	for _, t := range tools {
		c := command{tool: t}
		if n, ok := commandNames[t.Name]; ok {
			c.words, c.args = strings.Fields(n.words), n.args
		} else if t.Name == agentproto.MethodForgeSync {
			c.words = []string{"forge", "sync"}
		} else if group, verb, ok := strings.Cut(t.Name, "_"); ok {
			c.words = []string{group, strings.ReplaceAll(verb, "_", "-")}
		} else {
			c.words = []string{t.Name}
		}
		out = append(out, c)
	}
	return out
}

// RunCLI runs `studio <args>` and returns its exit code.
func RunCLI(ctx context.Context, c Caller, args []string, env CLIEnv) int {
	cmds := commands()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) > 1 {
			if cmd, _, ok := findCommand(cmds, args[1:]); ok {
				printCommandHelp(env.Stdout, cmd)
				return ExitOK
			}
			if group := groupCommands(cmds, args[1]); len(group) > 0 {
				printGroupHelp(env.Stdout, args[1], group)
				return ExitOK
			}
		}
		printHelp(env.Stdout, cmds)
		return ExitOK
	}
	if args[0] == "call" {
		return runCall(ctx, c, args[1:], env)
	}
	cmd, rest, ok := findCommand(cmds, args)
	if group := groupCommands(cmds, args[0]); !ok && len(group) > 0 {
		// `studio browser` or `studio browser --help`: what the group has.
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h" || args[1] == "help") {
			printGroupHelp(env.Stdout, args[0], group)
			return ExitOK
		}
		if len(args) > 1 {
			fmt.Fprintf(env.Stderr, "studio: unknown command %q\n", strings.Join(args[:2], " "))
		}
		printGroupHelp(env.Stderr, args[0], group)
		return ExitUsage
	}
	if !ok {
		fmt.Fprintf(env.Stderr, "studio: unknown command %q; `studio --help` lists them\n", strings.Join(args[:min(2, len(args))], " "))
		return ExitUsage
	}
	inv, err := parseArgs(ctx, cmd, rest, env)
	if errors.Is(err, errHelp) {
		printCommandHelp(env.Stdout, cmd)
		return ExitOK
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "studio %s: %v\n", strings.Join(cmd.words, " "), err)
		return ExitUsage
	}
	return invoke(ctx, c, cmd.tool.Name, inv, env)
}

// findCommand finds the command args start with, and the args after its words.
func findCommand(cmds []command, args []string) (command, []string, bool) {
	var best command
	found := false
	for _, c := range cmds {
		if len(args) >= len(c.words) && slices.Equal(args[:len(c.words)], c.words) && len(c.words) > len(best.words) {
			best, found = c, true
		}
	}
	if !found {
		return command{}, nil, false
	}
	return best, args[len(best.words):], true
}

// invocation is one call to make, and what to do with its result.
type invocation struct {
	params map[string]any
	json   bool
	wait   bool
	output string
}

var errHelp = errors.New("help")

// runCall is `studio call <tool> [json|-]`: any tool, its arguments as JSON.
func runCall(ctx context.Context, c Caller, args []string, env CLIEnv) int {
	inv := invocation{params: map[string]any{}}
	var pos []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			inv.json = true
		case a == "--wait":
			inv.wait = true
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(env.Stderr, "studio call: -o needs a file")
				return ExitUsage
			}
			i++
			inv.output = args[i]
		case strings.HasPrefix(a, "--output="):
			inv.output = strings.TrimPrefix(a, "--output=")
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 || len(pos) > 2 {
		fmt.Fprintln(env.Stderr, "usage: studio call <tool> ['<json arguments>' | -] [--json] [--wait] [-o file]")
		return ExitUsage
	}
	if len(pos) == 2 {
		raw := []byte(pos[1])
		if pos[1] == "-" {
			var err error
			if raw, err = io.ReadAll(io.LimitReader(env.Stdin, agentproto.MaxCallLine)); err != nil {
				fmt.Fprintf(env.Stderr, "studio call: %v\n", err)
				return ExitFailed
			}
		}
		if err := json.Unmarshal(raw, &inv.params); err != nil {
			fmt.Fprintf(env.Stderr, "studio call: the arguments are a JSON object: %v\n", err)
			return ExitUsage
		}
	}
	name := pos[0]
	if !slices.ContainsFunc(commandTools(), func(t Tool) bool { return t.Name == name }) {
		fmt.Fprintf(env.Stderr, "studio call: unknown tool %q; `studio <command> --help` names a command's tool\n", name)
		return ExitUsage
	}
	return invoke(ctx, c, name, inv, env)
}

// parseArgs turns a command's arguments into its tool's: positional ones in order, the rest as
// --flags named for the schema's properties (fact_id is --fact-id), and --<flag>-file for a
// string read from a file or stdin (-).
func parseArgs(ctx context.Context, cmd command, args []string, env CLIEnv) (invocation, error) {
	inv := invocation{params: map[string]any{}}
	props, _ := cmd.tool.InputSchema["properties"].(map[string]any)
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "h", "help":
			return inv, errHelp
		case "json":
			inv.json = true
			continue
		case "wait":
			inv.wait = true
			continue
		case "o", "output":
			v, err := next()
			if err != nil {
				return inv, err
			}
			inv.output = v
			continue
		}
		if cmd.tool.Name == agentproto.MethodGitPullRequestReview && name == "comment" {
			v, err := next()
			if err != nil {
				return inv, err
			}
			comment, err := parseLineComment(v)
			if err != nil {
				return inv, err
			}
			list, _ := inv.params["comments"].([]any)
			inv.params["comments"] = append(list, comment)
			continue
		}
		key := strings.ReplaceAll(name, "-", "_")
		fromFile := false
		if _, ok := props[key]; !ok && strings.HasSuffix(key, "_file") {
			key, fromFile = strings.TrimSuffix(key, "_file"), true
		}
		schema, ok := props[key].(map[string]any)
		if !ok {
			return inv, fmt.Errorf("unknown flag --%s", name)
		}
		if schema["type"] == "boolean" && !fromFile {
			b := true
			if hasValue {
				var err error
				if b, err = strconv.ParseBool(value); err != nil {
					return inv, fmt.Errorf("--%s is true or false", name)
				}
			}
			inv.params[key] = b
			continue
		}
		v, err := next()
		if err != nil {
			return inv, err
		}
		if fromFile {
			if schema["type"] != "string" {
				return inv, fmt.Errorf("unknown flag --%s", name)
			}
			data, err := readArg(env, v)
			if err != nil {
				return inv, err
			}
			inv.params[key] = string(data)
			continue
		}
		if inv.params[key], err = convert(key, schema, v); err != nil {
			return inv, err
		}
	}
	if len(pos) > len(cmd.args) {
		return inv, fmt.Errorf("too many arguments: %q", pos[len(cmd.args)])
	}
	for i, v := range pos {
		key := cmd.args[i]
		if _, set := inv.params[key]; set {
			return inv, fmt.Errorf("%s is given twice", key)
		}
		schema, _ := props[key].(map[string]any)
		var err error
		if inv.params[key], err = convert(key, schema, v); err != nil {
			return inv, err
		}
	}
	fillFromGit(ctx, props, inv.params, env)
	required, _ := cmd.tool.InputSchema["required"].([]string)
	for _, r := range required {
		if _, ok := inv.params[r]; !ok {
			return inv, fmt.Errorf("give %s", flagOrArg(cmd, r))
		}
	}
	return inv, nil
}

func flagOrArg(cmd command, key string) string {
	if slices.Contains(cmd.args, key) {
		return "<" + strings.ReplaceAll(key, "_", "-") + ">"
	}
	return "--" + strings.ReplaceAll(key, "_", "-")
}

func readArg(env CLIEnv, name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(io.LimitReader(env.Stdin, agentproto.MaxCallLine))
	}
	return env.ReadFile(name)
}

// convert reads a flag's value as its schema's type. An enum value may use - for _, as
// request-changes for request_changes.
func convert(key string, schema map[string]any, v string) (any, error) {
	flag := "--" + strings.ReplaceAll(key, "_", "-")
	switch schema["type"] {
	case "integer":
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s is a number, not %q", flag, v)
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s is true or false", flag)
		}
		return b, nil
	case "array", "object":
		var out any
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("%s is JSON: %v", flag, err)
		}
		return out, nil
	}
	if enum, ok := schema["enum"].([]string); ok && !slices.Contains(enum, v) {
		if alt := strings.ReplaceAll(v, "-", "_"); slices.Contains(enum, alt) {
			return alt, nil
		}
		return nil, fmt.Errorf("%s is one of %s", flag, strings.Join(enum, ", "))
	}
	return v, nil
}

// parseLineComment reads a review's --comment: path:line=body, or path:line:old=body for a
// removed line.
func parseLineComment(v string) (map[string]any, error) {
	where, body, ok := strings.Cut(v, "=")
	parts := strings.Split(where, ":")
	if !ok || strings.TrimSpace(body) == "" || len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("--comment is path:line=text, or path:line:old=text for a removed line")
	}
	line, err := strconv.Atoi(parts[1])
	if err != nil || line < 1 {
		return nil, fmt.Errorf("--comment %q: the line is a number", where)
	}
	c := map[string]any{"path": parts[0], "line": line, "body": body}
	if len(parts) == 3 {
		if parts[2] != "old" && parts[2] != "new" {
			return nil, fmt.Errorf("--comment %q: the side is old or new", where)
		}
		c["side"] = parts[2]
	}
	return c, nil
}

// fillFromGit fills what a pull request command leaves out from the repository it runs in: the
// forge and repository from a remote through Studio's git remote, and the branch checked out.
func fillFromGit(ctx context.Context, props, params map[string]any, env CLIEnv) {
	if env.Git == nil {
		return
	}
	_, wantRepo := props["repository"]
	_, wantForge := props["forge"]
	if wantRepo && params["repository"] == nil {
		if forge, repo, ok := studioRemote(ctx, env); ok {
			params["repository"] = repo
			if wantForge && params["forge"] == nil {
				params["forge"] = forge
			}
		}
	}
	if _, ok := props["branch"]; ok && params["branch"] == nil {
		if b, err := env.Git(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "" && b != "HEAD" {
			params["branch"] = b
		}
	}
}

// studioRemote is the forge and repository of the working directory's remote through
// git.studio.internal: origin's, else the only one there is.
func studioRemote(ctx context.Context, env CLIEnv) (forge, repo string, ok bool) {
	out, err := env.Git(ctx, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return "", "", false
	}
	type remote struct{ name, forge, repo string }
	var found []remote
	for line := range strings.Lines(out) {
		key, raw, _ := strings.Cut(strings.TrimSpace(line), " ")
		f, r, ok := agentproto.ParseGitRemote(raw)
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		found = append(found, remote{name, f, r})
	}
	for _, r := range found {
		if r.name == "origin" {
			return r.forge, r.repo, true
		}
	}
	if len(found) == 1 {
		return found[0].forge, found[0].repo, true
	}
	return "", "", false
}

// invoke makes the call and prints its result: text by default, the JSON with --json, an
// image to a file. A call that waits for the user exits 75, or with --wait asks again until
// the user decides.
func invoke(ctx context.Context, c Caller, tool string, inv invocation, env CLIEnv) int {
	result, err := call(ctx, c, tool, inv.params)
	for err != nil && waitingError(err) {
		if !inv.wait {
			fmt.Fprintf(env.Stderr, "studio: %v\n", err)
			fmt.Fprintln(env.Stderr, "studio: waiting for the user; run it again later, or with --wait")
			return ExitWaiting
		}
		if !sleep(ctx, env.Poll) {
			return ExitFailed
		}
		result, err = call(ctx, c, tool, inv.params)
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "studio: %v\n", err)
		return ExitFailed
	}
	switch tool {
	case agentproto.MethodGitPullRequestChecks:
		return checksResult(ctx, c, tool, inv, env, result)
	case agentproto.MethodApprovalStatus:
		return waitResult(ctx, c, tool, inv, env, result)
	}
	if awaiting(result) {
		follow, params := statusCall(tool, inv.params, result)
		if id := approvalOf(result); inv.wait && follow == "" && id != "" {
			// No status tool of its own, as for pr comment: approval_status says.
			inv.params = map[string]any{"approval": id}
			if result, err = call(ctx, c, agentproto.MethodApprovalStatus, inv.params); err != nil {
				fmt.Fprintf(env.Stderr, "studio: %v\n", err)
				return ExitFailed
			}
			return waitResult(ctx, c, agentproto.MethodApprovalStatus, inv, env, result)
		}
		if !inv.wait || follow == "" {
			if code := print(result, inv, tool, env); code != ExitOK {
				return code
			}
			switch id := approvalOf(result); {
			case id != "":
				fmt.Fprintf(env.Stderr, "studio: waiting for the user; `studio wait %s` waits for the decision\n", id)
			case follow != "":
				fmt.Fprintln(env.Stderr, "studio: waiting for the user; run it with --wait to wait for the decision")
			}
			return ExitWaiting
		}
		for awaiting(result) {
			if !sleep(ctx, env.Poll) {
				return ExitFailed
			}
			if result, err = call(ctx, c, follow, params); err != nil {
				fmt.Fprintf(env.Stderr, "studio: %v\n", err)
				return ExitFailed
			}
		}
	}
	return print(result, inv, tool, env)
}

// waitResult is `studio wait`: it asks again until the approval is settled, then prints it. It
// exits 0 when the user approved and what that did went through, 1 when they denied or dismissed
// it or the forge refused it.
func waitResult(ctx context.Context, c Caller, tool string, inv invocation, env CLIEnv, result json.RawMessage) int {
	var st struct {
		Status  string `json:"status"`
		Settled bool   `json:"settled"`
		Result  struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	_ = json.Unmarshal(result, &st)
	for !st.Settled {
		if !sleep(ctx, env.Poll) {
			return ExitFailed
		}
		var err error
		if result, err = call(ctx, c, tool, inv.params); err != nil {
			fmt.Fprintf(env.Stderr, "studio: %v\n", err)
			return ExitFailed
		}
		st.Result.Status = ""
		_ = json.Unmarshal(result, &st)
	}
	if code := print(result, inv, tool, env); code != ExitOK {
		return code
	}
	if st.Status != "approved" || st.Result.Status == "failed" {
		return ExitFailed
	}
	return ExitOK
}

// approvalOf is the approval ID a result that waits for the user names, at its top or in its
// pullRequest (pr create's is an object, pr review's a string).
func approvalOf(result json.RawMessage) string {
	var v struct {
		Approval    string          `json:"approval"`
		PullRequest json.RawMessage `json:"pullRequest"`
	}
	_ = json.Unmarshal(result, &v)
	if v.Approval == "" && len(v.PullRequest) > 0 {
		var pr struct {
			Approval string `json:"approval"`
		}
		_ = json.Unmarshal(v.PullRequest, &pr)
		return pr.Approval
	}
	return v.Approval
}

// checksPolls is how many Polls `pr checks --wait` waits between reads: 30 seconds.
const checksPolls = 6

// checksResult prints a pull request's checks: with --wait once none runs any more. It exits 0
// when they passed or there are none, 1 when one failed and 8 while one runs.
func checksResult(ctx context.Context, c Caller, tool string, inv invocation, env CLIEnv, result json.RawMessage) int {
	var sum struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	_ = json.Unmarshal(result, &sum)
	if sum.State == "pending" && inv.wait {
		// Ask about the number found, so the branch's pull request isn't looked up each time.
		params := maps.Clone(inv.params)
		if sum.Number > 0 {
			params["number"] = sum.Number
		}
		for sum.State == "pending" {
			if !sleep(ctx, checksPolls*env.Poll) {
				return ExitFailed
			}
			var err error
			if result, err = call(ctx, c, tool, params); err != nil {
				fmt.Fprintf(env.Stderr, "studio: %v\n", err)
				return ExitFailed
			}
			_ = json.Unmarshal(result, &sum)
		}
	}
	if code := print(result, inv, tool, env); code != ExitOK {
		return code
	}
	switch sum.State {
	case "failure":
		return ExitFailed
	case "pending":
		fmt.Fprintln(env.Stderr, "studio: checks are still running; --wait waits for them")
		return ExitPending
	}
	return ExitOK
}

func call(ctx context.Context, c Caller, tool string, params map[string]any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result json.RawMessage
	err := c.Call(ctx, tool, params, &result)
	return result, err
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// waitingError is a refusal that only waits for the user, as an action on a new site does:
// asking again later may go through.
func waitingError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.HasPrefix(msg, "waiting for the user") || strings.Contains(msg, "call again")
}

// awaiting is a result whose status says the user hasn't decided yet.
func awaiting(result json.RawMessage) bool {
	var v struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(result, &v) == nil && v.Status == "awaiting approval"
}

// statusCall is the tool that says what became of a call that waits for the user, and its
// arguments.
func statusCall(tool string, params map[string]any, result json.RawMessage) (string, map[string]any) {
	pick := func(keys ...string) map[string]any {
		out := map[string]any{}
		for _, k := range keys {
			if v, ok := params[k]; ok {
				out[k] = v
			}
		}
		return out
	}
	switch tool {
	case agentproto.MethodGitPullRequestCreate, agentproto.MethodGitPullRequestStatus:
		return agentproto.MethodGitPullRequestStatus, pick("branch", "forge", "repository")
	case agentproto.MethodGitPullRequestReview, agentproto.MethodGitReviewStatus:
		return agentproto.MethodGitReviewStatus, pick("forge", "repository", "number")
	}
	return "", nil
}

// print writes a result: an image to a file, whose path it prints; a string as it is; JSON as
// indented lines of key: value, or as the JSON with --json.
func print(result json.RawMessage, inv invocation, tool string, env CLIEnv) int {
	var img struct {
		Image *struct {
			MIMEType string `json:"mimeType"`
			Data     string `json:"data"`
		} `json:"image"`
		Text string `json:"text"`
	}
	if json.Unmarshal(result, &img) == nil && img.Image != nil && img.Image.Data != "" {
		data, err := base64.StdEncoding.DecodeString(img.Image.Data)
		if err != nil {
			fmt.Fprintf(env.Stderr, "studio: the image doesn't decode: %v\n", err)
			return ExitFailed
		}
		name := inv.output
		if name == "" {
			ext := ".png"
			if strings.Contains(img.Image.MIMEType, "jpeg") {
				ext = ".jpg"
			}
			name = filepath.Join(env.TempDir, fmt.Sprintf("studio-%s-%s%s", strings.ReplaceAll(tool, "_", "-"), env.Now().Format("20060102-150405.000"), ext))
		}
		if err := env.WriteFile(name, data); err != nil {
			fmt.Fprintf(env.Stderr, "studio: %v\n", err)
			return ExitFailed
		}
		if inv.json {
			out, _ := json.Marshal(map[string]any{"file": name, "mimeType": img.Image.MIMEType, "text": img.Text})
			fmt.Fprintln(env.Stdout, string(out))
		} else {
			fmt.Fprintln(env.Stdout, name)
			if img.Text != "" {
				fmt.Fprintln(env.Stdout, img.Text)
			}
		}
		return ExitOK
	}
	if inv.json {
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		fmt.Fprintln(env.Stdout, string(result))
		return ExitOK
	}
	var s string
	if json.Unmarshal(result, &s) == nil {
		fmt.Fprintln(env.Stdout, strings.TrimRight(s, "\n"))
		return ExitOK
	}
	var b strings.Builder
	if err := renderText(&b, json.NewDecoder(bytes.NewReader(result)), 0); err != nil {
		fmt.Fprintln(env.Stdout, string(result))
		return ExitOK
	}
	fmt.Fprint(env.Stdout, b.String())
	return ExitOK
}

// renderText writes JSON as plain text in its own order: an object's fields as key: value,
// a list's items as - item, nested ones indented, and text over several lines as a block.
func renderText(b *strings.Builder, dec *json.Decoder, indent int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return renderValue(b, dec, tok, indent, "")
}

func renderValue(b *strings.Builder, dec *json.Decoder, tok json.Token, indent int, lead string) error {
	pad := strings.Repeat("  ", indent)
	switch t := tok.(type) {
	case json.Delim:
		empty := true
		switch t {
		case '{':
			first := true
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				v, err := dec.Token()
				if err != nil {
					return err
				}
				prefix := pad
				if first && lead != "" {
					prefix = lead
				}
				first, empty = false, false
				if d, ok := v.(json.Delim); ok {
					if !dec.More() {
						// An empty object or list.
						_, _ = dec.Token()
						if d == '{' {
							fmt.Fprintf(b, "%s%s: {}\n", prefix, k)
						} else {
							fmt.Fprintf(b, "%s%s: []\n", prefix, k)
						}
						continue
					}
					fmt.Fprintf(b, "%s%s:\n", prefix, k)
					if err := renderValue(b, dec, d, indent+1, ""); err != nil {
						return err
					}
					continue
				}
				fmt.Fprintf(b, "%s%s:%s\n", prefix, k, scalar(v, indent+1))
			}
		case '[':
			for dec.More() {
				v, err := dec.Token()
				if err != nil {
					return err
				}
				empty = false
				if d, ok := v.(json.Delim); ok {
					if err := renderValue(b, dec, d, indent+1, pad+"- "); err != nil {
						return err
					}
					continue
				}
				fmt.Fprintf(b, "%s-%s\n", pad, scalar(v, indent+1))
			}
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return err
		}
		if empty && lead != "" {
			fmt.Fprintf(b, "%s\n", strings.TrimRight(lead, " "))
		}
		return nil
	default:
		fmt.Fprintf(b, "%s%s\n", pad, strings.TrimPrefix(scalar(t, indent), " "))
		return nil
	}
}

// scalar is a value after a key or a list's dash: " value", or text over several lines on the
// lines below, indented.
func scalar(v json.Token, indent int) string {
	switch t := v.(type) {
	case nil:
		return " —"
	case string:
		if !strings.Contains(t, "\n") {
			return " " + t
		}
		pad := strings.Repeat("  ", indent)
		var b strings.Builder
		for line := range strings.Lines(strings.TrimRight(t, "\n")) {
			b.WriteString("\n" + pad + strings.TrimRight(line, "\n"))
		}
		return b.String()
	case json.Number:
		return " " + t.String()
	case float64:
		return " " + strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(" ", t)
	}
}

// printHelp lists the commands by group.
func printHelp(w io.Writer, cmds []command) {
	fmt.Fprint(w, `studio: Sandbox Studio's tools as commands. Each one calls Studio, which decides what this
sandbox's teammate may do; an action that needs the user's approval asks them first.

Usage:
  studio <command> [arguments] [--flags]   flags are the tool's arguments: studio <command> --help
  studio <group> --help                    a group's commands; studio browser --help explains the browser
  studio call <tool> '<json>'              any tool by its name; - reads the JSON from stdin

Output is plain text; --json prints the result as JSON for jq. An image, as a screenshot, goes to
a file (-o to name it) whose path is printed. A --<flag>-file reads a text argument from a file,
or from stdin with -.

Exit codes: 0 done, 1 refused or failed, 2 a usage mistake, 75 waiting for the user's approval.
--wait asks again until the user decides. pr checks exits 1 when a check failed and 8 while one
runs; with --wait it waits until none runs.

Commands:
`)
	width := 0
	for _, c := range cmds {
		width = max(width, len(usageLine(c)))
	}
	group := ""
	for _, c := range sortedCommands(cmds) {
		if g := c.words[0]; g != group && len(c.words) > 1 {
			group = g
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "  %-*s  %s\n", width, usageLine(c), firstSentence(c.tool.Description))
	}
	fmt.Fprintln(w, "\n~/forge holds the pull requests of this sandbox's repositories as files; ~/memory holds your memory.")
}

// groupCommands is the commands whose first word is group, as "browser".
func groupCommands(cmds []command, group string) []command {
	var out []command
	for _, c := range sortedCommands(cmds) {
		if len(c.words) > 1 && c.words[0] == group {
			out = append(out, c)
		}
	}
	return out
}

// groupHelp opens a group's help.
var groupHelp = map[string]string{"browser": browserHelp}

func printGroupHelp(w io.Writer, group string, cmds []command) {
	if h := groupHelp[group]; h != "" {
		fmt.Fprintf(w, "%s\n\n", h)
	}
	fmt.Fprintf(w, "studio %s commands; studio %s <command> --help shows a command's flags:\n\n", group, group)
	width := 0
	for _, c := range cmds {
		width = max(width, len(usageLine(c)))
	}
	for _, c := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, usageLine(c), firstSentence(c.tool.Description))
	}
}

func sortedCommands(cmds []command) []command {
	out := slices.Clone(cmds)
	slices.SortStableFunc(out, func(a, b command) int {
		return strings.Compare(strings.Join(a.words, " "), strings.Join(b.words, " "))
	})
	return out
}

func usageLine(c command) string {
	parts := slices.Clone(c.words)
	required, _ := c.tool.InputSchema["required"].([]string)
	for _, a := range c.args {
		name := strings.ReplaceAll(a, "_", "-")
		if slices.Contains(required, a) {
			parts = append(parts, "<"+name+">")
		} else {
			parts = append(parts, "["+name+"]")
		}
	}
	return strings.Join(parts, " ")
}

// firstSentence is a description's first sentence, for the list of commands; "e.g." ends none.
func firstSentence(s string) string {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' && !strings.HasSuffix(s[:i], "e.g") && !strings.HasSuffix(s[:i], "i.e") {
			s = s[:i+1]
			break
		}
	}
	if r := []rune(s); len(r) > 100 {
		s = strings.TrimRight(string(r[:99]), " ,;:") + "…"
	}
	return s
}

// printCommandHelp shows a command's tool, what it does, and its flags.
func printCommandHelp(w io.Writer, c command) {
	fmt.Fprintf(w, "studio %s   (tool %s, for studio call)\n\n%s\n", usageLine(c), c.tool.Name, c.tool.Description)
	props, _ := c.tool.InputSchema["properties"].(map[string]any)
	required, _ := c.tool.InputSchema["required"].([]string)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		ia, ib := slices.Index(c.args, a), slices.Index(c.args, b)
		if (ia >= 0) != (ib >= 0) {
			if ia >= 0 {
				return -1
			}
			return 1
		}
		if ia >= 0 {
			return ia - ib
		}
		return strings.Compare(a, b)
	})
	if len(keys) > 0 {
		fmt.Fprintln(w, "\nArguments:")
	}
	for _, k := range keys {
		s, _ := props[k].(map[string]any)
		name := "--" + strings.ReplaceAll(k, "_", "-")
		if slices.Contains(c.args, k) {
			name = "<" + strings.ReplaceAll(k, "_", "-") + ">"
			if !slices.Contains(required, k) {
				name = "[" + strings.ReplaceAll(k, "_", "-") + "]"
			}
		}
		var notes []string
		if t, _ := s["type"].(string); t != "" && t != "string" {
			notes = append(notes, t)
		}
		if e, ok := s["enum"].([]string); ok {
			notes = append(notes, strings.Join(e, "|"))
		}
		if slices.Contains(required, k) {
			notes = append(notes, "required")
		}
		if k == "repository" || k == "forge" {
			notes = append(notes, "default: this repository's remote")
		}
		if k == "branch" {
			notes = append(notes, "default: the branch checked out")
		}
		desc, _ := s["description"].(string)
		if len(notes) > 0 {
			desc = strings.TrimSpace(desc + " (" + strings.Join(notes, "; ") + ")")
		}
		line := fmt.Sprintf("  %-22s %s", name, desc)
		fmt.Fprintln(w, line)
	}
	if c.tool.Name == agentproto.MethodGitPullRequestReview {
		fmt.Fprintln(w, "  --comment path:line=text  a comment on a line of the new file; path:line:old=text on a removed line; repeat it")
	}
	fmt.Fprintln(w, "\n  --json  print the result as JSON    --wait  wait for the user's decision    -o file  where an image goes")
}
