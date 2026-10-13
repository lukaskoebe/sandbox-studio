package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Markdown export and import (docs/memory.md, Export and import). A scope is one markdown
// file: front matter, then a section per page (core pages first) and per active fact. Each
// section has its own front matter, and its body is given as a byte count, so no content can
// pass for structure. An import is untrusted: its size, grammar, values and slugs are all
// checked before anything is written.

const (
	exchangeFormat   = "1"
	exchangeMaxBytes = 2 << 20
	exchangeMaxItems = 5000
	exchangeMaxKeys  = 16
	exchangeOrigin   = "imported from a memory export"
)

// ExchangeMaxBytes is the largest file an import takes.
const ExchangeMaxBytes = exchangeMaxBytes

// oneLine collapses a value to one line, the form front matter takes.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Export renders a scope as one markdown file. It holds the pages and the active facts; the
// timeline, superseded, retracted and disputed facts are not part of it.
func (s *Service) Export(ctx context.Context, envID, scope string, now time.Time) (string, error) {
	if err := ValidateScope(scope); err != nil {
		return "", err
	}
	pages, err := s.Pages(ctx, envID, scope)
	if err != nil {
		return "", err
	}
	facts, err := s.Facts(ctx, envID, FactFilter{Scope: scope, Status: StatusActive})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nstudio-memory: %s\nscope: %s\nexported: %s\n---\n", exchangeFormat, scope, now.UTC().Format(time.RFC3339))
	for _, p := range pages {
		fmt.Fprintf(&b, "\n## page %s\n---\ntitle: %s\nkind: %s\ncore: %t\ntier: %s\nbytes: %d\n---\n%s\n",
			p.Slug, oneLine(p.Title), p.Kind, p.AlwaysLoad, p.Tier, len(p.Compiled), p.Compiled)
	}
	for _, f := range facts {
		entities, _ := json.Marshal(f.EntityIDs)
		fmt.Fprintf(&b, "\n## fact\n---\nkind: %s\ntier: %s\nobserved: %s\nentities: %s\nattribute: %s\ncontext: %s\nbytes: %d\n---\n%s\n",
			f.Kind, f.Tier, f.ObservedAt.Format(time.DateOnly), entities, oneLine(f.Attribute), oneLine(f.Context), len(f.Text), f.Text)
	}
	return b.String(), nil
}

// ImportResult says what an import did.
type ImportResult struct {
	Pages   int      `json:"pages" doc:"Pages created"`
	Facts   int      `json:"facts" doc:"Facts created"`
	Skipped int      `json:"skipped" doc:"Pages with a slug the scope has already, and facts whose text it has already"`
	Notes   []string `json:"notes,omitempty" doc:"What differed from the file, e.g. a core page imported as a plain page because the core budget is full"`
}

// importItem is one checked section of an import file.
type importItem struct {
	isPage bool
	page   PageInput
	fact   FactInput
}

// Import writes a markdown export into a scope. The file must be for that scope. It is parsed
// and checked in full before anything is written. Imported pages and facts get tier document
// (a file, not something the user or a verified run said), and the ones the scope already has
// are skipped. The file's own tiers and author are not trusted.
func (s *Service) Import(ctx context.Context, envID, scope, text string) (ImportResult, error) {
	if err := ValidateScope(scope); err != nil {
		return ImportResult{}, err
	}
	items, err := parseExchange(text, scope)
	if err != nil {
		return ImportResult{}, err
	}
	o := Origin{Tier: TierDocument, SourceKind: TierDocument, Evidence: exchangeOrigin}
	var res ImportResult
	pages, err := s.Pages(ctx, envID, scope)
	if err != nil {
		return res, err
	}
	slugs := map[string]bool{}
	for _, p := range pages {
		slugs[p.Slug] = true
	}
	facts, err := s.Facts(ctx, envID, FactFilter{Scope: scope, Status: StatusActive})
	if err != nil {
		return res, err
	}
	texts := map[string]bool{}
	for _, f := range facts {
		texts[normalizeText(f.Text)] = true
	}
	for _, it := range items {
		if it.isPage {
			if slugs[it.page.Slug] {
				res.Skipped++
				continue
			}
			slugs[it.page.Slug] = true
			if _, err := s.CreatePage(ctx, envID, it.page, o); errors.Is(err, ErrCoreBudget) {
				in := it.page
				in.AlwaysLoad = false
				if _, err := s.CreatePage(ctx, envID, in, o); err != nil {
					return res, err
				}
				res.Notes = append(res.Notes, fmt.Sprintf("page %s imported as a plain page: the core budget of %s is full", in.Slug, scope))
			} else if err != nil {
				return res, err
			}
			res.Pages++
			continue
		}
		key := normalizeText(it.fact.Text)
		if texts[key] {
			res.Skipped++
			continue
		}
		texts[key] = true
		in := it.fact
		in.Scope = scope
		if _, err := s.CreateFact(ctx, envID, in, o); err != nil {
			return res, err
		}
		res.Facts++
	}
	return res, nil
}

func normalizeText(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// exchangeReader walks an import file by byte offset.
type exchangeReader struct {
	text string
	pos  int
}

func (r *exchangeReader) errf(format string, args ...any) error {
	line := strings.Count(r.text[:min(r.pos, len(r.text))], "\n") + 1
	return fmt.Errorf("%w: line %d: %s", ErrInvalid, line, fmt.Sprintf(format, args...))
}

func (r *exchangeReader) eof() bool { return r.pos >= len(r.text) }

// line returns the next line without its newline.
func (r *exchangeReader) line() string {
	rest := r.text[r.pos:]
	i := strings.IndexByte(rest, '\n')
	if i < 0 {
		r.pos = len(r.text)
		return rest
	}
	r.pos += i + 1
	return rest[:i]
}

// skipNewlines moves past blank lines.
func (r *exchangeReader) skipNewlines() {
	for !r.eof() && r.text[r.pos] == '\n' {
		r.pos++
	}
}

// frontMatter reads a block of "key: value" lines between two "---" lines. Every key must be
// one of allowed, and none may repeat.
func (r *exchangeReader) frontMatter(allowed []string) (map[string]string, error) {
	if r.line() != "---" {
		return nil, r.errf("front matter must open with ---")
	}
	fm := map[string]string{}
	for {
		if r.eof() {
			return nil, r.errf("front matter is not closed with ---")
		}
		l := r.line()
		if l == "---" {
			return fm, nil
		}
		if len(fm) >= exchangeMaxKeys {
			return nil, r.errf("too many front matter lines")
		}
		key, value, ok := strings.Cut(l, ":")
		if !ok || key == "" || strings.TrimSpace(key) != key {
			return nil, r.errf("front matter line %q is not key: value", l)
		}
		if !containsString(allowed, key) {
			return nil, r.errf("unknown key %q", key)
		}
		if _, dup := fm[key]; dup {
			return nil, r.errf("key %q given twice", key)
		}
		fm[key] = strings.TrimPrefix(value, " ")
	}
}

// body reads exactly n bytes, then nothing but newlines before the next section or the end.
func (r *exchangeReader) body(n int) (string, error) {
	if n < 0 || r.pos+n > len(r.text) {
		return "", r.errf("the body is cut short")
	}
	body := r.text[r.pos : r.pos+n]
	r.pos += n
	if !utf8.ValidString(body) {
		return "", r.errf("the body is not UTF-8")
	}
	r.skipNewlines()
	if !r.eof() && !strings.HasPrefix(r.text[r.pos:], "## ") {
		return "", r.errf("text after the body that is not a section")
	}
	return body, nil
}

// require checks that every key in keys is in fm.
func (r *exchangeReader) require(fm map[string]string, keys ...string) error {
	for _, k := range keys {
		if _, ok := fm[k]; !ok {
			return r.errf("key %q is missing", k)
		}
	}
	return nil
}

// bodyLength reads the "bytes" key: a plain non-negative integer up to max.
func (r *exchangeReader) bodyLength(fm map[string]string, max int) (int, error) {
	n, err := strconv.Atoi(fm["bytes"])
	if err != nil || n < 0 || n > max || strconv.Itoa(n) != fm["bytes"] {
		return 0, r.errf("bytes %q must be a number up to %d", fm["bytes"], max)
	}
	return n, nil
}

func (r *exchangeReader) oneOf(key, v string, allowed []string) error {
	if err := oneOf(key, v, allowed); err != nil {
		return r.errf("%v", err)
	}
	return nil
}

// parseExchange reads and checks an import file for scope.
func parseExchange(text, scope string) ([]importItem, error) {
	if len(text) > exchangeMaxBytes {
		return nil, fmt.Errorf("%w: the file is larger than 2 MiB", ErrInvalid)
	}
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: the file is not UTF-8", ErrInvalid)
	}
	r := &exchangeReader{text: text}
	head, err := r.frontMatter([]string{"studio-memory", "scope", "exported"})
	if err != nil {
		return nil, err
	}
	if err := r.require(head, "studio-memory", "scope", "exported"); err != nil {
		return nil, err
	}
	if head["studio-memory"] != exchangeFormat {
		return nil, r.errf("format %q is not one this Studio reads", head["studio-memory"])
	}
	if _, err := time.Parse(time.RFC3339, head["exported"]); err != nil {
		return nil, r.errf("exported %q is not a time", head["exported"])
	}
	if head["scope"] != scope {
		return nil, fmt.Errorf("%w: this file is for scope %q, not %q", ErrInvalid, head["scope"], scope)
	}

	var items []importItem
	for {
		r.skipNewlines()
		if r.eof() {
			return items, nil
		}
		if len(items) >= exchangeMaxItems {
			return nil, r.errf("more than %d sections", exchangeMaxItems)
		}
		header, ok := strings.CutPrefix(r.line(), "## ")
		if !ok {
			return nil, r.errf("expected a section")
		}
		kind, slug, _ := strings.Cut(header, " ")
		switch kind {
		case "page":
			if !slugPattern.MatchString(slug) || len(slug) > 128 {
				return nil, r.errf("page slug %q is not valid", slug)
			}
			fm, err := r.frontMatter([]string{"title", "kind", "core", "tier", "bytes"})
			if err != nil {
				return nil, err
			}
			if err := r.require(fm, "title", "kind", "core", "tier", "bytes"); err != nil {
				return nil, err
			}
			if err := r.oneOf("kind", fm["kind"], pageKinds); err != nil {
				return nil, err
			}
			if err := r.oneOf("tier", fm["tier"], tiers); err != nil {
				return nil, err
			}
			if fm["core"] != "true" && fm["core"] != "false" {
				return nil, r.errf("core %q must be true or false", fm["core"])
			}
			n, err := r.bodyLength(fm, 100000*utf8.UTFMax)
			if err != nil {
				return nil, err
			}
			body, err := r.body(n)
			if err != nil {
				return nil, err
			}
			p := PageInput{Scope: scope, Slug: slug, Title: fm["title"], Kind: fm["kind"], Compiled: body, AlwaysLoad: fm["core"] == "true"}
			if err := p.validate(); err != nil {
				return nil, r.errf("%v", err)
			}
			if CredentialLike.MatchString(body) || CredentialLike.MatchString(p.Title) {
				return nil, r.errf("page %s contains something like a credential", slug)
			}
			items = append(items, importItem{isPage: true, page: p})
		case "fact":
			if slug != "" {
				return nil, r.errf("a fact section takes no name")
			}
			fm, err := r.frontMatter([]string{"kind", "tier", "observed", "entities", "attribute", "context", "bytes"})
			if err != nil {
				return nil, err
			}
			if err := r.require(fm, "kind", "tier", "bytes"); err != nil {
				return nil, err
			}
			if err := r.oneOf("kind", fm["kind"], factKinds); err != nil {
				return nil, err
			}
			if err := r.oneOf("tier", fm["tier"], tiers); err != nil {
				return nil, err
			}
			var in FactInput
			if fm["observed"] != "" {
				t, err := time.Parse(time.DateOnly, fm["observed"])
				if err != nil {
					return nil, r.errf("observed %q is not a date", fm["observed"])
				}
				in.ObservedAt = &t
			}
			if fm["entities"] != "" {
				if err := json.Unmarshal([]byte(fm["entities"]), &in.EntityIDs); err != nil || len(in.EntityIDs) > 32 {
					return nil, r.errf("entities must be a JSON list of up to 32 names")
				}
				for _, e := range in.EntityIDs {
					if err := notEmpty("entity", e, 128); err != nil {
						return nil, r.errf("%v", err)
					}
				}
			}
			if len(fm["attribute"]) > 128 {
				return nil, r.errf("attribute is longer than 128 characters")
			}
			in.Attribute, in.Context = fm["attribute"], fm["context"]
			n, err := r.bodyLength(fm, 2000*utf8.UTFMax)
			if err != nil {
				return nil, err
			}
			if in.Text, err = r.body(n); err != nil {
				return nil, err
			}
			in.Scope, in.Kind = scope, fm["kind"]
			if err := in.validate(); err != nil {
				return nil, r.errf("%v", err)
			}
			if CredentialLike.MatchString(in.Text) || CredentialLike.MatchString(in.Attribute) || CredentialLike.MatchString(in.Context) {
				return nil, r.errf("a fact contains something like a credential")
			}
			items = append(items, importItem{fact: in})
		default:
			return nil, r.errf("section %q is not a page or a fact", kind)
		}
	}
}
