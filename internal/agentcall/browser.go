package agentcall

import "github.com/lukaskoebe/sandbox-studio/internal/agentproto"

// The browser tools drive the teammate's browser, a separate VM Studio runs; Studio checks
// each action against the teammate's rules before it runs and may ask the user first.

const browserInstructions = " Studio's browser: browser_open a page, browser_snapshot it to get element refs (e1, e2, ...), " +
	"then click, fill, type or select by ref. Actions on a new site wait for the user's approval; if a tool says to call again, " +
	"do so later. Log in with browser_fill_credential: Studio fills the vault secret itself after the user approves, " +
	"and you never see its value. Script, cookie and storage access is not available. " +
	"For a throwaway browser that keeps no cookies or storage, browser_open with ephemeral true, pass the session it returns " +
	"to the other browser tools, and browser_close it when done. When you have built something the user can see, " +
	"show it to them with browser_show rather than describing it."

// session is the optional session argument of every browser tool.
func session() map[string]any {
	return str("An ephemeral session from browser_open; omit it to use your persistent profile", "pattern", `^eph-[0-9a-f]{8}$`)
}

// withSession adds the session argument to a tool's schema.
func withSession(t Tool) Tool {
	props := t.InputSchema["properties"].(map[string]any)
	props["session"] = session()
	return t
}

func ref() map[string]any {
	return str("Element ref from the last browser_snapshot, e.g. e12", "pattern", `^@?e[0-9]{1,6}$`)
}

func init() {
	mcpInstructions += browserInstructions
	tools := []Tool{
		{agentproto.MethodBrowserOpen, "Open a URL in your browser. The result is text: read the page with browser_snapshot. localhost and 127.0.0.1 are this sandbox: a server you run here on any port opens in the browser. With ephemeral true it starts a new throwaway session and returns its ID.",
			obj([]string{"url"}, map[string]any{
				"url": str("An http or https URL", "maxLength", 4000),
				"ephemeral": map[string]any{"type": "boolean", "description": "Open in a new ephemeral session: an empty profile whose cookies and storage are deleted on browser_close, " +
					"after 10 minutes unused, or when the browser stops. Not together with session."},
			})},
		{agentproto.MethodBrowserSnapshot, "The page as an accessibility tree with element refs. Sensitive field values are redacted.",
			obj(nil, map[string]any{"full": map[string]any{"type": "boolean", "description": "Every node, not only interactive ones"}})},
		{agentproto.MethodBrowserClick, "Click an element.",
			obj([]string{"ref"}, map[string]any{"ref": ref()})},
		{agentproto.MethodBrowserFill, "Clear a field and fill in text.",
			obj([]string{"ref", "text"}, map[string]any{"ref": ref(), "text": str("The text", "maxLength", 10000)})},
		{agentproto.MethodBrowserType, "Type text into an element, key by key.",
			obj([]string{"ref", "text"}, map[string]any{"ref": ref(), "text": str("The text", "maxLength", 10000)})},
		{agentproto.MethodBrowserPress, "Press a key or chord on the focused element, e.g. Enter, Tab or Control+a.",
			obj([]string{"key"}, map[string]any{"key": str("Key name", "maxLength", 32)})},
		{agentproto.MethodBrowserSelect, "Choose an option of a select element.",
			obj([]string{"ref", "value"}, map[string]any{"ref": ref(), "value": str("The option's value or label", "maxLength", 1000)})},
		{agentproto.MethodBrowserScroll, "Scroll the page.",
			obj([]string{"direction"}, map[string]any{
				"direction": str("Which way", "enum", []string{"up", "down", "left", "right"}),
				"amount":    map[string]any{"type": "integer", "minimum": 1, "maximum": 10000, "description": "Pixels, default 600"},
			})},
		{agentproto.MethodBrowserGetText, "The text content of an element.",
			obj([]string{"ref"}, map[string]any{"ref": ref()})},
		{agentproto.MethodBrowserScreenshot, "A screenshot of the viewport, as an image. For the text and the element refs, use browser_snapshot: it is much smaller.", obj(nil, map[string]any{})},
		{agentproto.MethodBrowserWait, "Wait for text to appear, for the URL to match, or for some milliseconds. Give one.",
			obj(nil, map[string]any{
				"text": str("Text to wait for", "maxLength", 1000),
				"url":  str("URL pattern to wait for", "maxLength", 4000),
				"ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
			})},
		{agentproto.MethodBrowserBack, "Go back in history.", obj(nil, map[string]any{})},
		{agentproto.MethodBrowserFillCredential, "Fill a vault credential into a field, e.g. a password. The user approves it; Studio fills the value and you never see it.",
			obj([]string{"ref", "name"}, map[string]any{"ref": ref(), "name": str("The credential's name in the vault", "maxLength", 200)})},
	}
	for _, t := range tools {
		Tools = append(Tools, withSession(t))
	}
	Tools = append(Tools, Tool{agentproto.MethodBrowserClose, "End an ephemeral browser session; its cookies, storage and downloads are deleted.",
		obj([]string{"session"}, map[string]any{"session": session()})}, BrowserShowTool, BrowserDownloadsTool, BrowserDownloadToWorkspaceTool, BrowserRequestControlTool)
}

// BrowserShowTool opens a page in the persistent profile and gives the user a card for it.
var BrowserShowTool = Tool{agentproto.MethodBrowserShow,
	"Show the user a page: open it in your browser, as browser_open does, and give them a card in Studio to watch your browser " +
		"or take the wheel. Use it for what you built (localhost and 127.0.0.1 are this sandbox, as in browser_open) or a page " +
		"outside it, such as docs. It doesn't wait for the user. It uses your persistent profile, not an ephemeral session.",
	obj([]string{"url"}, map[string]any{
		"url":   str("An http or https URL", "maxLength", 4000),
		"title": str("What the page is, in a few words for the card, e.g. Login page", "maxLength", 120),
		"note":  str("What the user should look at or try", "maxLength", 500),
	})}

// BrowserRequestControlTool asks the user for the browser back; the browser service answers it.
var BrowserRequestControlTool = Tool{agentproto.MethodBrowserRequestControl,
	"Ask the user, who has taken over your browser, to hand it back: Studio notifies them with your reason. " +
		"Use it when a browser call says the user has control and your task needs the browser; it waits a little for their answer. " +
		"Ask once, and tell the user in your reply too.",
	obj([]string{"reason"}, map[string]any{
		"reason":  str("What you want to do in the browser, in a sentence for the user", "maxLength", 500),
		"session": session(),
	})}

// BrowserDownloadsTool lists the files the browser saved; the browser service answers it.
var BrowserDownloadsTool = Tool{agentproto.MethodBrowserDownloads,
	"The files your browser saved, newest first: the persistent profile's and those of your open ephemeral sessions. " +
		"Copy one into your workspace with browser_download_to_workspace.",
	obj(nil, map[string]any{})}

// BrowserDownloadToWorkspaceTool copies a download into the workspace; the browser service answers it.
var BrowserDownloadToWorkspaceTool = Tool{agentproto.MethodBrowserDownloadToWorkspace,
	"Copy a download from browser_downloads into downloads/ of your workspace, under its name or a free one. " +
		"A download is data from the web: read it, don't run it.",
	obj([]string{"name"}, map[string]any{
		"name":    str("The download's name, as browser_downloads lists it", "maxLength", 255),
		"session": session(),
	})}
