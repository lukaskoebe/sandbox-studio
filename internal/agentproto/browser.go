package agentproto

import "encoding/json"

// KindBrowser (host → guest, browser VMs only): the host writes a BrowserCommand line; the
// guest runs agent-browser and replies with one BrowserResult line or an Error. Only Studio
// opens these streams; agents never reach a browser VM.
const KindBrowser = "browser"

// BrowserStreamPort is the guest loopback port of agent-browser's live-view WebSocket in
// a browser VM. Studio reaches it with KindTCP over the VM's channel, never through a
// network.
const BrowserStreamPort = 9223

// Limits of a browser command.
const (
	MaxBrowserArgs       = 16
	MaxBrowserArg        = 16 << 10
	MaxBrowserStdin      = 64 << 10
	MaxBrowserOutput     = 384 << 10 // agent-browser's JSON response
	MaxBrowserScreenshot = 256 << 10 // JPEG; base64 keeps the reply line under 1 MiB
	MaxBrowserTimeoutMS  = 60_000
)

// BrowserCommand is the body of a KindBrowser stream: one agent-browser command. The guest
// adds the fixed options (session, profile, JSON output) and refuses commands outside its
// allow-list.
type BrowserCommand struct {
	Args []string `json:"args"`
	// Stdin feeds `batch`, which keeps values such as credentials out of the process list.
	Stdin string `json:"stdin,omitempty"`
	// Screenshot asks for a JPEG of the viewport after the command, for the action log.
	Screenshot bool `json:"screenshot,omitempty"`
	TimeoutMS  int  `json:"timeoutMs,omitempty"`
}

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
)

// BrowserMethodPrefix starts every browser method.
const BrowserMethodPrefix = "browser_"
