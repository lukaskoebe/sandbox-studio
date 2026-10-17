package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The keys of the approvals that are neither network requests nor git: browser actions and
// credentials, memory shares and conflicts, and an agent's permission requests. They decide as
// the web's cards for them do (browser-approval.tsx, memory-sessions.tsx, memory-conflict.tsx,
// chat/permission-card.tsx).
var (
	keyFill     = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "fill"))
	keyShare    = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "share"))
	keyDecline  = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "decline"))
	keyRemember = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "always"))
	keyKeepA    = key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "keep A"))
	keyKeepB    = key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "keep B"))
	keyLater    = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "later"))
	keyBoth     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "more (browser)"))
	keySkip     = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "skip"))
	keyReply    = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "answer"))
	keyAnswer   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send"))
)

// keyPick is the key bar's entry for the numbered choices 1 to n.
func keyPick(n int) key.Binding {
	keys := make([]string, 0, n)
	for i := 1; i <= n && i <= 9; i++ {
		keys = append(keys, fmt.Sprint(i))
	}
	help := "1"
	if n > 1 {
		help = fmt.Sprintf("1-%d", min(n, 9))
	}
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, "choose"))
}

// digit is the number 1 to 9 a key press types, or 0.
func digit(msg tea.KeyPressMsg) int {
	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		return int(s[0] - '0')
	}
	return 0
}

// pick is one numbered choice: what it says, and the decision or answer it sends.
type pick struct {
	label    string
	action   string // allow or deny for a permission; accept for an answer
	remember bool   // a permission's always-option
	value    any    // an answer's value for the question's field; nil answers with no field
}

// permissionPicks are the options an agent offers for a permission request, in its order, each as
// the decision that picks it: Studio maps allow or deny, remembered or not, back to the option of
// that kind (chooseOption in internal/chat), as the web's Allow, Deny and "Don't ask again" do.
func permissionPicks(options []acp.PermissionOption) []pick {
	var out []pick
	for _, o := range options {
		p := pick{label: o.Name}
		switch o.Kind {
		case acp.AllowOnce:
			p.action = "allow"
		case acp.AllowAlways:
			p.action, p.remember = "allow", true
		case acp.RejectOnce:
			p.action = "deny"
		case acp.RejectAlways:
			p.action, p.remember = "deny", true
		default:
			continue
		}
		out = append(out, p)
	}
	return out
}

// picksText is the numbered choices on one line: "1 Allow once  2 Always allow".
func picksText(picks []pick) string {
	parts := make([]string, 0, len(picks))
	for i, p := range picks {
		if i == 9 {
			break
		}
		parts = append(parts, styleBold.Render(fmt.Sprint(i+1))+" "+oneLine(p.label))
	}
	return strings.Join(parts, "  ")
}

// kindDecision is the body the web sends for a decision on an approval of another kind: the
// browser's are the teammate's, memory and permissions the environment's. Ignoring remembers
// nothing.
func kindDecision(a api.ApprovalView, action string, remember bool) Decision {
	d := Decision{Action: action, Scope: "environment"}
	if strings.HasPrefix(a.Kind, "browser.") {
		d.Scope = "teammate"
	}
	if (a.Kind == "browser.action" || a.Kind == "chat.permission") && action != "dismiss" {
		d.Remember = remember
	}
	return d
}

// kindKey answers the keys of a card of another kind. A kind the TUI can't decide prints the
// page that does, as does enter on a memory conflict, whose other resolutions are written there.
func (m *app) kindKey(msg tea.KeyPressMsg, c card) tea.Cmd {
	a := c.first()
	ch := m.choiceOf(c)
	decide := func(action string, remember bool, verb, what string) tea.Cmd {
		return m.decide(c, kindDecision(a, action, remember), verb, what)
	}
	switch {
	case a.Kind == "browser.action" && a.BrowserAction != nil:
		what := "the " + a.BrowserAction.Action + " on " + a.BrowserAction.Origin
		switch {
		case key.Matches(msg, keyAllow):
			return decide("allow", ch.remember, "Allowed", what)
		case key.Matches(msg, keyDeny):
			return decide("deny", ch.remember, "Denied", what)
		case key.Matches(msg, keyIgnore):
			return decide("dismiss", false, "Ignored", what)
		case key.Matches(msg, keyRemember):
			ch.remember = !ch.remember
			return nil
		}
	case a.Kind == "browser.credential" && a.BrowserCredential != nil:
		what := a.BrowserCredential.Credential + " on " + a.BrowserCredential.Origin
		switch {
		case key.Matches(msg, keyFill):
			return decide("allow", false, "Filling", what)
		case key.Matches(msg, keyDeny):
			return decide("deny", false, "Denied", what)
		case key.Matches(msg, keyIgnore):
			return decide("dismiss", false, "Ignored", what)
		}
	case a.Kind == "memory.share":
		switch {
		case key.Matches(msg, keyShare):
			return decide("allow", false, "Shared", "the fact with every teammate")
		case key.Matches(msg, keyDecline):
			return decide("deny", false, "Declined", "the share")
		case key.Matches(msg, keyIgnore):
			return decide("dismiss", false, "Ignored", "the share")
		}
	case a.Kind == "memory.conflict" && a.MemoryConflict != nil && a.MemoryConflict.ConflictID != "":
		switch {
		case key.Matches(msg, keyKeepA):
			return m.resolveConflict(a, "keep_a", "Kept fact A; B is retracted.")
		case key.Matches(msg, keyKeepB):
			return m.resolveConflict(a, "keep_b", "Kept fact B; A is retracted.")
		case key.Matches(msg, keyLater):
			return decide("dismiss", false, "Put off", "the conflict; it stays open on the memory page")
		}
	case a.Kind == "chat.permission" && a.ChatPermission != nil:
		picks := permissionPicks(a.ChatPermission.Options)
		if n := digit(msg); n > 0 && n <= len(picks) {
			p := picks[n-1]
			return decide(p.action, p.remember, "Chose “"+oneLine(p.label)+"” for", firstNonEmpty(oneLine(a.ChatPermission.Title), "the tool call"))
		}
		if key.Matches(msg, keyIgnore) {
			return decide("dismiss", false, "Ignored", "the permission request")
		}
	}
	if key.Matches(msg, keyOpen) {
		m.setNotice("open in browser: "+m.approvalURL(a), false)
	}
	return nil
}

// resolveConflict settles a memory conflict by keeping one fact, as the web's conflict sheet
// does; Studio closes the approval with it.
func (m *app) resolveConflict(a api.ApprovalView, resolution, done string) tea.Cmd {
	ctx, client, env, id := m.ctx, m.client, a.EnvironmentID, a.MemoryConflict.ConflictID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		if err := client.ResolveConflict(rctx, env, id, resolution); err != nil {
			return actionDone{err: fmt.Errorf("could not resolve the conflict: %w", err)}
		}
		return actionDone{done: done}
	}
}

// kindBindings are the keys the key bar shows for a card of another kind.
func kindBindings(a api.ApprovalView) []key.Binding {
	switch {
	case a.Kind == "browser.action" && a.BrowserAction != nil:
		return []key.Binding{keyAllow, keyDeny, keyIgnore, keyRemember, keyBack}
	case a.Kind == "browser.credential" && a.BrowserCredential != nil:
		return []key.Binding{keyFill, keyDeny, keyIgnore, keyBack}
	case a.Kind == "memory.share":
		return []key.Binding{keyShare, keyDecline, keyIgnore, keyBack}
	case a.Kind == "memory.conflict" && a.MemoryConflict != nil && a.MemoryConflict.ConflictID != "":
		return []key.Binding{keyKeepA, keyKeepB, keyLater, keyBoth, keyBack}
	case a.Kind == "chat.permission" && a.ChatPermission != nil:
		return []key.Binding{keyPick(len(permissionPicks(a.ChatPermission.Options))), keyIgnore, keyBack}
	}
	return []key.Binding{keyOpen, keyBack}
}

// kindLines are what a card of another kind shows under its title, in w cells: what it asks,
// the facts the web's card shows, and its choices.
func (m *app) kindLines(c card, w int) []string {
	a := c.first()
	lines := []string{fit(summary(a), w)}
	switch {
	case a.Kind == "browser.action" && a.BrowserAction != nil:
		act := a.BrowserAction
		if act.Text != "" {
			lines = append(lines, styleDim.Render("text ")+fit(oneLine(act.Text), w-5))
		}
		reach := "this one"
		if m.choiceOf(c).remember {
			reach = "every " + act.Action + " on this origin"
		}
		lines = append(lines, styleDim.Render("decides ")+reach)
	case a.Kind == "browser.credential" && a.BrowserCredential != nil:
		cred := a.BrowserCredential
		field := "the field"
		if cred.Label != "" {
			field = "“" + cred.Label + "”"
		}
		lines = append(lines, styleDim.Render(fit("Studio fills the secret into "+field+"; "+
			firstNonEmpty(cred.TeammateName, "the agent")+" sees only its name.", w)))
	case a.Kind == "memory.share" && a.MemoryShare != nil:
		share := a.MemoryShare
		lines = []string{fit(oneLine(share.Text), w), styleDim.Render(fit(strings.Join(nonEmpty(share.TeammateName, share.Kind, share.Tier), " · "), w))}
	case a.Kind == "memory.conflict" && a.MemoryConflict != nil:
		p := a.MemoryConflict
		for _, side := range []struct {
			mark string
			text string
			tier string
		}{{"A ", p.FactA.Text, p.FactA.Tier}, {"B ", p.FactB.Text, p.FactB.Tier}} {
			tier := " · " + side.tier
			lines = append(lines, styleBold.Render(side.mark)+fit(oneLine(side.text), w-2-len(tier))+styleDim.Render(tier))
		}
		if p.ConflictID == "" {
			lines = append(lines, styleDim.Render("enter opens it in the browser"))
		}
	case a.Kind == "chat.permission" && a.ChatPermission != nil:
		p := a.ChatPermission
		lines = append(lines, styleDim.Render(fit("In "+firstNonEmpty(p.TeammateName, "the teammate")+"'s chat “"+firstNonEmpty(oneLine(p.ChatTitle), "New chat")+"”", w)))
		lines = append(lines, picksText(permissionPicks(p.Options))+styleDim.Render("  i ignore"))
	default:
		lines = append(lines, styleDim.Render("enter opens it in the browser"))
	}
	return lines
}

// questionField is the one field of an agent's question the TUI answers: pick one of its
// choices, or type its text or number. A question with no field is answered with OK.
type questionField struct {
	name  string
	kind  string // one, bool, number or text
	picks []pick
}

// questionForm reads a question's form, as the web's question card does (chat/question-card.tsx).
// ok is false for a form the TUI leaves to the browser: several fields, or one that picks several.
func questionForm(schema json.RawMessage) (f questionField, ok bool) {
	var s struct {
		Properties map[string]struct {
			Type  json.RawMessage `json:"type"`
			Enum  []any           `json:"enum"`
			Items *struct {
				Enum []any `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return questionField{picks: []pick{{label: "OK", action: "accept"}}}, true
	}
	if len(s.Properties) > 1 {
		return questionField{}, false
	}
	for name, p := range s.Properties {
		f.name = name
		t := schemaType(p.Type)
		switch {
		case p.Enum != nil:
			f.kind = "one"
			for _, v := range p.Enum {
				f.picks = append(f.picks, pick{label: fmt.Sprint(v), action: "accept", value: v})
			}
		case t == "array":
			return questionField{}, false
		case t == "boolean":
			f.kind = "bool"
			f.picks = []pick{{label: "Yes", action: "accept", value: true}, {label: "No", action: "accept", value: false}}
		case t == "number" || t == "integer":
			f.kind = "number"
		default:
			f.kind = "text"
		}
	}
	return f, true
}

// schemaType is a JSON Schema type: the one named, or the first that isn't null of a list.
func schemaType(raw json.RawMessage) string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		for _, t := range list {
			if t != "null" {
				return t
			}
		}
	}
	return "string"
}

// answerContent is the form's values for an answer: the field's value, or nothing for a
// question with no field.
func answerContent(f questionField, value any) map[string]any {
	if f.name == "" {
		return map[string]any{}
	}
	return map[string]any{f.name: value}
}

// answerQuestion sends an answer to a chat's question.
func (m *app) answerQuestion(chatID, item string, a Answer, done string) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		if err := client.AnswerQuestion(rctx, env, chatID, item, a); err != nil {
			return actionDone{err: fmt.Errorf("could not answer: %w", err)}
		}
		return actionDone{done: done}
	}
}

// decideApproval decides one approval of the environment on show by its ID, as a card would.
func (m *app) decideApproval(id string, d Decision, verb, what string) tea.Cmd {
	c := card{approvals: []api.ApprovalView{{Approval: store.Approval{ID: id, EnvironmentID: m.env.ID}}}}
	return m.decide(c, d, verb, what)
}
