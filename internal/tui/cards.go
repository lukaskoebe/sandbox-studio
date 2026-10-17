package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// The visual look's approval cards: a rounded frame with the rule of the approval's area down
// its left edge, the kind as a chip, what it asks, its choices and, on the card under the cursor,
// its keys as keycaps. The focused card's frame is the accent.

// cardArea is the area an approval belongs to, which gives its card its hue.
func cardArea(a api.ApprovalView) area {
	switch {
	case isNetwork(a):
		return areaNetwork
	case strings.HasPrefix(a.Kind, "git."):
		return areaGit
	case strings.HasPrefix(a.Kind, "browser."):
		return areaBrowser
	case strings.HasPrefix(a.Kind, "memory."):
		return areaMemory
	case a.Kind == "chat.permission":
		return areaChat
	}
	return areaNone
}

// kindChip is an approval's kind as the card's chip: a glyph and the kind's name in capitals.
func kindChip(a api.ApprovalView) string {
	glyph := map[area]string{
		areaNetwork: "⇅", areaGit: "±", areaBrowser: "▣", areaMemory: "◈", areaChat: "▶",
	}[cardArea(a)]
	name := kindTitle(a.Kind)
	if isNetwork(a) {
		name = "Network"
	}
	return firstNonEmpty(glyph, "•") + " " + strings.ToUpper(name)
}

// visualCard is one card of the approvals screen in the visual look, w cells wide, and where its
// keycaps sit in it.
func (m *app) visualCard(c card, selected bool, w int) ([]string, []keyHit) {
	a := c.first()
	hue := m.pal.hue(cardArea(a))
	frame := styleDim
	if selected {
		frame = styleAccent
	}
	inner := max(w-4, 10)
	source := firstNonEmpty(approvalSource(a), "A sandbox")
	when := ""
	if !a.CreatedAt.IsZero() {
		when = age(m.now.Sub(a.CreatedAt))
	}
	details := nonEmpty(m.envName(a.EnvironmentID), when)
	var title string
	var body []string
	ch := m.choiceOf(c)
	key := func(k string) string { return styleDim.Render(" ‹" + k + "›") }
	switch {
	case c.group:
		title = firstNonEmpty(a.Network.Site, source) + " wants " + fmt.Sprint(len(c.approvals)) + " hosts"
		if a.Network.Site != "" {
			details = append([]string{source}, details...)
		}
		hosts := make([]string, 0, len(c.approvals))
		for _, x := range c.approvals {
			hosts = append(hosts, styleBold.Render(hostOf(x)))
		}
		body = append(body, strings.Join(hosts, "  "), whoText(a, *ch)+key("s"))
	case isNetwork(a):
		title = source + " wants " + styleBold.Render(hostOf(a))
		body = append(body, styleDim.Render("pattern ")+patternText(a, *ch)+key("p")+"   "+
			whoText(a, *ch)+key("s")+"   "+styleDim.Render("ports ")+portsText(*ch)+key("x"))
	case isGit(a):
		title = gitSummary(a)
		details = append([]string{source}, details...)
		if rv := a.Git.Review; rv != nil && a.Git.PullRequest == nil && rv.Files > 0 {
			body = append(body, m.pal.added.Render(fmt.Sprintf("+%d", rv.Additions))+" "+
				m.pal.removed.Render(fmt.Sprintf("−%d", rv.Deletions))+fmt.Sprintf(" in %d %s", rv.Files, plural(rv.Files, "file", "files")))
		}
	default:
		title = summary(a)
		details = append(nonEmpty(approvalSource(a)), details...)
		lines := m.kindLines(c, inner)
		switch a.Kind {
		case "memory.share":
		case "chat.permission":
			lines = lines[1 : len(lines)-1] // the choices are its keycaps
		default:
			lines = lines[1:]
		}
		body = append(body, lines...)
	}
	right := styleDim.Render(strings.Join(details, " · "))
	chip := hue.Bold(true).Render(kindChip(a))
	head := chip + "   " + clip(title, max(inner-lipgloss.Width(chip)-lipgloss.Width(right)-5, 8))
	lines := []string{spread(head, right, inner)}
	lines = append(lines, body...)
	var keys []keyHit
	if selected {
		if caps := cardCaps(c, *ch); len(caps) > 0 {
			line, spans := m.pal.capsLine(caps)
			for _, k := range spans {
				// The box's top is the card's first row, and its rule and a space start each line.
				if k.x1 <= inner {
					keys = append(keys, keyHit{y: 1 + len(lines), x0: 2 + k.x0, x1: 2 + k.x1, key: k.key})
				}
			}
			lines = append(lines, line)
		}
	}
	return cardBox(lines, w, hue, frame), keys
}

// cardCaps are a card's choices as keycaps: the decisions its keys make.
func cardCaps(c card, ch choice) []choiceCap {
	a := c.first()
	switch {
	case c.group:
		return []choiceCap{{"A", "Allow all"}, {"D", "Deny all"}, {"r", "One by one"}}
	case isNetwork(a):
		caps := []choiceCap{{"y", "Allow"}, {"n", "Deny"}, {"o", "Once"}, {"O", "Deny once"}, {"1", "1 hour"}, {"i", "Ignore"}}
		if a.Network != nil && a.Network.Navigation && a.Network.Site != "" {
			caps = append(caps, choiceCap{"w", "Allow site"})
		}
		return caps
	case isGit(a):
		return []choiceCap{{"enter", "Review the diff"}}
	case a.Kind == "browser.action" && a.BrowserAction != nil:
		always := "Always"
		if ch.remember {
			always = "Just this one"
		}
		return []choiceCap{{"y", "Allow"}, {"n", "Deny"}, {"i", "Ignore"}, {"r", always}}
	case a.Kind == "browser.credential" && a.BrowserCredential != nil:
		return []choiceCap{{"y", "Fill"}, {"n", "Deny"}, {"i", "Ignore"}}
	case a.Kind == "memory.share":
		return []choiceCap{{"y", "Share"}, {"n", "Decline"}, {"i", "Ignore"}}
	case a.Kind == "memory.conflict" && a.MemoryConflict != nil && a.MemoryConflict.ConflictID != "":
		return []choiceCap{{"1", "Keep A"}, {"2", "Keep B"}, {"i", "Later"}, {"enter", "More, in the browser"}}
	case a.Kind == "chat.permission" && a.ChatPermission != nil:
		var caps []choiceCap
		for i, p := range permissionPicks(a.ChatPermission.Options) {
			if i == 9 {
				break
			}
			caps = append(caps, choiceCap{fmt.Sprint(i + 1), oneLine(p.label)})
		}
		return append(caps, choiceCap{"i", "Ignore"})
	}
	return []choiceCap{{"enter", "Open in the browser"}}
}
