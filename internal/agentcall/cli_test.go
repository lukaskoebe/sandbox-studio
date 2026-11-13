package agentcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

type cliRun struct {
	code   int
	stdout string
	stderr string
	files  map[string][]byte
}

func runCLI(t *testing.T, c Caller, stdin string, git map[string]string, args ...string) cliRun {
	t.Helper()
	var out, errOut bytes.Buffer
	files := map[string][]byte{}
	env := CLIEnv{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
		Git: func(_ context.Context, args ...string) (string, error) {
			if v, ok := git[strings.Join(args, " ")]; ok {
				return v, nil
			}
			return "", errors.New("not a git repository")
		},
		ReadFile: func(name string) ([]byte, error) {
			if name == "body.md" {
				return []byte("from a file\n"), nil
			}
			return nil, os.ErrNotExist
		},
		WriteFile: func(name string, data []byte) error { files[name] = data; return nil },
		TempDir:   "/tmp",
		Now:       func() time.Time { return time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC) },
		Poll:      time.Millisecond,
	}
	code := RunCLI(context.Background(), c, args, env)
	return cliRun{code, out.String(), errOut.String(), files}
}

func params(t *testing.T, c fakeCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.Params, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Every command's positional arguments are properties of its tool, so none of them goes to
// Studio under a name it doesn't know.
func TestCommandArgsAreSchemaProperties(t *testing.T) {
	seen := map[string]string{}
	for _, c := range commands() {
		props, _ := c.tool.InputSchema["properties"].(map[string]any)
		for _, a := range c.args {
			if _, ok := props[a]; !ok {
				t.Errorf("studio %s: <%s> isn't a property of %s", strings.Join(c.words, " "), a, c.tool.Name)
			}
		}
		name := strings.Join(c.words, " ")
		if other, ok := seen[name]; ok {
			t.Errorf("studio %s names both %s and %s", name, other, c.tool.Name)
		}
		seen[name] = c.tool.Name
	}
	for name := range commandNames {
		found := false
		for _, tool := range commandTools() {
			found = found || tool.Name == name
		}
		if !found {
			t.Errorf("commandNames names %s, which isn't a tool", name)
		}
	}
}

func TestCLIPositionalAndFlags(t *testing.T) {
	f := &fakeCaller{reply: func(string, json.RawMessage) (any, error) { return "ok", nil }}
	r := runCLI(t, f, "", nil, "memory", "search", "deploy steps", "--limit=3")
	if r.code != ExitOK || r.stdout != "ok\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	got := params(t, f.calls[0])
	if f.calls[0].Method != agentproto.MethodMemorySearch || got["query"] != "deploy steps" {
		t.Fatalf("call %s %v", f.calls[0].Method, got)
	}
	if got["limit"] != float64(3) {
		t.Fatalf("limit %v, want the number 3", got["limit"])
	}
}

func TestCLIUnknownFlagAndMissingArgument(t *testing.T) {
	f := &fakeCaller{}
	if r := runCLI(t, f, "", nil, "memory", "search", "x", "--nope", "1"); r.code != ExitUsage || !strings.Contains(r.stderr, "unknown flag --nope") {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	if r := runCLI(t, f, "", nil, "memory", "remember"); r.code != ExitUsage || !strings.Contains(r.stderr, "give <text>") {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	if r := runCLI(t, f, "", nil, "memory", "nonsense"); r.code != ExitUsage {
		t.Fatalf("code %d", r.code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestCLIFlagFromFileAndStdin(t *testing.T) {
	f := &fakeCaller{}
	runCLI(t, f, "", nil, "memory", "remember", "--text-file", "body.md", "--kind", "fact")
	runCLI(t, f, "piped\n", nil, "memory", "remember", "--kind=fact", "--text-file", "-")
	if got := params(t, f.calls[0])["text"]; got != "from a file\n" {
		t.Fatalf("text %q", got)
	}
	if got := params(t, f.calls[1])["text"]; got != "piped\n" {
		t.Fatalf("text %q", got)
	}
}

func TestCLIEnumTakesDashes(t *testing.T) {
	f := &fakeCaller{}
	r := runCLI(t, f, "", nil, "pr", "review", "12", "--forge", "github", "--repository", "o/r", "--verdict", "request-changes", "--summary", "see the comments",
		"--comment", "src/a.go:10=nil here", "--comment", "src/b.go:3:old=why was this removed?")
	if r.code != ExitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	got := params(t, f.calls[0])
	if got["verdict"] != "request_changes" || got["number"] != float64(12) {
		t.Fatalf("params %v", got)
	}
	comments, _ := got["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("comments %v", got["comments"])
	}
	second := comments[1].(map[string]any)
	if second["path"] != "src/b.go" || second["line"] != float64(3) || second["side"] != "old" || second["body"] != "why was this removed?" {
		t.Fatalf("comment %v", second)
	}
}

// A pull request command run in a checkout takes its forge, repository and branch from the
// remote through Studio's git host and the branch checked out.
func TestCLIInfersTheRepository(t *testing.T) {
	git := map[string]string{
		`config --get-regexp ^remote\..*\.url$`: "remote.upstream.url https://git.studio.internal/gitlab/group/sub/proj.git\nremote.origin.url https://git.studio.internal/github/me/app.git",
		"rev-parse --abbrev-ref HEAD":           "fix-login",
	}
	f := &fakeCaller{}
	if r := runCLI(t, f, "", git, "pr", "status"); r.code != ExitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	got := params(t, f.calls[0])
	if got["forge"] != "github" || got["repository"] != "me/app" || got["branch"] != "fix-login" {
		t.Fatalf("params %v", got)
	}
	// One studio remote, not named origin, is the one.
	git[`config --get-regexp ^remote\..*\.url$`] = "remote.github.url https://example.com/x.git\nremote.lab.url https://git.studio.internal/gitlab/group/sub/proj.git"
	runCLI(t, f, "", git, "pr", "list")
	got = params(t, f.calls[1])
	if got["forge"] != "gitlab" || got["repository"] != "group/sub/proj" {
		t.Fatalf("params %v", got)
	}
	// What the command line says wins.
	runCLI(t, f, "", git, "pr", "read", "7", "--repository", "other/repo", "--forge", "forgejo")
	got = params(t, f.calls[2])
	if got["forge"] != "forgejo" || got["repository"] != "other/repo" || got["number"] != float64(7) {
		t.Fatalf("params %v", got)
	}
}

func TestParseGitRemote(t *testing.T) {
	for raw, want := range map[string][2]string{
		"https://git.studio.internal/github/o/r.git":             {"github", "o/r"},
		"https://git.studio.internal/codeberg/o/r":               {"codeberg", "o/r"},
		"https://git.studio.internal/gitlab/g/s/p.git":           {"gitlab", "g/s/p"},
		"https://github.com/o/r.git":                             {},
		"https://git.studio.internal/github/r.git":               {},
		"git@git.studio.internal:github/o/r.git":                 {},
		"https://git.studio.internal.example.com/github/o/r.git": {},
	} {
		forge, repo, ok := agentproto.ParseGitRemote(raw)
		if ok != (want[0] != "") || forge != want[0] || repo != want[1] {
			t.Errorf("%s: %q %q %v, want %v", raw, forge, repo, ok, want)
		}
	}
}

func TestCLIOutput(t *testing.T) {
	result := map[string]any{"pull_requests": []any{
		map[string]any{"number": 4, "title": "Fix the login", "draft": false, "labels": []any{}},
	}, "next": nil, "note": "line one\nline two"}
	f := &fakeCaller{reply: func(string, json.RawMessage) (any, error) { return result, nil }}
	r := runCLI(t, f, "", nil, "pr", "list", "--forge", "github", "--repository", "o/r")
	want := "next: —\nnote:\n  line one\n  line two\npull_requests:\n  - draft: false\n    labels: []\n    number: 4\n    title: Fix the login\n"
	if r.stdout != want {
		t.Fatalf("stdout\n%s\nwant\n%s", r.stdout, want)
	}
	r = runCLI(t, f, "", nil, "pr", "list", "--forge", "github", "--repository", "o/r", "--json")
	var back map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &back); err != nil || back["note"] != "line one\nline two" {
		t.Fatalf("--json %q: %v", r.stdout, err)
	}
}

func TestCLIImageGoesToAFile(t *testing.T) {
	f := &fakeCaller{reply: func(string, json.RawMessage) (any, error) {
		return map[string]any{"image": map[string]any{"mimeType": "image/png", "data": "aGVsbG8="}, "text": "1280x577"}, nil
	}}
	r := runCLI(t, f, "", nil, "browser", "screenshot")
	name := filepath.Join("/tmp", "studio-browser-screenshot-20261112-093000.000.png")
	if r.code != ExitOK || string(r.files[name]) != "hello" || r.stdout != name+"\n1280x577\n" {
		t.Fatalf("code %d, stdout %q, files %v, stderr %q", r.code, r.stdout, r.files, r.stderr)
	}
	r = runCLI(t, f, "", nil, "browser", "screenshot", "-o", "shot.png", "--json")
	if string(r.files["shot.png"]) != "hello" || !strings.Contains(r.stdout, `"file":"shot.png"`) {
		t.Fatalf("stdout %q, files %v", r.stdout, r.files)
	}
}

func TestCLIErrorsExitOne(t *testing.T) {
	f := &fakeCaller{reply: func(string, json.RawMessage) (any, error) { return nil, errors.New("no such task") }}
	r := runCLI(t, f, "", nil, "task", "note", "t1", "done")
	if r.code != ExitFailed || r.stderr != "studio: no such task\n" {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
}

// A browser action on a site the user hasn't allowed yet waits for them: 75 without --wait,
// and with it the call is made again until it goes through.
func TestCLIWaitsForTheUser(t *testing.T) {
	n := 0
	f := &fakeCaller{reply: func(string, json.RawMessage) (any, error) {
		n++
		if n < 3 {
			return nil, errors.New("waiting for the user to approve example.com; call again in a minute to retry")
		}
		return "opened", nil
	}}
	r := runCLI(t, f, "", nil, "browser", "open", "https://example.com")
	if r.code != ExitWaiting || !strings.Contains(r.stderr, "--wait") {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	r = runCLI(t, f, "", nil, "browser", "open", "https://example.com", "--wait")
	if r.code != ExitOK || r.stdout != "opened\n" || n != 3 {
		t.Fatalf("code %d, stdout %q, calls %d", r.code, r.stdout, n)
	}
}

// A pull request waits for approval: create prints the proposal and exits 75; with --wait it
// asks git_pull_request_status until the user decides.
func TestCLIWaitsForAPullRequest(t *testing.T) {
	status := 0
	f := &fakeCaller{reply: func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case agentproto.MethodGitPullRequestCreate:
			return map[string]any{"status": "awaiting approval"}, nil
		case agentproto.MethodGitPullRequestStatus:
			status++
			if status < 2 {
				return map[string]any{"status": "awaiting approval"}, nil
			}
			return map[string]any{"status": "open", "url": "https://github.com/o/r/pull/9"}, nil
		}
		return nil, errors.New("unexpected " + method)
	}}
	args := []string{"pr", "create", "Fix the login", "--branch", "fix", "--forge", "github", "--repository", "o/r"}
	r := runCLI(t, f, "", nil, args...)
	if r.code != ExitWaiting || r.stdout != "status: awaiting approval\n" {
		t.Fatalf("code %d, stdout %q", r.code, r.stdout)
	}
	r = runCLI(t, f, "", nil, append(args, "--wait")...)
	if r.code != ExitOK || !strings.Contains(r.stdout, "url: https://github.com/o/r/pull/9") {
		t.Fatalf("code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	last := f.calls[len(f.calls)-1]
	if got := params(t, last); last.Method != agentproto.MethodGitPullRequestStatus || got["branch"] != "fix" || got["repository"] != "o/r" {
		t.Fatalf("last call %s %v", last.Method, got)
	}
}

func TestCLICall(t *testing.T) {
	f := &fakeCaller{}
	if r := runCLI(t, f, "", nil, "call", "task_create", `{"title":"Ship it"}`); r.code != ExitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	if r := runCLI(t, f, `{"title":"From stdin"}`, nil, "call", "task_create", "-"); r.code != ExitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	if params(t, f.calls[0])["title"] != "Ship it" || params(t, f.calls[1])["title"] != "From stdin" {
		t.Fatalf("calls %v", f.calls)
	}
	// Only the tools: the credential helpers stay their own commands.
	for _, name := range []string{agentproto.MethodCredential, agentproto.MethodGitCredential, agentproto.MethodHook, "nope"} {
		if r := runCLI(t, f, "", nil, "call", name, `{}`); r.code != ExitUsage {
			t.Fatalf("call %s: code %d", name, r.code)
		}
	}
	if r := runCLI(t, f, "", nil, "call", "task_create", `not json`); r.code != ExitUsage {
		t.Fatalf("code %d", r.code)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestCLIHelp(t *testing.T) {
	r := runCLI(t, &fakeCaller{}, "", nil, "--help")
	for _, want := range []string{"memory search <query>", "pr review <number>", "browser get-text <ref>", "forge sync", "Exit codes"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, r.stdout)
		}
	}
	r = runCLI(t, &fakeCaller{}, "", nil, "pr", "review", "--help")
	for _, want := range []string{"git_pull_request_review", "--verdict", "comment|approve|request_changes", "--comment path:line=text"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("pr review --help lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := runCLI(t, &fakeCaller{}, "", nil, "help", "task", "create"); !strings.Contains(r.stdout, "task_create") {
		t.Errorf("help task create:\n%s", r.stdout)
	}
	// A group lists its commands, the way agents look for them.
	for _, args := range [][]string{{"browser", "--help"}, {"help", "browser"}} {
		if r := runCLI(t, &fakeCaller{}, "", nil, args...); r.code != ExitOK || !strings.Contains(r.stdout, "browser snapshot") ||
			!strings.Contains(r.stdout, "unless --full") || strings.Contains(r.stdout, "memory search") {
			t.Errorf("%v: %d\n%s", args, r.code, r.stdout)
		}
	}
	if r := runCLI(t, &fakeCaller{}, "", nil, "browser"); r.code != ExitUsage || !strings.Contains(r.stderr, "browser click <ref>") {
		t.Errorf("bare browser: %d\n%s", r.code, r.stderr)
	}
}

// pr checks exits 8 while a check runs, waits with --wait asking about the number it found,
// and exits 1 once one failed.
func TestCLIChecksWait(t *testing.T) {
	reads := 0
	f := &fakeCaller{reply: func(method string, _ json.RawMessage) (any, error) {
		if method != agentproto.MethodGitPullRequestChecks {
			return nil, errors.New("unexpected " + method)
		}
		reads++
		state := "pending"
		if reads >= 3 {
			state = "failure"
		}
		return map[string]any{"number": 9, "state": state, "checks": []map[string]any{{"name": "test", "state": state}}}, nil
	}}
	git := map[string]string{"rev-parse --abbrev-ref HEAD": "fix", "config --get-regexp ^remote\\..*\\.url$": "remote.origin.url https://" + agentproto.GitHost + "/github/o/r.git"}
	r := runCLI(t, f, "", git, "pr", "checks")
	if r.code != ExitPending || !strings.Contains(r.stdout, "state: pending") || !strings.Contains(r.stderr, "--wait") {
		t.Fatalf("code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if got := params(t, f.calls[0]); got["branch"] != "fix" || got["repository"] != "o/r" || got["number"] != nil {
		t.Fatalf("first call %v", got)
	}
	r = runCLI(t, f, "", git, "pr", "checks", "--wait")
	if r.code != ExitFailed || !strings.Contains(r.stdout, "state: failure") || reads != 3 {
		t.Fatalf("code %d, stdout %q, reads %d", r.code, r.stdout, reads)
	}
	if got := params(t, f.calls[len(f.calls)-1]); got["number"] != float64(9) {
		t.Fatalf("waiting asks about the number found: %v", got)
	}
	f.reply = func(string, json.RawMessage) (any, error) {
		return map[string]any{"number": 9, "state": "success", "checks": []any{}}, nil
	}
	if r = runCLI(t, f, "", git, "pr", "checks", "9"); r.code != ExitOK {
		t.Fatalf("passed: code %d", r.code)
	}
}

// An answer that waits for the user names `studio wait <id>`, which asks approval_status until
// the approval is settled: 0 when approved and done, 1 when denied or failed.
func TestCLIWaitForAnApproval(t *testing.T) {
	reads := 0
	outcome := map[string]any{"status": "posted", "url": "https://github.com/o/r/pull/9#review"}
	f := &fakeCaller{reply: func(method string, raw json.RawMessage) (any, error) {
		switch method {
		case agentproto.MethodGitPullRequestReview:
			return map[string]any{"status": "awaiting approval", "approval": "ap1", "pullRequest": "o/r#9"}, nil
		case agentproto.MethodApprovalStatus:
			reads++
			switch reads {
			case 1:
				return map[string]any{"approval": "ap1", "status": "pending", "settled": false}, nil
			case 2:
				// Approved, still posting.
				return map[string]any{"approval": "ap1", "status": "approved", "settled": false}, nil
			}
			return map[string]any{"approval": "ap1", "status": "approved", "settled": true, "result": outcome}, nil
		}
		return nil, errors.New("unexpected " + method)
	}}
	r := runCLI(t, f, "", nil, "pr", "review", "9", "--repository", "o/r", "--verdict", "comment", "--summary", "LGTM")
	if r.code != ExitWaiting || !strings.Contains(r.stderr, "studio wait ap1") {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	r = runCLI(t, f, "", nil, "wait", "ap1")
	if r.code != ExitOK || reads != 3 || !strings.Contains(r.stdout, "posted") {
		t.Fatalf("code %d, reads %d, stdout %q", r.code, reads, r.stdout)
	}
	if got := params(t, f.calls[len(f.calls)-1]); got["approval"] != "ap1" {
		t.Fatalf("asked %v", got)
	}
	outcome = map[string]any{"status": "failed", "reason": "403"}
	if r = runCLI(t, f, "", nil, "wait", "ap1"); r.code != ExitFailed {
		t.Fatalf("failed: code %d", r.code)
	}
	f.reply = func(string, json.RawMessage) (any, error) {
		return map[string]any{"approval": "ap1", "status": "denied", "settled": true}, nil
	}
	if r = runCLI(t, f, "", nil, "wait", "ap1"); r.code != ExitFailed {
		t.Fatalf("denied: code %d", r.code)
	}
}

// A call with no status tool of its own, as pr comment, waits through approval_status.
func TestCLICommentWaitsThroughApprovalStatus(t *testing.T) {
	reads := 0
	f := &fakeCaller{reply: func(method string, raw json.RawMessage) (any, error) {
		switch method {
		case agentproto.MethodGitPullRequestComment:
			return map[string]any{"status": "awaiting approval", "approval": "ap2", "pullRequest": "o/r#9"}, nil
		case agentproto.MethodApprovalStatus:
			reads++
			if reads == 1 {
				return map[string]any{"approval": "ap2", "status": "pending", "settled": false}, nil
			}
			return map[string]any{"approval": "ap2", "status": "approved", "settled": true,
				"result": map[string]any{"status": "posted", "url": "https://github.com/o/r/pull/9#issuecomment-1"}}, nil
		}
		return nil, errors.New("unexpected " + method)
	}}
	r := runCLI(t, f, "", nil, "pr", "comment", "9", "--repository", "o/r", "--body", "Ping", "--wait")
	if r.code != ExitOK || reads != 2 || !strings.Contains(r.stdout, "issuecomment-1") {
		t.Fatalf("code %d, reads %d, stdout %q, stderr %q", r.code, reads, r.stdout, r.stderr)
	}
	if got := params(t, f.calls[0]); got["body"] != "Ping" || got["number"] != float64(9) {
		t.Fatalf("asked %v", got)
	}
}
