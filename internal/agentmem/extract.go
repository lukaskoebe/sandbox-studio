package agentmem

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// Extraction limits.
const (
	maxFacts          = 20
	maxFactText       = 500
	maxEvidence       = 300
	maxEntities       = 8
	maxSummary        = 600 // a session's running summary, in characters
	maxOutputTokens   = 2000
	extractTimeout    = 2 * time.Minute
	dupSimilarity     = 0.92 // the same fact in other words
	dupAttrSimilarity = 0.75 // the same entity attribute, close enough
	dupJaccard        = 0.8  // word overlap, when vectors are not there yet
)

// errOverBudget is what extract returns when it skipped a run for today's budget, so a chat
// can wait for the next day's.
var errOverBudget = errors.New("over the daily budget")

type extractJob struct {
	c  caller
	se session
	ev agenthook.Event
}

// enqueue hands a transcript delta to the extraction worker. A full queue is an error, so
// the guest keeps its offset and sends the delta again.
func (s *Service) enqueue(c caller, se session, ev agenthook.Event) error {
	if strings.TrimSpace(ev.Transcript) == "" {
		return nil
	}
	select {
	case s.jobs <- extractJob{c: c, se: se, ev: ev}:
		return nil
	default:
		return errors.New("extraction queue is full")
	}
}

// Run extracts queued transcripts and idle chats until ctx ends. One worker, so the daily
// budget is checked and spent in order, and at most one extraction, and one utility VM for it,
// is in memory at a time. Every chatSweep it also catches up on the chats that weren't read to
// the end (sweepChats), a few at a time between the other work.
func (s *Service) Run(ctx context.Context) {
	sweep := time.NewTimer(s.chatIdle()) // the first once Studio has settled
	defer sweep.Stop()
	var backlog []chatDue
	for {
		var next <-chan time.Time // ready while the backlog has a chat
		if len(backlog) > 0 {
			next = ready
		}
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if len(backlog) == 0 {
				backlog = s.sweepChats(ctx)
			}
			sweep.Reset(chatSweep)
		case <-next:
			s.runChat(ctx, backlog[0])
			backlog = backlog[1:]
		case d := <-s.chatsDue:
			s.runChat(ctx, d)
		case j := <-s.jobs:
			jctx, cancel := context.WithTimeout(ctx, extractTimeout)
			if err := s.extract(jctx, j); err != nil && !errors.Is(err, errOverBudget) {
				s.Log.Warn("memory extraction failed", "sandbox", j.c.sandbox.ID, "teammate", j.c.teammate.ID, "err", err)
			}
			cancel()
		}
	}
}

// ready is always ready to receive from.
var ready = func() <-chan time.Time {
	c := make(chan time.Time)
	close(c)
	return c
}()

func (s *Service) runChat(ctx context.Context, d chatDue) {
	jctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	if err := s.extractChat(jctx, d); err != nil && ctx.Err() == nil {
		s.Log.Warn("memory extraction of a chat failed", "chat", d.chat, "err", err)
	}
}

// extractWhy is the log's record of an extraction run.
type extractWhy struct {
	Event        string `json:"event"`
	Model        string `json:"model,omitempty"`
	Chars        int    `json:"chars"`
	InputTokens  int    `json:"inputTokens,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	CostMicros   int64  `json:"costMicros,omitempty"`
	Facts        int    `json:"facts"`
	Skipped      string `json:"skipped,omitempty"`
	Rejected     string `json:"rejected,omitempty"`
}

func (s *Service) extract(ctx context.Context, j extractJob) error {
	c, se := j.c, j.se
	why := extractWhy{Event: j.ev.Event, Chars: len(j.ev.Transcript)}
	done := func(summary string) {
		s.log(ctx, c, se.id, entry{kind: LogExtraction, why: why, summary: summary})
	}
	skip := func(reason string) error {
		why.Skipped = reason
		s.countSkip(ctx, c.env)
		done("extraction skipped: " + reason)
		return nil
	}
	if c.teammate.ProviderID == "" {
		return skip("the teammate has no provider")
	}
	p, err := s.Store.Provider(ctx, c.env, c.teammate.ProviderID)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}
	model, ok := UtilityFor(p)
	if !ok {
		return skip(model.Note)
	}
	why.Model = model.Model

	previous, err := s.sessionSummary(ctx, se.id)
	if err != nil {
		return err
	}
	system, user := extractionPrompt(c, j.ev.Transcript, previous)
	estIn := (len(system) + len(user)) / 3
	if reason, err := s.overBudget(ctx, c.env, model, estIn); err != nil {
		return err
	} else if reason != "" {
		_ = skip(reason)
		return errOverBudget
	}
	key := ""
	if p.SecretID != "" {
		if key, err = s.Keys.Value(ctx, c.env, p.SecretID); err != nil {
			return errors.New("reading the provider key failed") // never the key or its error text
		}
	}
	utility := s.Utility
	if utility == nil {
		utility = DefaultUtility
	}
	llm, model, err := utility(p, key)
	if err != nil {
		return skip(err.Error())
	}
	resp, err := llm.Complete(ctx, Request{System: system, User: user, Schema: factsSchema, MaxTokens: maxOutputTokens})
	if errors.Is(err, ErrLowMemory) {
		return err // no call was made
	}
	in, out := resp.InputTokens, resp.OutputTokens
	if in == 0 && out == 0 { // no usage reported: charge the estimate
		in, out = estIn, len(resp.Text)/3
	}
	why.InputTokens, why.OutputTokens, why.CostMicros = in, out, model.cost(in, out)
	if uerr := s.spend(ctx, c.env, in, out, why.CostMicros); uerr != nil {
		s.Log.Warn("recording extraction usage failed", "err", uerr)
	}
	if err != nil {
		why.Rejected = "call failed"
		done("extraction failed: " + truncate(err.Error(), 200))
		return err
	}
	facts, summary, err := parseFacts(resp.Text, j.ev.Transcript)
	if err != nil {
		why.Rejected = err.Error()
		done("extraction output rejected: " + err.Error())
		return nil
	}
	why.Facts = len(facts)
	done(fmt.Sprintf("extracted %d facts with %s", len(facts), model.Model))
	if summary != "" {
		if err := s.setSessionSummary(ctx, se.id, summary); err != nil {
			return err
		}
	}
	for _, f := range facts {
		if _, _, err := s.write(ctx, c, se, f, "extraction"); err != nil {
			return err
		}
	}
	return nil
}

// --- the prompt and its answer ----------------------------------------------------------

var factsSchema = map[string]any{
	"type": "object", "additionalProperties": false, "required": []string{"facts", "summary"},
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"facts": map[string]any{
			"type": "array", "maxItems": maxFacts,
			"items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"text", "kind", "tier", "entities", "attribute", "evidence"},
				"properties": map[string]any{
					"text":      map[string]any{"type": "string"},
					"kind":      map[string]any{"type": "string", "enum": []string{memory.KindPreference, memory.KindDecision, memory.KindFact, memory.KindProcedure, memory.KindEvent}},
					"tier":      map[string]any{"type": "string", "enum": []string{memory.TierUser, memory.TierInferred}},
					"entities":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"attribute": map[string]any{"type": "string"},
					"evidence":  map[string]any{"type": "string"},
				},
			},
		},
	},
}

func extractionPrompt(c caller, transcript, previous string) (system, user string) {
	schema, _ := json.Marshal(factsSchema)
	system = `You extract durable memories from a coding agent's session for the agent's teammate "` + c.teammate.Name + `".
Keep only what will still matter in a later session: the user's preferences, decisions and their reasons, facts about people, projects and systems, and procedures that worked. Skip chit-chat, transient state, things any engineer knows, anything already marked as memory, and secrets of any kind.
The session ran in the sandbox "` + c.sandbox.Name + `", one of several separate machines the teammate works in. A fact about the state of this sandbox (its files, branches, installed tools, services or paths) names it, e.g. "In sandbox ` + c.sandbox.Name + `, …"; preferences, decisions and procedures hold everywhere and name no sandbox.
Each fact is one short self-contained sentence. tier is "user" only when the user said it, otherwise "inferred". entities are short lowercase names (a project, person, service or tool). attribute is a short key such as "test_command" when the fact sets one property of an entity, else "". evidence is a verbatim quote of at most 300 characters from the transcript that supports the fact.
summary is the session so far in at most 400 characters, for the teammate's next sessions: what the user wanted, what was done and where it was left, naming the sandbox's project. It updates the previous summary with this excerpt; keep what still matters of it. No secrets.
Answer with JSON only, no prose, matching this schema: ` + string(schema) + `
When no fact is worth keeping, answer with "facts": [] and the summary.`
	user = "Transcript excerpt:\n\n" + transcript
	if previous != "" {
		user = "Summary of the session before this excerpt:\n\n" + previous + "\n\n" + user
	}
	return system, user
}

var (
	fence        = regexp.MustCompile("(?s)^\\s*```[a-z]*\\s*\\n(.*?)\\n\\s*```\\s*$")
	attributeKey = regexp.MustCompile(`^[a-z0-9_.-]{0,128}$`)
)

type factOut struct {
	Text      string   `json:"text"`
	Kind      string   `json:"kind"`
	Tier      string   `json:"tier"`
	Entities  []string `json:"entities"`
	Attribute string   `json:"attribute"`
	Evidence  string   `json:"evidence"`
}

// parseFacts validates the model's answer strictly: one violation rejects it all. A summary
// that is too long is cut, one that looks like a credential dropped.
func parseFacts(text, transcript string) ([]candidate, string, error) {
	if m := fence.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(text))))
	dec.DisallowUnknownFields()
	var out struct {
		Facts   *[]factOut `json:"facts"`
		Summary string     `json:"summary"`
	}
	if err := dec.Decode(&out); err != nil {
		return nil, "", fmt.Errorf("not the schema: %v", err)
	}
	if dec.More() {
		return nil, "", errors.New("not the schema: trailing data")
	}
	if out.Facts == nil {
		return nil, "", errors.New("not the schema: no facts array")
	}
	if len(*out.Facts) > maxFacts {
		return nil, "", fmt.Errorf("more than %d facts", maxFacts)
	}
	norm := normalize(transcript)
	var res []candidate
	for i, f := range *out.Facts {
		bad := func(why string) error { return fmt.Errorf("fact %d: %s", i+1, why) }
		f.Text, f.Evidence = strings.TrimSpace(f.Text), strings.TrimSpace(f.Evidence)
		switch {
		case f.Text == "" || len([]rune(f.Text)) > maxFactText:
			return nil, "", bad("text must have 1 to 500 characters")
		case f.Evidence == "" || len([]rune(f.Evidence)) > maxEvidence:
			return nil, "", bad("evidence must have 1 to 300 characters")
		case f.Tier != memory.TierUser && f.Tier != memory.TierInferred:
			return nil, "", bad("tier must be user or inferred")
		case len(f.Entities) > maxEntities:
			return nil, "", bad("too many entities")
		case !attributeKey.MatchString(f.Attribute):
			return nil, "", bad("invalid attribute")
		case secretLike.MatchString(f.Text) || secretLike.MatchString(f.Evidence):
			return nil, "", bad("contains something like a credential")
		case !strings.Contains(norm, normalize(f.Evidence)):
			return nil, "", bad("evidence is not in the transcript")
		}
		cand := candidate{Text: f.Text, Kind: f.Kind, Tier: f.Tier, Attribute: f.Attribute, Evidence: f.Evidence}
		if err := cand.check(); err != nil {
			return nil, "", bad(err.Error())
		}
		if cand.Tier == memory.TierUser && !saidByUser(transcript, f.Evidence) {
			cand.Tier = memory.TierInferred // the model's claim that the user said it doesn't hold
		}
		for _, e := range f.Entities {
			if e = entityID(e); e != "" && !containsStr(cand.Entities, e) {
				cand.Entities = append(cand.Entities, e)
			}
		}
		res = append(res, cand)
	}
	summary := strings.Join(strings.Fields(out.Summary), " ")
	if secretLike.MatchString(summary) {
		summary = ""
	}
	return res, truncate(summary, maxSummary), nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// normalize folds case and whitespace for evidence matching.
func normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// saidByUser reports whether evidence is on a "user:" line of the condensed transcript.
func saidByUser(transcript, evidence string) bool {
	ev := normalize(evidence)
	var cur strings.Builder
	user := false
	flush := func() bool { return user && strings.Contains(normalize(cur.String()), ev) }
	for _, line := range strings.Split(transcript, "\n") {
		if strings.HasPrefix(line, "user:") || strings.HasPrefix(line, "assistant:") {
			if flush() {
				return true
			}
			cur.Reset()
			user = strings.HasPrefix(line, "user:")
		}
		cur.WriteString(line + "\n")
	}
	return flush()
}

// --- dedupe -----------------------------------------------------------------------------

// duplicate finds an active fact of scope that cand repeats, and says why it matched.
func (s *Service) duplicate(ctx context.Context, env, scope string, cand candidate) (*memory.Fact, string, error) {
	teammate := strings.TrimPrefix(scope, "teammate:")
	if scope == memory.SharedScope {
		teammate = ""
	}
	hits, err := s.Memory.Search(ctx, env, memory.SearchRequest{Query: cand.Text, Teammate: teammate, Limit: 10})
	if errors.Is(err, memory.ErrInvalid) {
		return nil, "", nil // no searchable words
	}
	if err != nil {
		return nil, "", err
	}
	text := normalize(cand.Text)
	for _, h := range hits {
		f := h.Fact
		if f == nil || h.Scope != scope || f.Status != memory.StatusActive {
			continue
		}
		sim := h.Why.Similarity
		if h.Why.Vector != "used" {
			sim = 0
		}
		switch {
		case normalize(f.Text) == text:
			return f, "same text", nil
		case cand.Attribute != "" && f.Attribute == cand.Attribute && overlaps(f.EntityIDs, cand.Entities) && sim >= dupAttrSimilarity:
			return f, fmt.Sprintf("same attribute %s, similarity %.2f", f.Attribute, sim), nil
		case sim >= dupSimilarity:
			return f, fmt.Sprintf("similarity %.2f", sim), nil
		case jaccard(f.Text, cand.Text) >= dupJaccard:
			return f, fmt.Sprintf("word overlap %.2f", jaccard(f.Text, cand.Text)), nil
		}
	}
	return nil, "", nil
}

func overlaps(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	for _, x := range a {
		if containsStr(b, x) {
			return true
		}
	}
	return false
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range words(s) {
		out[w] = true
	}
	return out
}

func jaccard(a, b string) float64 {
	wa, wb := wordSet(a), wordSet(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(wa)+len(wb)-inter)
}

// --- the daily budget -------------------------------------------------------------------

// Usage is an environment's extraction spending on one UTC day.
type Usage struct {
	Day          string `json:"day"`
	Calls        int    `json:"calls"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	CostMicros   int64  `json:"costMicros" doc:"USD × 10^6, for models with a known price"`
	Skipped      int    `json:"skipped" doc:"Runs skipped: over budget, or no utility model"`
}

func (s *Service) day() string { return s.now().Format(time.DateOnly) }

func (s *Service) usage(ctx context.Context, env string) (Usage, error) {
	u := Usage{Day: s.day()}
	err := s.db.QueryRowContext(ctx, `SELECT calls, input_tokens, output_tokens, cost_micros, skipped FROM memory_utility_usage
		WHERE environment_id = ? AND day = ?`, env, u.Day).Scan(&u.Calls, &u.InputTokens, &u.OutputTokens, &u.CostMicros, &u.Skipped)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	return u, nil
}

// overBudget says why a call of about estIn input tokens would exceed today's budget.
func (s *Service) overBudget(ctx context.Context, env string, m UtilityModel, estIn int) (string, error) {
	u, err := s.usage(ctx, env)
	if err != nil {
		return "", err
	}
	if m.Priced {
		if u.CostMicros+m.cost(estIn, maxOutputTokens) > s.Budget.MaxCostMicros {
			return fmt.Sprintf("daily budget of $%.2f reached", float64(s.Budget.MaxCostMicros)/1e6), nil
		}
		return "", nil
	}
	if u.Calls >= s.Budget.MaxCalls {
		return fmt.Sprintf("daily cap of %d calls reached", s.Budget.MaxCalls), nil
	}
	return "", nil
}

func (s *Service) spend(ctx context.Context, env string, in, out int, cost int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_utility_usage (environment_id, day, calls, input_tokens, output_tokens, cost_micros, skipped)
		VALUES (?, ?, 1, ?, ?, ?, 0)
		ON CONFLICT (environment_id, day) DO UPDATE SET calls = calls + 1, input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens, cost_micros = cost_micros + excluded.cost_micros`,
		env, s.day(), in, out, cost)
	return err
}

func (s *Service) countSkip(ctx context.Context, env string) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_utility_usage (environment_id, day, calls, input_tokens, output_tokens, cost_micros, skipped)
		VALUES (?, ?, 0, 0, 0, 0, 1) ON CONFLICT (environment_id, day) DO UPDATE SET skipped = skipped + 1`, env, s.day()); err != nil {
		s.Log.Warn("recording a skipped extraction failed", "err", err)
	}
}
