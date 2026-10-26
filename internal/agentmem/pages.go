package agentmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Teammates write pages (docs/design/memory-links.md#5-teammates-write-pages).
const (
	maxPageWrites   = 20   // memory_page calls that write, per session
	maxPageCompiled = 8000 // characters of compiled truth a teammate writes at once
)

// PageProposal is a page a teammate proposes in a memory.share approval: a shared page, or
// new compiled truth for a page the user wrote.
type PageProposal struct {
	Scope    string `json:"scope" doc:"shared, or the teammate's own scope for a page the user wrote"`
	PageID   string `json:"pageId,omitempty" doc:"The page it changes; empty for a new page"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Compiled string `json:"compiled" doc:"The proposed compiled truth"`
	Previous string `json:"previous,omitempty" doc:"The compiled truth it replaces"`
	Timeline string `json:"timeline,omitempty" doc:"An entry to append to the page's timeline"`
}

// countPageWrite counts a write against the session's limit.
func (s *Service) countPageWrite(sessionID string) error {
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	if s.pageWrites == nil || len(s.pageWrites) > 10000 {
		s.pageWrites = map[string]int{} // sessions end; forget them now and then
	}
	if s.pageWrites[sessionID] >= maxPageWrites {
		return userErrorf("this session already wrote %d pages; leave the rest as facts with remember", maxPageWrites)
	}
	s.pageWrites[sessionID]++
	return nil
}

func (s *Service) toolPage(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		Slug     string  `json:"slug"`
		Title    string  `json:"title"`
		Kind     string  `json:"kind"`
		Compiled *string `json:"compiled"`
		Timeline string  `json:"timeline"`
		Scope    string  `json:"scope"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if memory.EntitySlug(slug) != slug {
		return nil, userErrorf("slug %q must be lowercase words joined by - and /, such as project/webshop or topic/deploys", in.Slug)
	}
	if in.Compiled == nil && strings.TrimSpace(in.Timeline) == "" {
		return nil, userErrorf("give compiled, timeline or both")
	}
	if in.Compiled != nil && utf8.RuneCountInString(*in.Compiled) > maxPageCompiled {
		return nil, userErrorf("compiled is longer than %d characters; keep the page to what holds now and move the history to the timeline", maxPageCompiled)
	}
	if utf8.RuneCountInString(in.Timeline) > 2000 {
		return nil, userErrorf("timeline is longer than 2000 characters")
	}
	if in.Kind != "" && !slices.Contains([]string{"project", "topic", "person", "procedure"}, in.Kind) {
		return nil, userErrorf("kind must be project, topic, person or procedure")
	}
	for _, text := range []string{in.Title, in.Timeline, derefOr(in.Compiled, "")} {
		if memory.CredentialLike.MatchString(text) {
			return nil, userErrorf("the page has something like a credential; memory keeps none")
		}
	}
	se, err := s.latestSession(ctx, c)
	if err != nil {
		return nil, err
	}
	scope := c.scope
	switch in.Scope {
	case "", "teammate":
	case "shared":
		scope = memory.SharedScope
	default:
		return nil, userErrorf("scope must be teammate or shared")
	}
	old, err := s.Memory.PageBySlug(ctx, c.env, scope, slug)
	exists := err == nil
	if err != nil && !errors.Is(err, memory.ErrNotFound) {
		return nil, err
	}
	prop := PageProposal{Scope: scope, Slug: slug, Title: strings.TrimSpace(in.Title), Kind: in.Kind, Timeline: strings.TrimSpace(in.Timeline)}
	if exists {
		prop.PageID, prop.Previous = old.ID, old.Compiled
		prop.Title, prop.Kind = orDefault(prop.Title, old.Title), orDefault(prop.Kind, old.Kind)
		prop.Compiled = old.Compiled
	} else {
		prop.Title = orDefault(prop.Title, slug[strings.LastIndex(slug, "/")+1:])
		prop.Kind = orDefault(prop.Kind, slugKindOf(slug))
	}
	if in.Compiled != nil {
		prop.Compiled = strings.TrimSpace(*in.Compiled)
	}
	if err := s.countPageWrite(se.id); err != nil {
		return nil, err
	}

	// Shared pages, and new truth for a page the user wrote, wait for the user.
	if scope == memory.SharedScope || (exists && old.Tier == memory.TierUser && in.Compiled != nil && prop.Compiled != old.Compiled) {
		text := "Page " + slug + ": " + orDefault(memory.FirstSentence(prop.Compiled), prop.Timeline)
		payload, _ := json.Marshal(SharePayload{TeammateID: c.teammate.ID, TeammateName: c.teammate.Name, Text: truncate(text, 2000), Kind: "page",
			EntityIDs: []string{}, Tier: memory.TierInferred, SessionRef: se.ref, Page: &prop})
		a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.env, SandboxID: c.sandbox.ID, Kind: ApprovalKind,
			Subject: truncate(c.teammate.Name+": "+text, 300), Payload: payload})
		if err != nil {
			return nil, err
		}
		if s.Notify != nil {
			s.Notify(c.env)
		}
		s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "approval", itemID: a.ID, summary: "proposed page " + slug})
		note := "The user decides in Studio's inbox; the page is written when approved."
		if scope != memory.SharedScope {
			note = "The user wrote this page, so they decide on your new compiled truth in Studio's inbox."
			if prop.Timeline != "" {
				note += " The timeline entry waits with it."
			}
		}
		return map[string]any{"approvalId": a.ID, "status": a.Status, "note": note}, nil
	}

	o := memory.Origin{Tier: memory.TierInferred, AuthorTeammate: c.teammate.ID, SessionRef: se.ref}
	p, err := s.writePage(ctx, c.env, prop, o, nil)
	if err != nil {
		return nil, pageError(err)
	}
	s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "page", itemID: p.ID, summary: "wrote page " + slug})
	return map[string]any{"page": c.pageView(p)}, nil
}

// writePage creates or updates the page of a proposal and appends its timeline entry. An
// existing page keeps its tier when only its timeline changes; also runs in the transaction
// that writes the page.
func (s *Service) writePage(ctx context.Context, env string, prop PageProposal, o memory.Origin, also func(tx *sql.Tx) error) (memory.Page, error) {
	var p memory.Page
	var err error
	cur, getErr := s.Memory.PageBySlug(ctx, env, prop.Scope, prop.Slug)
	switch {
	case errors.Is(getErr, memory.ErrNotFound):
		p, err = s.Memory.CreatePageWith(ctx, env, memory.PageInput{Scope: prop.Scope, Slug: prop.Slug, Title: prop.Title, Kind: prop.Kind, Compiled: prop.Compiled}, o, also)
	case getErr != nil:
		return p, getErr
	case cur.Compiled == prop.Compiled && cur.Title == prop.Title && cur.Kind == prop.Kind && also == nil:
		p = cur // only the timeline changes
	default:
		if cur.Compiled == prop.Compiled {
			o = memory.Origin{Tier: cur.Tier, AuthorTeammate: cur.AuthorTeammate} // a title or kind alone doesn't change who wrote the page
		}
		p, err = s.Memory.UpdatePageWith(ctx, env, cur.ID, memory.PageInput{Title: prop.Title, Kind: prop.Kind, Compiled: prop.Compiled, AlwaysLoad: cur.AlwaysLoad}, o, also)
	}
	if err != nil {
		return p, err
	}
	if prop.Timeline != "" {
		if _, err := s.Memory.AppendTimeline(ctx, env, p.ID, memory.TimelineInput{Text: prop.Timeline}, memory.Origin{Tier: o.Tier, AuthorTeammate: o.AuthorTeammate}); err != nil {
			return p, err
		}
	}
	return s.Memory.Page(ctx, env, p.ID)
}

// decidePage writes an approved page proposal, settling the approval in the same transaction.
func (s *Service) decidePage(ctx context.Context, a store.Approval, p SharePayload, c caller) error {
	author := p.TeammateID
	if _, err := s.Store.Teammate(ctx, a.EnvironmentID, author); err != nil {
		author = ""
	}
	approve := func(tx *sql.Tx) error {
		return store.DecideApprovalTx(ctx, tx, a.EnvironmentID, a.ID, store.StatusApproved, "")
	}
	page, err := s.writePage(ctx, a.EnvironmentID, *p.Page, memory.Origin{Tier: memory.TierInferred, AuthorTeammate: author, SessionRef: p.SessionRef}, approve)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log(ctx, c, "", entry{kind: LogWrite, itemType: "approval", itemID: a.ID, summary: "page write failed, still pending: " + truncate(err.Error(), 200)})
		}
		return err
	}
	s.log(ctx, c, "", entry{kind: LogWrite, itemType: "page", itemID: page.ID, summary: "wrote page " + page.Slug + " after approval"})
	return nil
}

// pageError turns the core budget and invalid input into answers the teammate can act on.
func pageError(err error) error {
	if errors.Is(err, memory.ErrCoreBudget) || errors.Is(err, memory.ErrInvalid) {
		return userErrorf("%s", strings.TrimPrefix(err.Error(), memory.ErrInvalid.Error()+": "))
	}
	return err
}

func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// slugKindOf is the page kind a slug's first part names, topic otherwise.
func slugKindOf(slug string) string {
	head, _, _ := strings.Cut(slug, "/")
	switch head {
	case "project", "person", "procedure":
		return head
	}
	return "topic"
}
