package agentproto

import "encoding/json"

// KindBrowser (host → guest, browser VMs only): the host writes a BrowserCommand line; the
// guest runs agent-browser and replies with one BrowserResult line or an Error. Only Studio
// opens these streams; agents never reach a browser VM.
const KindBrowser = "browser"

// BrowserStreamPort is the guest loopback port of agent-browser's live-view WebSocket in
// a browser VM. Studio reaches it with KindTCP over the VM's channel, never through a
// network. Ephemeral session n streams on BrowserStreamPort+n.
const BrowserStreamPort = 9223

// MaxBrowserEphemeral is how many ephemeral sessions a browser VM runs beside its
// persistent profile. Each is its own agent-browser session with a throwaway profile on
// the VM's tmpfs (docs/browser.md#ephemeral-sessions).
const MaxBrowserEphemeral = 2

// ErrBrowserEphemeralGone is the guest's error for a command on an ephemeral session whose
// profile no longer exists, for example because the VM restarted.
const ErrBrowserEphemeralGone = "the ephemeral browser session is gone"

// Limits of a browser command.
const (
	MaxBrowserArgs   = 16
	MaxBrowserArg    = 16 << 10
	MaxBrowserStdin  = 64 << 10
	MaxBrowserOutput = 384 << 10 // agent-browser's JSON response
	// MaxBrowserScreenshot is a JPEG. Base64 and the output of a command with a screenshot
	// keep its reply line within one stream window of 256 KiB: a longer reply waits seconds
	// for the host's window update on msb's vsock (docs/guest-agent.md).
	MaxBrowserScreenshot = 180 << 10
	MaxBrowserTimeoutMS  = 60_000
)

// BrowserCommand is the body of a KindBrowser stream: one agent-browser command. The guest
// adds the fixed options (session, profile, JSON output) and refuses commands outside its
// allow-list.
type BrowserCommand struct {
	Args []string `json:"args"`
	// Ephemeral picks the session: 0 is the persistent profile, 1 to MaxBrowserEphemeral an
	// ephemeral slot. On a slot, Args ["close"] ends the session and deletes its profile.
	Ephemeral int `json:"ephemeral,omitempty"`
	// Fresh, on an ephemeral slot, ends whatever ran there and starts an empty profile
	// before the command runs.
	Fresh bool `json:"fresh,omitempty"`
	// Stdin feeds `batch`, which keeps values such as credentials out of the process list.
	Stdin string `json:"stdin,omitempty"`
	// Screenshot asks for a JPEG of the viewport after the command, for the action log.
	Screenshot bool `json:"screenshot,omitempty"`
	TimeoutMS  int  `json:"timeoutMs,omitempty"`
	// Stealth runs the session's Chromium in stealth mode (docs/browser.md#stealth-mode). It
	// is a launch option, so it must be the same on every command of a session, close
	// included: agent-browser restarts a browser whose launch options changed.
	Stealth bool `json:"stealth,omitempty"`
}

// BrowserElementAt is the one command the guest runs that isn't agent-browser's own: Args
// [BrowserElementAt, x, y] describe the element at (x, y), in CSS pixels of the page's
// viewport, for a note the user pins on the live view. The guest evaluates its own fixed,
// read-only script there; the host passes only the two numbers (docs/browser.md).
const BrowserElementAt = "element-at"

// BrowserResult is agent-browser's JSON response and the optional screenshot.
type BrowserResult struct {
	Output     json.RawMessage `json:"output"`
	Screenshot []byte          `json:"screenshot,omitempty"`
}

// Browser tool methods: the calls `studio-agent mcp` forwards for the browser tools. Like
// every call they carry only tool arguments; Studio knows the caller from the channel.
const (
	MethodBrowserOpen           = "browser_open"
	MethodBrowserSnapshot       = "browser_snapshot"
	MethodBrowserClick          = "browser_click"
	MethodBrowserFill           = "browser_fill"
	MethodBrowserType           = "browser_type"
	MethodBrowserPress          = "browser_press"
	MethodBrowserSelect         = "browser_select"
	MethodBrowserScroll         = "browser_scroll"
	MethodBrowserGetText        = "browser_get_text"
	MethodBrowserScreenshot     = "browser_screenshot"
	MethodBrowserWait           = "browser_wait"
	MethodBrowserBack           = "browser_back"
	MethodBrowserFillCredential = "browser_fill_credential"
	MethodBrowserClose          = "browser_close"
	MethodBrowserShow           = "browser_show"
)

// BrowserMethodPrefix starts every browser method.
const BrowserMethodPrefix = "browser_"

// MethodBrowserDownloads and MethodBrowserDownloadToWorkspace are the download tools: the
// files a browser saved, and copying one into the calling sandbox (docs/browser.md#downloads).
const (
	MethodBrowserDownloads           = "browser_downloads"
	MethodBrowserDownloadToWorkspace = "browser_download_to_workspace"
	// MethodBrowserRequestControl asks the user, who drives the browser, to hand it back.
	MethodBrowserRequestControl = "browser_request_control"
)

// KindBrowserDownloads (host → guest, browser VMs only): the host writes a
// BrowserDownloadsRequest line. Without a name the guest replies with one BrowserDownloadList
// line. With a name it replies with one BrowserDownload line and then exactly the file's Size
// bytes; or with an Error.
const KindBrowserDownloads = "browser-downloads"

// MaxBrowserDownloads bounds a listing of a session's downloads, newest first.
const MaxBrowserDownloads = 100

// BrowserDownloadsRequest names a download of one browser session: Ephemeral as in
// BrowserCommand. Name is the file to read, or empty to list the session's downloads.
type BrowserDownloadsRequest struct {
	Ephemeral int    `json:"ephemeral,omitempty"`
	Name      string `json:"name,omitempty"`
}

// BrowserDownload describes a file a browser saved. Modified is unix seconds.
type BrowserDownload struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// BrowserDownloadList is the reply to a listing request.
type BrowserDownloadList struct {
	Files []BrowserDownload `json:"files"`
}

// KindForwards (host → guest, browser VMs only): the host writes a Forwards line; the guest
// listens on loopback at exactly those ports, closing the listeners of ports no longer named,
// and replies {} or an Error. Each connection to one of them becomes a KindForward stream.
// This is how localhost in the browser reaches the sandbox that drives it
// (docs/browser.md#localhost).
const KindForwards = "forwards"

// KindForward (guest → host, browser VMs only): a connection to a forwarded loopback port,
// named in Header.Port. The host replies with one line, {} or an Error; after {} the stream
// carries the connection's bytes to the same port in the driving sandbox.
const KindForward = "forward"

// MaxForwards bounds the ports one Forwards names.
const MaxForwards = 64

// Forwards is the body of a KindForwards stream.
type Forwards struct {
	Ports []int `json:"ports"`
}

// BrowserReservedPort reports whether port is one a browser VM uses itself, which a forward
// never takes: agent-browser's live-view WebSockets and Chromium's DevTools.
func BrowserReservedPort(port int) bool {
	return port >= BrowserStreamPort-1 && port <= BrowserStreamPort+MaxBrowserEphemeral
}
