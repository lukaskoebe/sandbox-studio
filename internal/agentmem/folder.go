package agentmem

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// The folder copy of memory (docs/design/memory-links.md#4-a-folder-instead-of-a-file):
//
//	~/memory/
//	  index.md              the catalog: your pages, then the shared ones
//	  pages/<slug>.md       one page: compiled truth, its facts, timeline, "Linked from"
//	  facts.md              facts that no page holds
//	  shared/…              the same for shared memory
//
// [[slug]] links become relative markdown links, so grep -r, cat and an editor follow them.

// scopePages are a scope's pages with their timelines and links, read once per sync.
func (b *browser) scopePages(ctx context.Context, scope string) ([]memory.Page, []memory.Fact, error) {
	if p, ok := b.pages[scope]; ok {
		return p, b.facts[scope], nil
	}
	pages, err := b.s.Memory.PagesWithLinks(ctx, b.env, scope)
	if err != nil {
		return nil, nil, err
	}
	facts, err := b.s.Memory.Facts(ctx, b.env, memory.FactFilter{Scope: scope})
	if err != nil {
		return nil, nil, err
	}
	b.pages[scope], b.facts[scope] = pages, facts
	return pages, facts, nil
}

// folderScope is one scope of the folder: where its files go and what it holds.
type folderScope struct {
	scope string
	dir   string // "" or "shared/"
	pages []memory.Page
	facts []memory.Fact
}

func (f folderScope) pagePath(slug string) string { return f.dir + "pages/" + slug + ".md" }

// folder renders the copy for a teammate's sandboxes.
func (b *browser) folder(ctx context.Context, teammate string) (agentproto.HomeDir, error) {
	own := memory.TeammateScope(teammate)
	var scopes []folderScope
	for _, sc := range []struct{ scope, dir string }{{own, ""}, {memory.SharedScope, "shared/"}} {
		pages, facts, err := b.scopePages(ctx, sc.scope)
		if err != nil {
			return agentproto.HomeDir{}, err
		}
		scopes = append(scopes, folderScope{scope: sc.scope, dir: sc.dir, pages: pages, facts: facts})
	}
	r := &folderRender{byScope: map[string]folderScope{}, teammateScope: own}
	for _, sc := range scopes {
		r.byScope[sc.scope] = sc
	}

	var files []agentproto.HomeFile
	left := folderBytes
	add := func(p, content string) bool {
		content = redact(content)
		if len(content) > left {
			return false
		}
		left -= len(content)
		files = append(files, agentproto.HomeFile{Path: p, Content: content, Mode: 0o444})
		return true
	}
	// The index goes first so it always fits; pages after it, the teammate's own first.
	indexAt := len(files)
	add("index.md", "")
	for _, sc := range scopes {
		add(sc.dir+"facts.md", r.factsFile(sc))
	}
	cut := 0
	for _, sc := range scopes {
		for _, p := range sc.pages {
			if !add(sc.pagePath(p.Slug), r.pageFile(sc, p)) {
				cut++
			}
		}
	}
	index := r.indexFile(scopes, "")
	if cut > 0 {
		index += fmt.Sprintf("\n%d pages didn't fit in this copy; memory_get reads them.\n", cut)
	}
	files[indexAt].Content = redact(index)
	add("shared/index.md", r.indexFile(scopes[1:], "shared/"))
	return agentproto.HomeDir{Dir: BrowseDir, Files: files}, nil
}

type folderRender struct {
	byScope       map[string]folderScope
	teammateScope string
}

// resolve finds the file a link from scope names: in scope first, then shared, with
// topic/ for a slug without "/", as memory resolves links.
func (r *folderRender) resolve(scope, target string) (string, bool) {
	cands := []string{target}
	if !strings.Contains(target, "/") {
		cands = append(cands, "topic/"+target)
	}
	for _, sc := range []string{scope, memory.SharedScope} {
		fs, ok := r.byScope[sc]
		if !ok {
			continue
		}
		for _, t := range cands {
			for _, p := range fs.pages {
				if p.Slug == t {
					return fs.pagePath(p.Slug), true
				}
			}
		}
	}
	return "", false
}

var folderLink = regexp.MustCompile(`\[\[([^\[\]|\n]{1,128})(?:\|([^\[\]\n]{1,200}))?\]\]`)

// links rewrites [[slug]] in text of scope, written to file from, as relative links.
func (r *folderRender) links(text, scope, from string) string {
	return folderLink.ReplaceAllStringFunc(text, func(m string) string {
		sub := folderLink.FindStringSubmatch(m)
		target := strings.ToLower(strings.TrimSpace(sub[1]))
		label := strings.TrimSpace(sub[2])
		if label == "" {
			label = strings.TrimSpace(sub[1])
		}
		to, ok := r.resolve(scope, target)
		if !ok {
			return label + " (no page yet)"
		}
		return "[" + label + "](" + relPath(from, to) + ")"
	})
}

// relPath is the relative link from the file from to the file to, both slash paths from the
// folder's root.
func relPath(from, to string) string {
	fromDir := strings.Split(path.Dir(from), "/")
	if fromDir[0] == "." {
		fromDir = nil
	}
	toParts := strings.Split(to, "/")
	i := 0
	for i < len(fromDir) && i < len(toParts)-1 && fromDir[i] == toParts[i] {
		i++
	}
	return strings.Repeat("../", len(fromDir)-i) + strings.Join(toParts[i:], "/")
}

func scopeTitle(scope string) string {
	if scope == memory.SharedScope {
		return "shared memory"
	}
	return "your memory"
}

func (r *folderRender) indexFile(scopes []folderScope, from string) string {
	var b strings.Builder
	b.WriteString("# Memory\n\nA read-only copy from Sandbox Studio, rewritten when memory changes. Links between pages are relative, so grep -r, cat and an editor follow them. memory_search finds more; memory_page writes a page.\n\n")
	kinds := []struct{ kind, heading string }{{"project", "Projects"}, {"topic", "Topics"}, {"person", "People"}, {"procedure", "Procedures"}}
	for _, sc := range scopes {
		fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(scopeTitle(sc.scope)[:1])+scopeTitle(sc.scope)[1:])
		if len(sc.pages) == 0 {
			b.WriteString("No pages yet.\n\n")
		}
		for _, k := range kinds {
			var lines []string
			for _, p := range sc.pages {
				if p.Kind != k.kind {
					continue
				}
				line := fmt.Sprintf("- [%s](%s) `%s`", p.Title, relPath(from+"index.md", sc.pagePath(p.Slug)), p.Slug)
				if p.AlwaysLoad {
					line += ", core"
				}
				if sum := memory.FirstSentence(p.Compiled); sum != "" {
					line += ": " + strings.ReplaceAll(folderLink.ReplaceAllString(sum, "$1"), "\n", " ")
				}
				lines = append(lines, line)
			}
			if len(lines) > 0 {
				fmt.Fprintf(&b, "### %s\n\n%s\n\n", k.heading, strings.Join(lines, "\n"))
			}
		}
		fmt.Fprintf(&b, "Facts no page holds: [%s](%s)\n\n", sc.dir+"facts.md", relPath(from+"index.md", sc.dir+"facts.md"))
	}
	return b.String()
}

// shown reports whether a fact goes into the copy: active or disputed, holding everywhere.
func shown(f memory.Fact) bool {
	return f.Context == "" && (f.Status == memory.StatusActive || f.Status == memory.StatusDisputed)
}

func factLineMD(f memory.Fact) string {
	disputed := ""
	if f.Status == memory.StatusDisputed {
		disputed = ", disputed"
	}
	return fmt.Sprintf("- %s (%s, %s, %s%s)\n", strings.Join(strings.Fields(f.Text), " "), f.Kind, f.Tier, f.ObservedAt.Format(time.DateOnly), disputed)
}

func (r *folderRender) pageFile(sc folderScope, p memory.Page) string {
	from := sc.pagePath(p.Slug)
	var b strings.Builder
	meta := []string{"`" + p.Slug + "`", p.Kind + " page in " + scopeTitle(p.Scope)}
	if p.AlwaysLoad {
		meta = append(meta, "core")
	}
	meta = append(meta, "updated "+p.UpdatedAt.Format(time.DateOnly))
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", p.Title, strings.Join(meta, " · "))
	if c := strings.TrimSpace(p.Compiled); c != "" {
		b.WriteString(r.links(c, p.Scope, from) + "\n\n")
	}
	var facts strings.Builder
	for _, f := range sc.facts {
		if shown(f) && memory.FactPageSlug(f) == p.Slug {
			facts.WriteString(r.links(factLineMD(f), f.Scope, from))
		}
	}
	if facts.Len() > 0 {
		b.WriteString("## Facts\n\n" + facts.String() + "\n")
	}
	if len(p.Timeline) > 0 {
		b.WriteString("## Timeline\n\n")
		for _, e := range p.Timeline {
			fmt.Fprintf(&b, "- %s: %s\n", e.At.Format(time.DateOnly), r.links(strings.Join(strings.Fields(e.Text), " "), p.Scope, from))
		}
		b.WriteString("\n")
	}
	var linked []string
	for _, l := range p.LinkedFrom {
		if l.Scope != memory.SharedScope && l.Scope != r.teammateScope {
			continue // another teammate's memory
		}
		text := strings.Join(strings.Fields(l.Text), " ")
		if l.Type == "page" {
			fs := r.byScope[l.Scope]
			linked = append(linked, fmt.Sprintf("- [%s](%s): %s", l.Title, relPath(from, fs.pagePath(l.Slug)), r.links(text, l.Scope, from)))
		} else {
			linked = append(linked, "- A fact: "+r.links(text, l.Scope, from))
		}
	}
	if len(linked) > 0 {
		b.WriteString("## Linked from\n\n" + strings.Join(linked, "\n") + "\n")
	}
	text := b.String()
	if len(text) > pageBytes {
		text = truncateBytes(text, pageBytes) + "\n\n… Studio cut this page at 64 KiB. memory_get reads all of it.\n"
	}
	return text
}

func (r *folderRender) factsFile(sc folderScope) string {
	onPage := map[string]bool{}
	for _, p := range sc.pages {
		onPage[p.Slug] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Facts in %s that no page holds\n\nNewest first.\n\n", scopeTitle(sc.scope))
	n := 0
	for _, f := range sc.facts { // newest first already
		if !shown(f) {
			continue
		}
		if slug := memory.FactPageSlug(f); slug != "" && onPage[slug] {
			continue
		}
		b.WriteString(r.links(factLineMD(f), f.Scope, sc.dir+"facts.md"))
		n++
	}
	if n == 0 {
		b.WriteString("None.\n")
	}
	return b.String()
}
