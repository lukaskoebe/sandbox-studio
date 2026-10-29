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
	"syscall"
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
	// Ephemeral sessions keep their profile and downloads on a tmpfs the guest mounts,
	// never on /workspace: they vanish on close, and at the latest when the VM stops.
	browserEphemeralRoot = "/run/studio-browser"
	browserEphemeralSize = "size=640m"
	tmpfsMagic           = 0x01021994
	// browserStealthChromium starts Chromium in stealth mode; the guest writes it, so it
	// works in browser VMs made from any image.
	browserStealthChromium = browserEphemeralRoot + "/chromium-stealth"
)

// browserStealthScript is Chromium with what stealth mode changes (docs/browser.md#stealth-mode).
// Chromium's own switches, not agent-browser's options: agent-browser splits --args at
// commas, which a user agent has, and its --user-agent override drops the client hints, so
// the page would see a Chrome without brands. The user agent is the one desktop Chromium
// sends on Linux, on any CPU; the screen switch comes after agent-browser's, so it wins.
const browserStealthScript = `#!/bin/sh
# Written by studio-agent: Chromium in stealth mode (docs/browser.md#stealth-mode).
major=$(/usr/bin/chromium --version 2>/dev/null | grep -o '[0-9][0-9]*\.' | head -n 1 | tr -d .)
exec /usr/bin/chromium --disable-blink-features=AutomationControlled \
	"--user-agent=Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${major:-140}.0.0.0 Safari/537.36" \
	"$@" --ozone-override-screen-size=1920,1080
`

// browserSlot is where one agent-browser session keeps its state.
type browserSlot struct {
	session, profile, downloads string
	port                        int
	ephemeral                   bool
	stealth                     bool // a launch option, the same on every command of the session
}

// browserSlotFor is the persistent profile for 0 and ephemeral slot n otherwise.
func browserSlotFor(n int) browserSlot {
	if n == 0 {
		return browserSlot{session: browserSession, profile: browserProfile, downloads: browserDownload, port: agentproto.BrowserStreamPort}
	}
	dir := filepath.Join(browserEphemeralRoot, "e"+strconv.Itoa(n))
	return browserSlot{session: browserSession + "-e" + strconv.Itoa(n), profile: filepath.Join(dir, "profile"),
		downloads: filepath.Join(dir, "downloads"), port: agentproto.BrowserStreamPort + n, ephemeral: true}
}

// dir is an ephemeral slot's directory, which holds its profile and downloads.
func (b browserSlot) dir() string { return filepath.Dir(b.profile) }

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
	"set":        nil,
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
	if args[0] == "set" && (batch || !viewportArgs(args)) {
		return errors.New("only set viewport, with a width and a height, is allowed")
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

// viewportArgs reports whether args are set viewport <width> <height>, the size a live view
// fits the page to: whole pixels of a screen, with no scale, which would be a phone's.
func viewportArgs(args []string) bool {
	if len(args) != 4 || args[1] != "viewport" {
		return false
	}
	for _, a := range args[2:] {
		if n, err := strconv.Atoi(a); err != nil || n < 1 || n > 10000 || strconv.Itoa(n) != a {
			return false
		}
	}
	return true
}

// checkBrowserSession picks the command's session. close is allowed only as the whole
// command on an ephemeral slot, where it ends the session; the profile is never closed.
func checkBrowserSession(req agentproto.BrowserCommand) (browserSlot, bool, error) {
	if req.Ephemeral < 0 || req.Ephemeral > agentproto.MaxBrowserEphemeral || (req.Fresh && req.Ephemeral == 0) {
		return browserSlot{}, false, errors.New("bad browser session")
	}
	slot := browserSlotFor(req.Ephemeral)
	slot.stealth = req.Stealth
	closing := slot.ephemeral && len(req.Args) == 1 && req.Args[0] == "close" && !req.Fresh && req.Stdin == "" && !req.Screenshot
	return slot, closing, nil
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

// browserArgv adds the fixed options to a command. Each slot is its own agent-browser
// session: a daemon with its own Chromium and profile. Stealth mode starts Chromium through
// browserStealthChromium, and the other executable makes agent-browser restart the browser
// when the mode changes; its pointer moves to a click the way a hand does.
func browserArgv(slot browserSlot, args []string) []string {
	chromium := browserChromium
	if slot.stealth {
		chromium = browserStealthChromium
	}
	argv := []string{"--json", "--session", slot.session, "--profile", slot.profile,
		"--executable-path", chromium, "--download-path", slot.downloads}
	if slot.stealth {
		argv = append(argv, "--input-mode", "human")
	}
	return append(argv, args...)
}

// writeStealthChromium puts browserStealthChromium in place, owned by root so the browser
// user cannot change what it runs.
func writeStealthChromium() error {
	if err := ensureTmpfs(browserEphemeralRoot); err != nil {
		return fmt.Errorf("stealth browser: %w", err)
	}
	if b, err := os.ReadFile(browserStealthChromium); err == nil && string(b) == browserStealthScript {
		return nil
	}
	tmp := browserStealthChromium + ".tmp"
	if err := os.WriteFile(tmp, []byte(browserStealthScript), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, browserStealthChromium)
}

// browserEnv is added to the agent user's environment.
func browserEnv(slot browserSlot) []string {
	return []string{
		"AGENT_BROWSER_STREAM_PORT=" + strconv.Itoa(slot.port),
		"AGENT_BROWSER_NO_WEBMCP=1",
		"AGENT_BROWSER_IDLE_TIMEOUT_MS=0", // Studio stops the whole VM, and ephemeral sessions, when idle
	}
}

func (a *Agent) browser(st net.Conn, br *bufio.Reader) error {
	var req agentproto.BrowserCommand
	if err := readBody(br, agentproto.MaxCallLine, &req); err != nil {
		return replyError(st, err)
	}
	slot, closing, err := checkBrowserSession(req)
	if err != nil {
		return replyError(st, err)
	}
	if closing {
		if err := a.endEphemeral(slot); err != nil {
			return replyError(st, err)
		}
		return agentproto.WriteJSONLine(st, agentproto.BrowserResult{Output: json.RawMessage(`{"success":true,"data":null,"error":null}`)})
	}
	args := req.Args
	if len(args) > 0 && args[0] == agentproto.BrowserElementAt {
		// Studio's own lookup for a note runs a fixed script, not a command of the
		// allow-list (browser_element.go).
		if args, err = elementAtArgs(req); err != nil {
			return replyError(st, err)
		}
	} else if err := checkBrowserArgs(args, false); err != nil {
		return replyError(st, err)
	}
	if args[0] == "batch" {
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
	switch {
	case req.Fresh:
		if err := a.endEphemeral(slot); err != nil {
			return replyError(st, err)
		}
		if err := a.beginEphemeral(slot); err != nil {
			return replyError(st, err)
		}
	case slot.ephemeral:
		// Only Fresh creates an ephemeral profile: a missing one means the session ended,
		// and a command must not quietly start an empty browser in its place.
		if _, err := os.Stat(slot.profile); err != nil {
			return replyError(st, errors.New(agentproto.ErrBrowserEphemeralGone))
		}
	default:
		for _, dir := range []string{browserProfile, browserDownload} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return replyError(st, err)
			}
			if err := os.Chown(dir, int(a.user.uid), int(a.user.gid)); err != nil {
				return replyError(st, err)
			}
		}
	}
	out, err := a.runBrowser(ctx, slot, args, req.Stdin)
	if err != nil {
		return replyError(st, err)
	}
	res := agentproto.BrowserResult{Output: out}
	if req.Screenshot {
		res.Screenshot = a.browserScreenshot(ctx, slot)
	}
	return agentproto.WriteJSONLine(st, res)
}

// beginEphemeral creates an empty profile for an ephemeral slot on the tmpfs.
func (a *Agent) beginEphemeral(slot browserSlot) error {
	if err := ensureTmpfs(browserEphemeralRoot); err != nil {
		return fmt.Errorf("ephemeral browser storage: %w", err)
	}
	for _, dir := range []string{slot.dir(), slot.profile, slot.downloads} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chown(dir, int(a.user.uid), int(a.user.gid)); err != nil {
			return err
		}
	}
	return nil
}

// endEphemeral closes an ephemeral slot's agent-browser session, makes sure no process of
// it survives, and deletes its profile and downloads.
func (a *Agent) endEphemeral(slot browserSlot) error {
	// agent-browser keeps a session's socket, pid and stream port files here.
	sockets := filepath.Join(a.user.home, ".agent-browser")
	if _, err := os.Stat(filepath.Join(sockets, slot.session+".sock")); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = a.runBrowserTrusted(ctx, slot, []string{"close"}, "") // the daemon may already be gone
		cancel()
	}
	killSession(slot)
	for _, ext := range []string{".sock", ".pid", ".stream", ".version", ".engine"} {
		os.Remove(filepath.Join(sockets, slot.session+ext))
	}
	if err := os.RemoveAll(slot.dir()); err != nil {
		return fmt.Errorf("deleting the ephemeral profile: %w", err)
	}
	return nil
}

// ensureTmpfs makes dir a tmpfs mount point unless it already is one.
func ensureTmpfs(dir string) error {
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err == nil && fs.Type == tmpfsMagic {
		return nil
	}
	if err := syscall.Mount("tmpfs", dir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, browserEphemeralSize+",mode=0711"); err != nil {
		return err
	}
	if err := syscall.Statfs(dir, &fs); err != nil || fs.Type != tmpfsMagic {
		return errors.New("not a tmpfs")
	}
	return nil
}

// killSession kills what an agent-browser session runs: its daemon, which carries the
// session in AGENT_BROWSER_SESSION, and the Chromium processes it started, which inherit
// that environment and name the profile in --user-data-dir.
func killSession(slot browserSlot) {
	env := "\x00AGENT_BROWSER_SESSION=" + slot.session + "\x00"
	arg := "\x00--user-data-dir=" + slot.profile + "\x00"
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		environ, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if strings.Contains("\x00"+string(environ), env) || strings.Contains("\x00"+string(cmdline), arg) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// runBrowser runs agent-browser, after the policies and the CA are in place, and returns its
// JSON response. A failed command exits
// non-zero and still prints a response with success false, which is returned as is. The
// stdin and the arguments are never logged: a batch may carry a credential.
func (a *Agent) runBrowser(ctx context.Context, slot browserSlot, args []string, stdin string) (json.RawMessage, error) {
	prepare := func(ctx context.Context) error {
		if err := writeBrowserPolicies(browserPolicyFile); err != nil {
			return err
		}
		if slot.stealth {
			if err := writeStealthChromium(); err != nil {
				return err
			}
		}
		return a.prepareBrowserTrust(ctx)
	}
	return runBrowserAfterTrust(ctx, prepare, func(ctx context.Context) (json.RawMessage, error) {
		return a.runBrowserTrusted(ctx, slot, args, stdin)
	})
}

// runBrowserAfterTrust keeps browser launch behind trust preparation. The injected run
// function also lets tests verify that a trust failure never starts agent-browser.
func runBrowserAfterTrust(ctx context.Context, prepare func(context.Context) error, run func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	if err := prepare(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("browser command context ended before agent-browser launch: %w", err)
	}
	return run(ctx)
}

// runBrowserTrusted runs agent-browser after the browser user's NSS database is ready.
func (a *Agent) runBrowserTrusted(ctx context.Context, slot browserSlot, args []string, stdin string) (json.RawMessage, error) {
	cmd := a.command(browserBin, browserArgv(slot, args)...)
	cmd.Env = append(cmd.Env, browserEnv(slot)...)
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
func (a *Agent) browserScreenshot(ctx context.Context, slot browserSlot) []byte {
	var b [8]byte
	rand.Read(b[:])
	path := filepath.Join(os.TempDir(), "studio-shot-"+hex.EncodeToString(b[:])+".jpg")
	defer os.Remove(path)
	if _, err := a.runBrowser(ctx, slot, []string{"screenshot", path, "--screenshot-format", "jpeg", "--screenshot-quality", "60"}, ""); err != nil {
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
