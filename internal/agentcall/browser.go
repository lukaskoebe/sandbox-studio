package agentcall

import "github.com/lukaskoebe/sandbox-studio/internal/agentproto"

// The browser tools drive the persona's browser, a separate VM Studio runs; Studio checks
// each action against the persona's rules before it runs and may ask the user first.

const browserInstructions = " Studio's browser: browser_open a page, browser_snapshot it to get element refs (e1, e2, ...), " +
	"then click, fill, type or select by ref. Actions on a new site wait for the user's approval; if a tool says to call again, " +
	"do so later. Log in with browser_fill_credential: Studio fills the vault secret itself after the user approves, " +
	"and you never see its value. Script, cookie and storage access is not available."

func ref() map[string]any {
	return str("Element ref from the last browser_snapshot, e.g. e12", "pattern", `^@?e[0-9]{1,6}$`)
}

func init() {
	mcpInstructions += browserInstructions
	Tools = append(Tools,
		Tool{agentproto.MethodBrowserOpen, "Open a URL in your browser.",
			obj([]string{"url"}, map[string]any{"url": str("An http or https URL", "maxLength", 4000)})},
		Tool{agentproto.MethodBrowserSnapshot, "The page as an accessibility tree with element refs. Sensitive field values are redacted.",
			obj(nil, map[string]any{"full": map[string]any{"type": "boolean", "description": "Every node, not only interactive ones"}})},
		Tool{agentproto.MethodBrowserClick, "Click an element.",
			obj([]string{"ref"}, map[string]any{"ref": ref()})},
		Tool{agentproto.MethodBrowserFill, "Clear a field and fill in text.",
			obj([]string{"ref", "text"}, map[string]any{"ref": ref(), "text": str("The text", "maxLength", 10000)})},
		Tool{agentproto.MethodBrowserType, "Type text into an element, key by key.",
			obj([]string{"ref", "text"}, map[string]any{"ref": ref(), "text": str("The text", "maxLength", 10000)})},
		Tool{agentproto.MethodBrowserPress, "Press a key or chord on the focused element, e.g. Enter, Tab or Control+a.",
			obj([]string{"key"}, map[string]any{"key": str("Key name", "maxLength", 32)})},
		Tool{agentproto.MethodBrowserSelect, "Choose an option of a select element.",
			obj([]string{"ref", "value"}, map[string]any{"ref": ref(), "value": str("The option's value or label", "maxLength", 1000)})},
		Tool{agentproto.MethodBrowserScroll, "Scroll the page.",
			obj([]string{"direction"}, map[string]any{
				"direction": str("Which way", "enum", []string{"up", "down", "left", "right"}),
				"amount":    map[string]any{"type": "integer", "minimum": 1, "maximum": 10000, "description": "Pixels, default 600"},
			})},
		Tool{agentproto.MethodBrowserGetText, "The text content of an element.",
			obj([]string{"ref"}, map[string]any{"ref": ref()})},
		Tool{agentproto.MethodBrowserScreenshot, "A screenshot of the viewport.", obj(nil, map[string]any{})},
		Tool{agentproto.MethodBrowserWait, "Wait for text to appear, for the URL to match, or for some milliseconds. Give one.",
			obj(nil, map[string]any{
				"text": str("Text to wait for", "maxLength", 1000),
				"url":  str("URL pattern to wait for", "maxLength", 4000),
				"ms":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
			})},
		Tool{agentproto.MethodBrowserBack, "Go back in history.", obj(nil, map[string]any{})},
		Tool{agentproto.MethodBrowserFillCredential, "Fill a vault credential into a field, e.g. a password. The user approves it; Studio fills the value and you never see it.",
			obj([]string{"ref", "name"}, map[string]any{"ref": ref(), "name": str("The credential's name in the vault", "maxLength", 200)})},
	)
}
