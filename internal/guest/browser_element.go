//go:build linux

package guest

import (
	"encoding/base64"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A note the user pins on the live view names the element under it, so that an agent that
// can't see the screenshot still knows what the note is about. agent-browser 0.39 has no hit
// test among the commands Studio allows, and a snapshot would renumber the element refs the
// agents' calls and the policy rely on. Its eval runs a script in the page without touching
// those refs, but eval stays off the allow-list (browserCommands): the host sends only
// [BrowserElementAt, x, y], and the script is this file's, fixed. A confused host can learn
// no more than the script returns, and agents never reach the browser VM at all.

// maxElementAt bounds a coordinate, in CSS pixels; a viewport is far smaller.
const maxElementAt = 100_000

// elementAtArgs turns the host's [BrowserElementAt, x, y] into the agent-browser command that
// runs elementAtScript at that point. The script goes base64 encoded, so no part of it can
// read as one of agent-browser's own options.
func elementAtArgs(req agentproto.BrowserCommand) ([]string, error) {
	if len(req.Args) != 3 || req.Args[0] != agentproto.BrowserElementAt || req.Stdin != "" || req.Fresh || req.Screenshot {
		return nil, errors.New("element-at takes the x and y of a point")
	}
	var at [2]string
	for i, s := range req.Args[1:] {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxElementAt {
			return nil, errors.New("element-at takes the x and y of a point in CSS pixels")
		}
		at[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	script := strings.NewReplacer("__X__", at[0], "__Y__", at[1]).Replace(elementAtScript)
	return []string{"eval", "-b", base64.StdEncoding.EncodeToString([]byte(script))}, nil
}

// elementAtScript describes the element at (__X__, __Y__) in the page's viewport: its tag,
// role, accessible name, ID, a short CSS path, its box and up to 300 characters of the text
// it shows. It only reads the page. It never reads what a form field holds: an input, a
// textarea or a select, which may be a login filled from the vault, has its label, placeholder,
// name and type instead, and the text of a container leaves out the fields in it. The value
// of a button-like input is the button's label, which the page shows.
const elementAtScript = `(() => {
  const x = __X__, y = __Y__;
  const clip = (s, n) => {
    s = String(s == null ? "" : s).replace(/\s+/g, " ").trim();
    return s.length > n ? s.slice(0, n - 1) + "…" : s;
  };
  let el = document.elementFromPoint(x, y);
  if (!el) return null;
  // The element under the point may be inside open shadow roots.
  for (let i = 0; i < 16 && el.shadowRoot; i++) {
    const inner = el.shadowRoot.elementFromPoint(x, y);
    if (!inner || inner === el) break;
    el = inner;
  }
  const tag = el.localName;
  const fieldTags = ["input", "textarea", "select"];
  const field = fieldTags.includes(tag) || el.isContentEditable;
  const skip = new Set(["textarea", "select", "option", "datalist", "script", "style", "noscript", "template"]);
  // The text an element shows, without what a field in it holds. It looks at 5000 nodes at
  // most, so a click on a large page's body answers quickly.
  const textOf = (root, n) => {
    if (!root) return "";
    const parts = [];
    let length = 0, visited = 0;
    const enough = {};
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
      acceptNode: (node) => {
        if (++visited > 5000) throw enough;
        if (node.nodeType === Node.TEXT_NODE) return NodeFilter.FILTER_ACCEPT;
        if (skip.has(node.localName) || node.isContentEditable) return NodeFilter.FILTER_REJECT;
        if (node.checkVisibility && !node.checkVisibility({ visibilityProperty: true })) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_SKIP;
      },
    });
    try {
      for (let node; length < n && (node = walker.nextNode()); ) {
        const t = node.nodeValue.replace(/\s+/g, " ");
        if (t.trim()) {
          parts.push(t);
          length += t.length;
        }
      }
    } catch (e) {
      if (e !== enough) throw e;
    }
    return clip(parts.join(""), n);
  };
  const type = tag === "input" ? (el.getAttribute("type") || "text").toLowerCase() : "";
  const buttonInput = ["submit", "button", "reset", "image"].includes(type);
  const implicitRole = () => {
    if (tag === "a" || tag === "area") return el.hasAttribute("href") ? "link" : "";
    if (tag === "input") {
      const roles = { checkbox: "checkbox", radio: "radio", range: "slider", number: "spinbutton", search: "searchbox",
        submit: "button", button: "button", reset: "button", image: "button", hidden: "" };
      return type in roles ? roles[type] : "textbox";
    }
    if (tag === "select") return el.multiple || el.size > 1 ? "listbox" : "combobox";
    if (/^h[1-6]$/.test(tag)) return "heading";
    const roles = { button: "button", textarea: "textbox", img: "img", nav: "navigation", main: "main", header: "banner",
      footer: "contentinfo", aside: "complementary", form: "form", dialog: "dialog", ul: "list", ol: "list", li: "listitem",
      table: "table", tr: "row", td: "cell", th: "columnheader", summary: "button", option: "option", p: "paragraph",
      label: "", iframe: "" };
    return roles[tag] || "";
  };
  const role = clip((el.getAttribute("role") || "").split(" ")[0] || implicitRole(), 40);
  const name = (() => {
    const by = (el.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean);
    const labelled = by.map((id) => textOf(document.getElementById(id), 200)).filter(Boolean).join(" ");
    if (labelled) return labelled;
    const aria = el.getAttribute("aria-label");
    if (aria && aria.trim()) return aria;
    if (fieldTags.includes(tag)) {
      if (buttonInput) return el.getAttribute("value") || el.getAttribute("alt") || (type === "reset" ? "Reset" : "Submit");
      const labels = Array.from(el.labels || []).map((l) => textOf(l, 200)).filter(Boolean).join(" ");
      return labels || el.getAttribute("placeholder") || el.getAttribute("title") || "";
    }
    if (tag === "img" || tag === "area") return el.getAttribute("alt") || el.getAttribute("title") || "";
    if (["button", "link", "heading", "tab", "menuitem", "option", "checkbox", "radio", "switch"].includes(role) || tag === "label")
      return textOf(el, 200) || el.getAttribute("title") || "";
    return el.getAttribute("title") || "";
  })();
  // A short CSS path: up to a unique ID, or five steps up. A step out of a shadow root to
  // its host reads " >>> ".
  const step = (e) => {
    if (e.id && /^[A-Za-z][\w-]*$/.test(e.id) && e.getRootNode().querySelectorAll("#" + e.id).length === 1)
      return "#" + e.id;
    let s = e.localName;
    const classes = Array.from(e.classList).filter((c) => /^[A-Za-z_-][\w-]{0,29}$/.test(c)).slice(0, 2);
    if (classes.length) s += "." + classes.join(".");
    const parent = e.parentElement;
    if (parent) {
      const same = Array.from(parent.children).filter((c) => c.localName === e.localName);
      if (same.length > 1) s += ":nth-of-type(" + (same.indexOf(e) + 1) + ")";
    }
    return s;
  };
  const path = [];
  for (let e = el; e && path.length < 5; ) {
    const s = step(e);
    path.unshift(s);
    if ((s.startsWith("#") && e.getRootNode() === document) || e.localName === "body") break;
    if (e.parentElement) {
      e = e.parentElement;
    } else {
      const root = e.getRootNode();
      e = root instanceof ShadowRoot ? root.host : null;
      if (e) path.unshift(">>>");
    }
  }
  const box = el.getBoundingClientRect();
  const round = (v) => Math.round(v * 10) / 10;
  return {
    tag: clip(tag, 40),
    role,
    name: clip(name, 200),
    id: clip(el.id, 100),
    selector: clip(path.join(" > ").replaceAll("> >>> >", ">>>"), 200),
    text: field ? "" : textOf(el, 300),
    field,
    type: clip(type, 20),
    fieldName: field ? clip(el.getAttribute("name"), 100) : "",
    placeholder: field ? clip(el.getAttribute("placeholder"), 100) : "",
    href: (tag === "a" || tag === "area") && el.href ? clip(el.href, 300) : "",
    box: { x: round(box.x), y: round(box.y), width: round(box.width), height: round(box.height) },
  };
})()`
