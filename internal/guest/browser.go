//go:build linux

package guest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The browser VM runs agent-browser (images/browser) as the agent user. Studio is its only
// client: commands arrive on the KindBrowser stream, the live view through KindTCP to
// agentproto.BrowserStreamPort. Nothing in the VM listens beyond loopback.
const (
	browserBin      = "/usr/local/bin/agent-browser"
	browserChromium = "/usr/bin/chromium"
	browserProfile  = Workdir + "/browser-profile" // on the owned disk, so logins persist
	browserDownload = Workdir + "/downloads"
	browserSession  = "studio"
	browserTimeout  = 30 * time.Second
)

// browserCommands are the agent-browser commands Studio may run, with the flags each may
// carry. Studio's broker builds every command itself; the list is a second line of defense
// that keeps script evaluation, network interception, cookie and storage access, state
// export and CDP out of reach even if the host were confused.
var browserCommands = map[string][]string{
	"open": nil, "snapshot": {"-i", "--interactive", "-c", "--compact", "-d", "--depth"},
	"click": nil, "fill": nil, "type": nil, "press": nil, "select": nil, "scroll": nil,
	"wait": {"--text", "-t", "--url", "-u", "--load", "-l"}, "back": nil, "forward": nil, "reload": nil,
	"get": nil, "upload": nil, "download": nil,
	"screenshot": {"--screenshot-format", "--screenshot-quality"},
}

// browserGets are the `get` subcommands allowed; `get value` is not, nor html or cdp-url.
var browserGets = []string{"text", "attr", "url", "title"}

// checkBrowserArgs refuses a command outside the allow-list.
func checkBrowserArgs(args []string, batch bool) error {
	if len(args) == 0 || len(args) > agentproto.MaxBrowserArgs {
		return errors.New("bad browser command")
	}
	for _, a := range args {
		if len(a) > agentproto.MaxBrowserArg || strings.ContainsRune(a, 0) {
			return errors.New("browser argument too long")
		}
	}
	if args[0] == "batch" && !batch {
		if len(args) != 1 {
			return errors.New("batch takes its commands on stdin")
		}
		return nil
	}
	flags, ok := browserCommands[args[0]]
	if !ok {
		return fmt.Errorf("browser command %q is not allowed", args[0])
	}
	if args[0] == "get" && (len(args) < 2 || !slices.Contains(browserGets, args[1])) {
		return errors.New("only get text, attr, url and title are allowed")
	}
	for i, a := range args[1:] {
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		// Values of an allowed flag, such as a quality or a URL glob, may start with a dash.
		if i > 0 && slices.Contains(flags, args[i]) {
			continue
		}
		if !slices.Contains(flags, a) {
			return fmt.Errorf("browser flag %q is not allowed", a)
		}
	}
	return nil
}

// checkBrowserBatch checks every command of a batch.
func checkBrowserBatch(stdin string) error {
	var cmds [][]string
	if err := json.Unmarshal([]byte(stdin), &cmds); err != nil {
		return errors.New("batch stdin is not a list of commands")
	}
	if len(cmds) == 0 || len(cmds) > 64 {
		return errors.New("a batch has 1 to 64 commands")
	}
	for _, c := range cmds {
		if err := checkBrowserArgs(c, true); err != nil {
			return err
		}
	}
	return nil
}

// browserArgv adds the fixed options to a command.
func browserArgv(args []string) []string {
	argv := []string{"--json", "--session", browserSession, "--profile", browserProfile,
		"--executable-path", browserChromium, "--download-path", browserDownload}
	// Chromium on Linux keeps its own trust store; agent-browser imports the environment's
	// CA into it so intercepted connections verify. verify (live)
	if _, err := os.Stat(caFile); err == nil {
		argv = append(argv, "--ca-cert", caFile)
	}
	return append(argv, args...)
}

// browserEnv is added to the agent user's environment.
var browserEnv = []string{
	"AGENT_BROWSER_STREAM_PORT=" + strconv.Itoa(agentproto.BrowserStreamPort),
	"AGENT_BROWSER_NO_WEBMCP=1",
	"AGENT_BROWSER_IDLE_TIMEOUT_MS=0", // Studio stops the whole VM when idle
}

func (a *Agent) browser(st net.Conn, br *bufio.Reader) error {
	var req agentproto.BrowserCommand
	if err := readBody(br, agentproto.MaxCallLine, &req); err != nil {
		return replyError(st, err)
	}
	if err := checkBrowserArgs(req.Args, false); err != nil {
		return replyError(st, err)
	}
	if req.Args[0] == "batch" {
		if len(req.Stdin) > agentproto.MaxBrowserStdin {
			return replyError(st, errors.New("batch too large"))
		}
		if err := checkBrowserBatch(req.Stdin); err != nil {
			return replyError(st, err)
		}
	} else if req.Stdin != "" {
		return replyError(st, errors.New("only batch reads stdin"))
	}
	timeout := browserTimeout
	if req.TimeoutMS > 0 && req.TimeoutMS <= agentproto.MaxBrowserTimeoutMS {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, dir := range []string{browserProfile, browserDownload} {
		os.MkdirAll(dir, 0o700)
		os.Chown(dir, int(a.user.uid), int(a.user.gid))
	}
	out, err := a.runBrowser(ctx, req.Args, req.Stdin)
	if err != nil {
		return replyError(st, err)
	}
	res := agentproto.BrowserResult{Output: out}
	if req.Screenshot {
		res.Screenshot = a.browserScreenshot(ctx)
	}
	return agentproto.WriteJSONLine(st, res)
}

// runBrowser runs agent-browser and returns its JSON response. A failed command exits
// non-zero and still prints a response with success false, which is returned as is. The
// stdin and the arguments are never logged: a batch may carry a credential.
func (a *Agent) runBrowser(ctx context.Context, args []string, stdin string) (json.RawMessage, error) {
	cmd := a.command(browserBin, browserArgv(args)...)
	cmd.Env = append(cmd.Env, browserEnv...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr capped
	stdout.max, stderr.max = agentproto.MaxBrowserOutput, 4<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agent-browser: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return nil, errors.New("the browser command timed out")
	}
	if stdout.over {
		return nil, errors.New("the browser's output is too large")
	}
	out := bytes.TrimSpace(stdout.buf.Bytes())
	if json.Valid(out) && len(out) > 0 {
		return out, nil
	}
	msg := strings.TrimSpace(stderr.buf.String())
	if msg == "" && runErr != nil {
		msg = runErr.Error()
	}
	return nil, fmt.Errorf("agent-browser failed: %s", msg)
}

// browserScreenshot takes a JPEG of the viewport, or nil if that fails or it is too large.
func (a *Agent) browserScreenshot(ctx context.Context) []byte {
	var b [8]byte
	rand.Read(b[:])
	path := filepath.Join(os.TempDir(), "studio-shot-"+hex.EncodeToString(b[:])+".jpg")
	defer os.Remove(path)
	if _, err := a.runBrowser(ctx, []string{"screenshot", path, "--screenshot-format", "jpeg", "--screenshot-quality", "60"}, ""); err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > agentproto.MaxBrowserScreenshot {
		return nil
	}
	return data
}

// capped keeps the first max bytes written and notes whether there were more.
type capped struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room < len(p) {
		c.over = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}
