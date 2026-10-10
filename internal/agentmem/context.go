package agentmem

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// Context budgets, in characters (about four per token).
const (
	PackBudget      = 12000 // a whole session-start pack
	soulBudget      = 4000
	projectBudget   = 3000
	maxChanges      = 10
	maxConflicts    = 5
	defaultLookback = 7 * 24 * time.Hour // shared changes shown to a persona's first session

	// RecallBudget caps what one prompt injects: about 500 tokens.
	RecallBudget = 2000
	maxRecall    = 5
	// MinSimilarity is the cosine similarity a hit needs to be injected when it doesn't
	// share MinTerms words with the prompt.
	MinSimilarity = 0.45
	MinTerms      = 2
)

// Tags around everything Studio injects; the guest drops them from transcripts so
// memory isn't extracted again.
const (
	openTag  = "<studio-memory>"
	closeTag = "</studio-memory>"
)

func (s *Service) hook(ctx context.Context, c caller, ev agenthook.Event) (agenthook.Result, error) {
	res := agenthook.Result{Event: ev.Event}
	if ev.SessionID == "" || len(ev.SessionID) > agenthook.MaxSessionID || !slices.Contains(agenthook.Events, ev.Event) {
		return res, nil
	}
	remote := ""
	if ev.Event == agenthook.SessionStart {
		remote = agenthook.NormalizeRemote("https://" + ev.GitRemote) // re-normalized: the guest is not trusted
	}
	se, err := s.session(ctx, c, ev.SessionID, remote)
	if err != nil {
		return res, err
	}
	switch ev.Event {
	case agenthook.SessionStart:
		res.Context, err = s.pack(ctx, c, se, remote, ev.Source)
	case agenthook.UserPrompt:
		res.Context, err = s.recall(ctx, c, se, ev.Prompt)
	default:
		err = s.enqueue(c, se, ev)
	}
	return res, err
}

// budget collects sections up to a total size.
type budget struct {
	b    strings.Builder
	left int
}

// add writes text, cut to max and to what is left; it reports whether anything fit.
func (b *budget) add(text string, limit int) bool {
	n := min(limit, b.left)
	if n <= 0 {
		return false
	}
	if r := []rune(text); len(r) > n {
		text = string(r[:max(n-1, 0)]) + "…\n"
	}
	b.b.WriteString(text)
	b.left -= len([]rune(text))
	return true
}

// pack is the session-start context: the soul, core pages, the project page, shared
// changes since the persona's last session and open conflicts on them.
func (s *Service) pack(ctx context.Context, c caller, se session, remote, source string) (string, error) {
	b := &budget{left: PackBudget}
	var logged []entry
	b.add(openTag+"\n# Memory from Sandbox Studio\n\n", 200)
	if strings.TrimSpace(c.persona.Soul) != "" {
		b.add("## Your soul\n\n"+strings.TrimSpace(c.persona.Soul)+"\n\n", soulBudget)
		logged = append(logged, entry{kind: LogContext, itemType: "soul", itemID: "soul:" + c.persona.ID, summary: "soul of " + c.persona.Name})
	}

	// Core pages: the persona's first, then shared.
	var core []memory.Page
	for _, scope := range []string{c.scope, memory.SharedScope} {
		pages, err := s.Memory.Pages(ctx, c.env, scope)
		if err != nil {
			return "", err
		}
		for _, p := range pages {
			if p.AlwaysLoad {
				core = append(core, p)
			}
		}
	}
	if len(core) > 0 {
		b.add("## Core memory\n\n", 100)
		for _, p := range core {
			if b.add(fmt.Sprintf("### %s (%s, %s)\n\n%s\n\n", p.Title, scopeLabel(p.Scope), p.Slug, strings.TrimSpace(p.Compiled)), memory.CoreBudget+200) {
				logged = append(logged, entry{kind: LogContext, itemType: "page", itemID: p.ID, summary: "core page " + p.Scope + "/" + p.Slug})
			}
		}
	}

	if p, ok, err := s.projectPage(ctx, c, remote); err != nil {
		return "", err
	} else if ok && !p.AlwaysLoad {
		if b.add(fmt.Sprintf("## This project: %s (%s)\n\n%s\n\n", p.Title, scopeLabel(p.Scope), strings.TrimSpace(p.Compiled)), projectBudget) {
			logged = append(logged, entry{kind: LogContext, itemType: "page", itemID: p.ID, summary: "project page " + p.Slug + " for remote " + remote})
		}
	}

	since, err := s.previousSessionStart(ctx, c, se)
	if err != nil {
		return "", err
	}
	sinceWhat := "your last session"
	if since.IsZero() {
		since, sinceWhat = se.startedAt.Add(-defaultLookback), "the last week"
	}
	changes, err := s.Memory.FactsChangedSince(ctx, c.env, memory.SharedScope, since, c.persona.ID, maxChanges)
	if err != nil {
		return "", err
	}
	if len(changes) > 0 {
		b.add("## Shared memory changed since "+sinceWhat+"\n\n", 100)
		for _, f := range changes {
			if b.add("- "+factLine(f)+"\n", 400) {
				logged = append(logged, entry{kind: LogContext, itemType: "fact", itemID: f.ID,
					summary: fmt.Sprintf("shared change since %s by %s", since.Format(time.DateOnly), authorLabel(f.AuthorPersona))})
			}
		}
		b.add("\n", 1)
	}

	conflicts, err := s.openConflicts(ctx, c, changes)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		b.add("## Open conflicts\n\nThe user hasn't resolved these yet. Until then the shared fact wins; check before relying on either.\n\n", 200)
		for _, cf := range conflicts {
			if b.add(fmt.Sprintf("- %s: %q (%s) vs %q (%s)\n", strings.ReplaceAll(cf.Verdict, "_", " "),
				truncate(cf.FactA.Text, 200), scopeLabel(cf.FactA.Scope), truncate(cf.FactB.Text, 200), scopeLabel(cf.FactB.Scope)), 600) {
				logged = append(logged, entry{kind: LogContext, itemType: "conflict", itemID: cf.ID, summary: "open conflict " + cf.Verdict})
			}
		}
		b.add("\n", 1)
	}
	if len(logged) == 0 {
		return "", nil
	}
	b.left += len(closeTag) + 1 // the closing tag always fits
	b.add(closeTag+"\n", len(closeTag)+1)
	for i := range logged {
		logged[i].summary += " (session start: " + orDefault(source, "startup") + ")"
	}
	s.log(ctx, c, se.id, logged...)
	return b.b.String(), nil
}

// projectPage is the page of the repository at remote: by its full slug
// (project/github.com/acme/api), then by its name (project/api), the persona's first.
func (s *Service) projectPage(ctx context.Context, c caller, remote string) (memory.Page, bool, error) {
	if remote == "" {
		return memory.Page{}, false, nil
	}
	remote = strings.ToLower(remote) // slugs are lowercase
	slugs := []string{"project/" + remote}
	if i := strings.LastIndex(remote, "/"); i >= 0 {
		slugs = append(slugs, "project/"+remote[i+1:])
	}
	for _, slug := range slugs {
		for _, scope := range []string{c.scope, memory.SharedScope} {
			p, err := s.Memory.PageBySlug(ctx, c.env, scope, slug)
			if err == nil {
				return p, true, nil
			}
			if !errors.Is(err, memory.ErrNotFound) {
				return p, false, err
			}
		}
	}
	return memory.Page{}, false, nil
}

// openConflicts are the open conflicts on the changed facts or their entities, and on the
// persona's own facts.
func (s *Service) openConflicts(ctx context.Context, c caller, changes []memory.Fact) ([]memory.Conflict, error) {
	all, err := s.Memory.Conflicts(ctx, c.env, "")
	if err != nil {
		return nil, err
	}
	ids, entities := map[string]bool{}, map[string]bool{}
	for _, f := range changes {
		ids[f.ID] = true
		for _, e := range f.EntityIDs {
			entities[e] = true
		}
	}
	readable := func(f memory.Fact) bool { return f.Scope == memory.SharedScope || f.Scope == c.scope }
	touches := func(f memory.Fact) bool {
		if ids[f.ID] || f.Scope == c.scope {
			return true
		}
		return slices.ContainsFunc(f.EntityIDs, func(e string) bool { return entities[e] })
	}
	var out []memory.Conflict
	for _, cf := range all {
		if cf.Status != "open" || !readable(cf.FactA) || !readable(cf.FactB) || !(touches(cf.FactA) || touches(cf.FactB)) {
			continue
		}
		out = append(out, cf)
		if len(out) == maxConflicts {
			break
		}
	}
	return out, nil
}

// recallWhy is why a hit was injected: the search's reasons plus the threshold it passed.
type recallWhy struct {
	memory.Why
	Passed       string   `json:"passed"`
	MatchedTerms []string `json:"matchedTerms,omitempty"`
}

// recall injects the memories relevant to a prompt: search hits that pass MinSimilarity or
// share MinTerms words with it, not yet given to the session, up to RecallBudget.
func (s *Service) recall(ctx context.Context, c caller, se session, prompt string) (string, error) {
	terms := queryTerms(prompt)
	if len(terms) == 0 {
		return "", nil
	}
	hits, err := s.Memory.Search(ctx, c.env, memory.SearchRequest{Query: prompt, Persona: c.persona.ID, Limit: 10})
	if errors.Is(err, memory.ErrInvalid) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	seen, err := s.injectedIDs(ctx, se.id)
	if err != nil {
		return "", err
	}
	var lines []string
	var logged []entry
	left := RecallBudget
	for _, h := range hits {
		if seen[h.ID] || len(lines) == maxRecall {
			continue
		}
		why := recallWhy{Why: h.Why}
		text := h.Snippet
		if h.Fact != nil {
			text = h.Fact.Text
		}
		why.MatchedTerms = matchedTerms(terms, text)
		switch {
		case h.Why.Similarity >= MinSimilarity:
			why.Passed = fmt.Sprintf("similarity %.2f ≥ %.2f", h.Why.Similarity, MinSimilarity)
		case len(why.MatchedTerms) >= MinTerms:
			why.Passed = fmt.Sprintf("%d prompt words matched (≥ %d)", len(why.MatchedTerms), MinTerms)
		default:
			continue
		}
		line := "- " + hitLine(h, text)
		if len([]rune(line))+1 > left {
			continue
		}
		left -= len([]rune(line)) + 1
		lines = append(lines, line)
		score := h.Score
		logged = append(logged, entry{kind: LogRecall, itemType: h.Type, itemID: h.ID, score: &score, why: why,
			summary: fmt.Sprintf("recalled for a prompt: %s; %s", why.Passed, h.Why.Summary)})
	}
	if len(lines) == 0 {
		return "", nil
	}
	s.log(ctx, c, se.id, logged...)
	return openTag + "\nFrom memory (Sandbox Studio), possibly relevant to this prompt:\n" + strings.Join(lines, "\n") + "\n" + closeTag + "\n", nil
}

func hitLine(h memory.Hit, text string) string {
	var tags []string
	if h.Type == "page" && h.Page != nil {
		tags = append(tags, "page "+h.Page.Slug)
	} else {
		tags = append(tags, h.Type+" "+h.ID)
	}
	tags = append(tags, scopeLabel(h.Scope), h.Why.Tier)
	if h.Disputed {
		tags = append(tags, "disputed")
	}
	return fmt.Sprintf("[%s] %s", strings.Join(tags, ", "), truncate(strings.Join(strings.Fields(text), " "), 400))
}

func factLine(f memory.Fact) string {
	disputed := ""
	if f.Status == memory.StatusDisputed {
		disputed = ", disputed"
	}
	return fmt.Sprintf("[fact %s, %s, by %s%s] %s", f.ID, f.Tier, authorLabel(f.AuthorPersona), disputed, truncate(f.Text, 300))
}

func scopeLabel(scope string) string {
	if scope == memory.SharedScope {
		return "shared"
	}
	return "yours"
}

func authorLabel(persona string) string {
	if persona == "" {
		return "the user"
	}
	return "persona " + persona
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// stopwords are left out when matching prompt words.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for are but not you all any can had her was one our out has him his how its may new now old see two way who did get let put say she too use that with have this will your from they know want been good much some time very when come here just like long make many over such take than them well were what where which while would there their about could other into more only also then these those after should please thanks thank need does done using used make sure file files code`) {
		stopwords[w] = true
	}
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// queryTerms are the distinct words of a prompt that count for matching.
func queryTerms(prompt string) []string {
	var out []string
	for _, w := range words(prompt) {
		if len(w) >= 3 && !stopwords[w] && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// matchedTerms are the query terms found in text, allowing a shared stem of four letters
// (deploy, deploys, deployment).
func matchedTerms(terms []string, text string) []string {
	tw := words(text)
	var out []string
	for _, t := range terms {
		if slices.ContainsFunc(tw, func(w string) bool { return w == t || (len(t) >= 4 && len(w) >= 4 && stem(w) == stem(t)) }) {
			out = append(out, t)
		}
	}
	return out
}

func stem(w string) string {
	for _, suf := range []string{"ments", "ment", "ings", "ing", "ers", "er", "ed", "es", "s"} {
		if r, ok := strings.CutSuffix(w, suf); ok && len(r) >= 4 {
			return r
		}
	}
	return w
}
