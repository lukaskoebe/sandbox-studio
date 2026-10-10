package agentmem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type tool func(s *Service, ctx context.Context, c caller, params json.RawMessage) (any, error)

var tools = map[string]tool{
	agentproto.MethodMemorySearch: (*Service).toolSearch,
	agentproto.MethodMemoryGet:    (*Service).toolGet,
	agentproto.MethodRemember:     (*Service).toolRemember,
	agentproto.MethodShare:        (*Service).toolShare,
	agentproto.MethodCorrect:      (*Service).toolCorrect,
	agentproto.MethodForget:       (*Service).toolForget,
}

// decode reads tool arguments strictly: unknown fields, such as a persona or scope the
// agent made up, are refused rather than ignored.
func decode(params json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return userErrorf("invalid arguments: %v", err)
	}
	return nil
}

// readable reports whether the caller may read something of scope.
func (c caller) readable(scope string) bool { return scope == memory.SharedScope || scope == c.scope }

// hitView is a search hit as a tool returns it.
type hitView struct {
	Type     string  `json:"type"`
	ID       string  `json:"id"`
	Scope    string  `json:"scope"`
	Slug     string  `json:"slug,omitempty"`
	Text     string  `json:"text"`
	Kind     string  `json:"kind,omitempty"`
	Tier     string  `json:"tier"`
	Disputed bool    `json:"disputed,omitempty"`
	Score    float64 `json:"score"`
	Why      string  `json:"why"`
}

func (s *Service) toolSearch(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, userErrorf("query is empty")
	}
	if in.Limit <= 0 || in.Limit > 20 {
		in.Limit = 8
	}
	hits, err := s.Memory.Search(ctx, c.env, memory.SearchRequest{Query: truncate(in.Query, 2000), Persona: c.persona.ID, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	out := []hitView{}
	for _, h := range hits {
		if !c.readable(h.Scope) { // Search is already scoped; this is a second lock
			continue
		}
		v := hitView{Type: h.Type, ID: h.ID, Scope: scopeLabel(h.Scope), Text: h.Snippet, Tier: h.Why.Tier, Disputed: h.Disputed, Score: h.Score, Why: h.Why.Summary}
		if h.Fact != nil {
			v.Text, v.Kind = h.Fact.Text, h.Fact.Kind
		}
		if h.Page != nil {
			v.Slug = h.Page.Slug
		}
		out = append(out, v)
	}
	se, _ := s.latestSession(ctx, c)
	s.log(ctx, c, se.id, entry{kind: LogTool, summary: fmt.Sprintf("memory_search %q: %d hits", truncate(in.Query, 80), len(out))})
	return map[string]any{"hits": out}, nil
}

func (s *Service) toolGet(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	notFound := userErrorf("not found in your memory or the shared memory")
	se, _ := s.latestSession(ctx, c)
	switch {
	case in.ID != "":
		if f, err := s.Memory.Fact(ctx, c.env, in.ID); err == nil {
			if !c.readable(f.Scope) {
				return nil, notFound
			}
			s.log(ctx, c, se.id, entry{kind: LogTool, itemType: "fact", itemID: f.ID, summary: "memory_get fact"})
			return f, nil
		} else if !errors.Is(err, memory.ErrNotFound) {
			return nil, err
		}
		p, err := s.Memory.Page(ctx, c.env, in.ID)
		if errors.Is(err, memory.ErrNotFound) || (err == nil && !c.readable(p.Scope)) {
			return nil, notFound
		}
		if err != nil {
			return nil, err
		}
		s.log(ctx, c, se.id, entry{kind: LogTool, itemType: "page", itemID: p.ID, summary: "memory_get page " + p.Slug})
		return p, nil
	case in.Slug != "":
		for _, scope := range []string{c.scope, memory.SharedScope} {
			p, err := s.Memory.PageBySlug(ctx, c.env, scope, in.Slug)
			if errors.Is(err, memory.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			full, err := s.Memory.Page(ctx, c.env, p.ID)
			if err != nil {
				return nil, err
			}
			s.log(ctx, c, se.id, entry{kind: LogTool, itemType: "page", itemID: p.ID, summary: "memory_get page " + p.Slug})
			return full, nil
		}
		return nil, notFound
	}
	return nil, userErrorf("give id or slug")
}

// candidate is a fact to write, from a tool or from extraction.
type candidate struct {
	Text      string
	Kind      string
	Entities  []string
	Attribute string
	Tier      string
	Evidence  string
}

var entityUnsafe = regexp.MustCompile(`[^a-z0-9._/-]+`)

// entityID normalizes a free-form entity name: "Acme API" → "acme-api".
func entityID(name string) string {
	e := entityUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	return truncate(strings.Trim(e, "-./"), 64)
}

func (s *Service) toolRemember(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		Text     string `json:"text"`
		Kind     string `json:"kind"`
		Entity   string `json:"entity"`
		Scope    string `json:"scope"`
		Source   string `json:"source"`
		Evidence string `json:"evidence"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	switch in.Source {
	case "":
		in.Source = memory.TierInferred
	case memory.TierUser, memory.TierVerified, memory.TierInferred:
	default:
		return nil, userErrorf("source must be user, verified or inferred")
	}
	cand := candidate{Text: strings.TrimSpace(in.Text), Kind: in.Kind, Tier: in.Source, Evidence: truncate(in.Evidence, 500)}
	if e := entityID(in.Entity); e != "" {
		cand.Entities = []string{e}
	}
	if err := cand.check(); err != nil {
		return nil, err
	}
	se, err := s.latestSession(ctx, c)
	if err != nil {
		return nil, err
	}
	switch in.Scope {
	case "", "persona":
	case "shared":
		return s.requestShare(ctx, c, se, shareRequest{Text: cand.Text, Kind: cand.Kind, EntityIDs: cand.Entities, Tier: cand.Tier, Evidence: cand.Evidence})
	default:
		return nil, userErrorf("scope must be persona or shared")
	}
	f, how, err := s.write(ctx, c, se, cand, "remember")
	if err != nil {
		return nil, err
	}
	return map[string]any{"fact": f, "result": how}, nil
}

func (cand *candidate) check() error {
	if cand.Text == "" || len([]rune(cand.Text)) > 2000 {
		return userErrorf("text must have 1 to 2000 characters")
	}
	switch cand.Kind {
	case memory.KindPreference, memory.KindDecision, memory.KindFact, memory.KindProcedure, memory.KindEvent:
	default:
		return userErrorf("kind must be preference, decision, fact, procedure or event")
	}
	if secretLike.MatchString(cand.Text) || secretLike.MatchString(cand.Evidence) {
		return userErrorf("that looks like a credential; memory never stores secrets")
	}
	return nil
}

// secretLike matches API keys and Studio's placeholders, which memory never keeps.
var secretLike = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|studio-[0-9a-f]{32}|ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})`)

// writeWhy is the log's reason for a write.
type writeWhy struct {
	Via      string `json:"via"` // remember or extraction
	Tier     string `json:"tier"`
	Evidence string `json:"evidence,omitempty"`
	Dedupe   string `json:"dedupe,omitempty"`
}

// write saves cand in the caller's scope, or reinforces a fact it duplicates.
func (s *Service) write(ctx context.Context, c caller, se session, cand candidate, via string) (memory.Fact, string, error) {
	o := memory.Origin{Tier: cand.Tier, AuthorPersona: c.persona.ID, SessionRef: se.ref, Evidence: cand.Evidence}
	dup, reason, err := s.duplicate(ctx, c.env, c.scope, cand)
	if err != nil {
		return memory.Fact{}, "", err
	}
	why := writeWhy{Via: via, Tier: cand.Tier, Evidence: truncate(cand.Evidence, 300), Dedupe: reason}
	if dup != nil {
		f, err := s.Memory.Reinforce(ctx, c.env, dup.ID, o)
		if err != nil {
			return f, "", err
		}
		s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "fact", itemID: f.ID, why: why, summary: via + " reinforced: " + f.Text})
		return f, "reinforced an existing fact (" + reason + ")", nil
	}
	f, err := s.Memory.CreateFact(ctx, c.env, memory.FactInput{Scope: c.scope, Kind: cand.Kind, EntityIDs: cand.Entities, Attribute: cand.Attribute, Text: cand.Text}, o)
	if err != nil {
		return f, "", err
	}
	s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "fact", itemID: f.ID, why: why, summary: via + " saved (" + f.Tier + "): " + f.Text})
	return f, "saved", nil
}

// ownFact returns a fact of the caller's own scope; others' facts look like missing ones.
func (s *Service) ownFact(ctx context.Context, c caller, id string) (memory.Fact, error) {
	f, err := s.Memory.Fact(ctx, c.env, id)
	if errors.Is(err, memory.ErrNotFound) || (err == nil && f.Scope != c.scope) {
		return f, userErrorf("fact %s is not in your memory; you can only change your own facts", id)
	}
	return f, err
}

func (s *Service) toolCorrect(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		FactID string `json:"fact_id"`
		Text   string `json:"text"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	old, err := s.ownFact(ctx, c, in.FactID)
	if err != nil {
		return nil, err
	}
	if old.Status != memory.StatusActive && old.Status != memory.StatusDisputed {
		return nil, userErrorf("fact %s is %s", old.ID, old.Status)
	}
	cand := candidate{Text: strings.TrimSpace(in.Text), Kind: old.Kind}
	if err := cand.check(); err != nil {
		return nil, err
	}
	se, err := s.latestSession(ctx, c)
	if err != nil {
		return nil, err
	}
	f, err := s.Memory.CreateFact(ctx, c.env, memory.FactInput{Scope: c.scope, Kind: old.Kind, EntityIDs: old.EntityIDs, Attribute: old.Attribute,
		Text: cand.Text, Supersedes: old.ID}, memory.Origin{Tier: memory.TierInferred, AuthorPersona: c.persona.ID, SessionRef: se.ref, Evidence: "correction of " + old.ID})
	if err != nil {
		return nil, err
	}
	s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "fact", itemID: f.ID, summary: "corrected " + old.ID + ": " + f.Text})
	return map[string]any{"fact": f, "superseded": old.ID}, nil
}

func (s *Service) toolForget(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		FactID string `json:"fact_id"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	old, err := s.ownFact(ctx, c, in.FactID)
	if err != nil {
		return nil, err
	}
	f, err := s.Memory.SetFactStatus(ctx, c.env, old.ID, memory.StatusRetracted)
	if err != nil {
		return nil, err
	}
	se, _ := s.latestSession(ctx, c)
	s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "fact", itemID: f.ID, summary: "retracted: " + f.Text})
	return map[string]any{"retracted": f.ID}, nil
}

// --- sharing ----------------------------------------------------------------------------

// SharePayload is a memory.share approval's payload: the fact a persona proposes for
// shared memory.
type SharePayload struct {
	PersonaID   string   `json:"personaId"`
	PersonaName string   `json:"personaName"`
	FactID      string   `json:"factId,omitempty" doc:"The persona's own fact, when it shared one"`
	Text        string   `json:"text"`
	Kind        string   `json:"kind"`
	EntityIDs   []string `json:"entityIds"`
	Attribute   string   `json:"attribute,omitempty"`
	Tier        string   `json:"tier"`
	Evidence    string   `json:"evidence,omitempty"`
	SessionRef  string   `json:"sessionRef,omitempty"`
}

type shareRequest struct {
	FactID    string
	Text      string
	Kind      string
	EntityIDs []string
	Attribute string
	Tier      string
	Evidence  string
}

func (s *Service) toolShare(ctx context.Context, c caller, params json.RawMessage) (any, error) {
	var in struct {
		FactID string `json:"fact_id"`
		Text   string `json:"text"`
		Kind   string `json:"kind"`
		Entity string `json:"entity"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	se, err := s.latestSession(ctx, c)
	if err != nil {
		return nil, err
	}
	if in.FactID != "" {
		f, err := s.ownFact(ctx, c, in.FactID)
		if err != nil {
			return nil, err
		}
		if f.Status != memory.StatusActive {
			return nil, userErrorf("fact %s is %s", f.ID, f.Status)
		}
		return s.requestShare(ctx, c, se, shareRequest{FactID: f.ID, Text: f.Text, Kind: f.Kind, EntityIDs: f.EntityIDs, Attribute: f.Attribute, Tier: f.Tier})
	}
	cand := candidate{Text: strings.TrimSpace(in.Text), Kind: in.Kind, Tier: memory.TierInferred}
	if cand.Kind == "" {
		cand.Kind = memory.KindFact
	}
	if e := entityID(in.Entity); e != "" {
		cand.Entities = []string{e}
	}
	if err := cand.check(); err != nil {
		return nil, err
	}
	return s.requestShare(ctx, c, se, shareRequest{Text: cand.Text, Kind: cand.Kind, EntityIDs: cand.Entities, Tier: cand.Tier})
}

// requestShare creates a pending memory.share approval. Shared memory changes only when the
// user approves it (DecideShare).
func (s *Service) requestShare(ctx context.Context, c caller, se session, r shareRequest) (any, error) {
	if r.EntityIDs == nil {
		r.EntityIDs = []string{}
	}
	if r.Tier == memory.TierDocument || r.Tier == "" {
		r.Tier = memory.TierInferred
	}
	payload, _ := json.Marshal(SharePayload{PersonaID: c.persona.ID, PersonaName: c.persona.Name, FactID: r.FactID, Text: r.Text, Kind: r.Kind,
		EntityIDs: r.EntityIDs, Attribute: r.Attribute, Tier: r.Tier, Evidence: r.Evidence, SessionRef: se.ref})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.env, SandboxID: c.sandbox.ID, Kind: ApprovalKind,
		Subject: truncate(c.persona.Name+": "+r.Text, 300), Payload: payload})
	if err != nil {
		return nil, err
	}
	s.log(ctx, c, se.id, entry{kind: LogWrite, itemType: "approval", itemID: a.ID, summary: "proposed for shared memory: " + r.Text})
	return map[string]any{"approvalId": a.ID, "status": a.Status,
		"note": "The user decides in Studio's inbox; the fact joins shared memory when approved."}, nil
}

// DecideShare applies the user's decision on a memory.share approval: approved writes the
// fact to shared memory with the proposing persona as author; anything else writes nothing.
// The caller marks the approval decided.
func (s *Service) DecideShare(ctx context.Context, a store.Approval, approved bool) (*memory.Fact, error) {
	if a.Kind != ApprovalKind {
		return nil, fmt.Errorf("approval %s is not a memory share", a.ID)
	}
	var p SharePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return nil, fmt.Errorf("share payload: %w", err)
	}
	c := caller{env: a.EnvironmentID, sandbox: store.Sandbox{ID: a.SandboxID}, persona: store.Persona{ID: p.PersonaID, Name: p.PersonaName}, scope: memory.SharedScope}
	if !approved {
		s.log(ctx, c, "", entry{kind: LogWrite, itemType: "approval", itemID: a.ID, summary: "share declined: " + p.Text})
		return nil, nil
	}
	author := p.PersonaID
	if _, err := s.Store.Persona(ctx, a.EnvironmentID, author); err != nil {
		author = "" // the persona is gone; the user's approval still stands
	}
	o := memory.Origin{Tier: p.Tier, AuthorPersona: author, SessionRef: p.SessionRef,
		Evidence: truncate("shared by "+p.PersonaName+", approved by the user. "+p.Evidence, 500)}
	cand := candidate{Text: p.Text, Kind: p.Kind, Entities: p.EntityIDs, Attribute: p.Attribute}
	dup, _, err := s.duplicate(ctx, a.EnvironmentID, memory.SharedScope, cand)
	if err != nil {
		return nil, err
	}
	var f memory.Fact
	if dup != nil {
		f, err = s.Memory.Reinforce(ctx, a.EnvironmentID, dup.ID, o)
	} else {
		f, err = s.Memory.CreateFact(ctx, a.EnvironmentID, memory.FactInput{Scope: memory.SharedScope, Kind: p.Kind, EntityIDs: p.EntityIDs,
			Attribute: p.Attribute, Text: p.Text}, o)
	}
	if err != nil {
		return nil, err
	}
	s.log(ctx, c, "", entry{kind: LogWrite, itemType: "fact", itemID: f.ID, summary: "shared after approval: " + f.Text})
	return &f, nil
}
