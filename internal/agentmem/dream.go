package agentmem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Consolidation ("dream", PLAN §6.7): per scope, merge near-duplicates, let the utility
// model judge pairs of facts about the same entity or attribute, apply supersessions,
// open conflicts for the user, and regenerate entity pages from the current facts.

// ConflictApprovalKind is the approval a dream opens for a conflict the user must resolve.
const ConflictApprovalKind = "memory.conflict"

const (
	// DreamAfterFacts new facts in a scope start a dream.
	DreamAfterFacts = 25
	// DreamHour is the local hour after which the nightly dream runs.
	DreamHour = 3

	judgePromptVersion = 1
	judgeMaxTokens     = 300
	maxJudgedPairs     = 100 // per run; the rest waits for the next run
	maxRelated         = 8   // facts each new fact is compared with
	autoDreamGap       = time.Hour
	dreamTick          = time.Minute
)

// Dream triggers and statuses.
const (
	TriggerNightly = "nightly"
	TriggerFacts   = "facts"
	TriggerManual  = "manual"

	DreamRunning = "running"
	DreamDone    = "done"
	DreamStopped = "stopped"
	DreamFailed  = "failed"
)

// Judge verdicts.
const (
	VerdictNone          = "no_conflict"
	VerdictDuplicate     = "duplicate"
	VerdictSupersedes    = "supersedes"
	VerdictContradiction = "contradiction"
)

// ErrDreamRunning means the scope is being consolidated already.
var ErrDreamRunning = errors.New("a dream of this scope is already running")

// DreamStats counts what a run did. A resumed run continues the counts.
type DreamStats struct {
	Facts        int    `json:"facts" doc:"New facts in the run's window"`
	Pairs        int    `json:"pairs" doc:"Pairs of related facts to judge"`
	Judged       int    `json:"judged" doc:"Pairs the utility model judged"`
	Cached       int    `json:"cached" doc:"Pairs answered from earlier verdicts"`
	Rejected     int    `json:"rejected" doc:"Judge answers rejected as malformed; nothing was changed for them"`
	Merged       int    `json:"merged" doc:"Near-duplicates merged"`
	Superseded   int    `json:"superseded"`
	Conflicts    int    `json:"conflicts" doc:"Conflicts opened for the user"`
	Pages        int    `json:"pages" doc:"Entity pages created or regenerated"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	CostMicros   int64  `json:"costMicros"`
	Model        string `json:"model,omitempty"`
}

// DreamRun is one consolidation of a scope.
type DreamRun struct {
	ID          string     `json:"id"`
	Scope       string     `json:"scope"`
	Trigger     string     `json:"trigger" enum:"nightly,facts,manual"`
	Status      string     `json:"status" enum:"running,done,stopped,failed" doc:"stopped: the budget or the pair limit ended it early; the next run continues"`
	WindowStart time.Time  `json:"windowStart" doc:"Facts created from here…"`
	WindowEnd   time.Time  `json:"windowEnd" doc:"…to here (both included) are new to this run"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Resumes     int        `json:"resumes" doc:"Times an interrupted run was picked up again"`
	Stats       DreamStats `json:"stats"`
	Note        string     `json:"note,omitempty"`
}

const runCols = "id, scope, trigger, status, window_start, window_end, started_at, finished_at, resumes, stats, note"

func scanRun(row interface{ Scan(...any) error }) (DreamRun, error) {
	var r DreamRun
	var ws, we, started int64
	var finished sql.NullInt64
	var stats string
	err := row.Scan(&r.ID, &r.Scope, &r.Trigger, &r.Status, &ws, &we, &started, &finished, &r.Resumes, &stats, &r.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return r, store.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(stats), &r.Stats)
	r.WindowStart, r.WindowEnd, r.StartedAt = time.Unix(ws, 0).UTC(), time.Unix(we, 0).UTC(), time.Unix(started, 0).UTC()
	if finished.Valid {
		t := time.Unix(finished.Int64, 0).UTC()
		r.FinishedAt = &t
	}
	return r, nil
}

// DreamRuns lists the latest runs of an environment, of one scope if scope is set.
// limit defaults to 20.
func (s *Service) DreamRuns(ctx context.Context, env, scope string, limit int) ([]DreamRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q, args := "SELECT "+runCols+" FROM memory_dream_runs WHERE environment_id = ?", []any{env}
	if scope != "" {
		if err := memory.ValidateScope(scope); err != nil {
			return nil, err
		}
		q += " AND scope = ?"
		args = append(args, scope)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY started_at DESC, rowid DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DreamRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- running a dream -------------------------------------------------------------------

func (s *Service) lockScope(env, scope string) bool {
	s.dreamMu.Lock()
	defer s.dreamMu.Unlock()
	if s.dreaming == nil {
		s.dreaming = map[string]bool{}
	}
	if s.dreaming[env+"|"+scope] {
		return false
	}
	s.dreaming[env+"|"+scope] = true
	return true
}

func (s *Service) unlockScope(env, scope string) {
	s.dreamMu.Lock()
	defer s.dreamMu.Unlock()
	delete(s.dreaming, env+"|"+scope)
}

// checkScope accepts shared and the scopes of the environment's personas.
func (s *Service) checkScope(ctx context.Context, env, scope string) error {
	if err := memory.ValidateScope(scope); err != nil {
		return err
	}
	if id, ok := strings.CutPrefix(scope, "persona:"); ok {
		if _, err := s.Store.Persona(ctx, env, id); err != nil {
			return fmt.Errorf("%w: no persona %s", memory.ErrInvalid, id)
		}
	}
	return nil
}

// begin locks the scope and returns its run: the interrupted one if there is one, else a
// new one covering the facts created since the last finished run.
func (s *Service) begin(ctx context.Context, env, scope, trigger string) (DreamRun, error) {
	if err := s.checkScope(ctx, env, scope); err != nil {
		return DreamRun{}, err
	}
	if !s.lockScope(env, scope) {
		return DreamRun{}, ErrDreamRunning
	}
	r, err := s.openRun(ctx, env, scope, trigger)
	if err != nil {
		s.unlockScope(env, scope)
	}
	return r, err
}

func (s *Service) openRun(ctx context.Context, env, scope, trigger string) (DreamRun, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runCols+" FROM memory_dream_runs WHERE environment_id = ? AND scope = ? AND status = 'running'", env, scope))
	if err == nil {
		r.Resumes++
		_, err = s.db.ExecContext(ctx, "UPDATE memory_dream_runs SET resumes = ? WHERE id = ?", r.Resumes, r.ID)
		return r, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return r, err
	}
	var start, end int64
	if err := s.db.QueryRowContext(ctx, `SELECT IFNULL(MAX(window_end), 0) FROM memory_dream_runs WHERE environment_id = ? AND scope = ? AND status = 'done'`,
		env, scope).Scan(&start); err != nil {
		return r, err
	}
	// The window ends at the newest fact, by the clock that stamped it. Both ends are
	// included, so a fact written in the boundary second is seen by the next run too, which
	// does no harm: every step is idempotent and verdicts are cached.
	if err := s.db.QueryRowContext(ctx, `SELECT IFNULL(MAX(created_at), 0) FROM memory_facts WHERE environment_id = ? AND scope IN (?, 'shared')`,
		env, scope).Scan(&end); err != nil {
		return r, err
	}
	end = max(end, start)
	r = DreamRun{ID: store.NewID(), Scope: scope, Trigger: trigger, Status: DreamRunning, WindowStart: time.Unix(start, 0).UTC(),
		WindowEnd: time.Unix(end, 0).UTC(), StartedAt: s.now().Truncate(time.Second)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO memory_dream_runs (id, environment_id, scope, trigger, status, window_start, window_end, started_at)
		VALUES (?, ?, ?, ?, 'running', ?, ?, ?)`, r.ID, env, scope, trigger, start, end, r.StartedAt.Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return r, ErrDreamRunning // another process holds it
	}
	return r, err
}

func (s *Service) saveRun(ctx context.Context, r *DreamRun) error {
	stats, _ := json.Marshal(r.Stats)
	var finished any
	if r.FinishedAt != nil {
		finished = r.FinishedAt.Unix()
	}
	// A cancelled run still records its progress.
	_, err := s.db.ExecContext(context.WithoutCancel(ctx), "UPDATE memory_dream_runs SET status = ?, stats = ?, note = ?, finished_at = ? WHERE id = ?",
		r.Status, string(stats), truncate(r.Note, 2000), finished, r.ID)
	return err
}

// Dream consolidates a scope ("shared" or "persona:<id>") and returns the finished run.
// An interrupted run of the scope is resumed; everything it already did is skipped or
// repeated without effect. A cancelled ctx leaves the run to be resumed.
func (s *Service) Dream(ctx context.Context, env, scope, trigger string) (DreamRun, error) {
	r, err := s.begin(ctx, env, scope, trigger)
	if err != nil {
		return r, err
	}
	defer s.unlockScope(env, scope)
	return s.process(ctx, env, r)
}

// StartDream starts consolidating a scope in the background and returns the run.
func (s *Service) StartDream(ctx context.Context, env, scope string) (DreamRun, error) {
	r, err := s.begin(ctx, env, scope, TriggerManual)
	if err != nil {
		return r, err
	}
	bg := s.background()
	go func() {
		defer s.unlockScope(env, scope)
		if _, err := s.process(bg, env, r); err != nil && bg.Err() == nil {
			s.Log.Warn("memory dream failed", "env", env, "scope", scope, "err", err)
		}
	}()
	return r, nil
}

func (s *Service) background() context.Context {
	s.dreamMu.Lock()
	defer s.dreamMu.Unlock()
	if s.bg != nil {
		return s.bg
	}
	return context.Background()
}

// process runs (or resumes) r. The run row is updated after each step.
func (s *Service) process(ctx context.Context, env string, r DreamRun) (DreamRun, error) {
	finish := func(status, note string) (DreamRun, error) {
		r.Status = status
		if note != "" {
			r.Note = strings.TrimSpace(r.Note + " " + note)
		}
		t := s.now().Truncate(time.Second)
		r.FinishedAt = &t
		return r, s.saveRun(ctx, &r)
	}
	d := &dreamer{s: s, env: env, r: &r}
	err := d.run(ctx)
	switch {
	case ctx.Err() != nil:
		_ = s.saveRun(ctx, &r) // still running; the next Dream resumes it
		return r, ctx.Err()
	case errors.Is(err, errStop):
		return finish(DreamStopped, d.stopNote)
	case err != nil:
		if _, ferr := finish(DreamFailed, truncate(err.Error(), 300)); ferr != nil {
			return r, ferr
		}
		return r, err
	}
	return finish(DreamDone, "")
}

// errStop ends a run early but cleanly: over budget or at the pair limit.
var errStop = errors.New("stopped")

type dreamer struct {
	s        *Service
	env      string
	r        *DreamRun
	llm      LLM
	model    UtilityModel
	noJudge  string // why pairs are not judged
	stopNote string
	entities map[string]bool
}

func (d *dreamer) save(ctx context.Context) error { return d.s.saveRun(ctx, d.r) }

func (d *dreamer) run(ctx context.Context) error {
	s, scope := d.s, d.r.Scope
	d.entities = map[string]bool{}
	fresh, err := s.Memory.FactsCreatedBetween(ctx, d.env, scope, d.r.WindowStart, d.r.WindowEnd)
	if err != nil {
		return err
	}
	persona := scope != memory.SharedScope
	if persona {
		// New shared facts are compared with this persona's facts, which they may supersede.
		shared, err := s.Memory.FactsCreatedBetween(ctx, d.env, memory.SharedScope, d.r.WindowStart, d.r.WindowEnd)
		if err != nil {
			return err
		}
		fresh = append(fresh, shared...)
	}
	d.r.Stats.Facts = len(fresh)
	if err := d.save(ctx); err != nil {
		return err
	}

	// 1. Merge near-duplicates within the scope.
	for _, f := range fresh {
		if f.Scope != scope {
			continue
		}
		if err := d.dedupe(ctx, f); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// 2. Judge pairs of related facts.
	pairs, err := d.pairs(ctx, fresh)
	if err != nil {
		return err
	}
	d.r.Stats.Pairs = len(pairs)
	if err := d.save(ctx); err != nil {
		return err
	}
	if len(pairs) > 0 {
		d.setupJudge(ctx)
	}
	judged := 0
	for _, p := range pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if judged >= maxJudgedPairs {
			d.stopNote = fmt.Sprintf("judged %d pairs, the limit of one run; the next run continues.", maxJudgedPairs)
			return d.compile(ctx, errStop)
		}
		called, err := d.judgeAndApply(ctx, p)
		if err != nil {
			if errors.Is(err, errStop) {
				return d.compile(ctx, errStop)
			}
			return err
		}
		if called {
			judged++
		}
		if err := d.save(ctx); err != nil {
			return err
		}
	}
	if d.noJudge != "" && len(pairs) > 0 {
		d.r.Note = strings.TrimSpace(d.r.Note + " Pairs not judged: " + d.noJudge + ".")
	}

	// 3. Regenerate entity pages.
	return d.compile(ctx, nil)
}

// compile regenerates the pages of the entities the run touched, then returns then.
func (d *dreamer) compile(ctx context.Context, then error) error {
	entities := make([]string, 0, len(d.entities))
	for e := range d.entities {
		entities = append(entities, e)
	}
	slices.Sort(entities)
	for _, e := range entities {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := d.s.Memory.CompileEntityPage(ctx, d.env, d.r.Scope, e)
		if err != nil {
			return err
		}
		if res.Changed {
			d.r.Stats.Pages++
		}
		if res.Note != "" && !strings.Contains(d.r.Note, res.Note) {
			d.r.Note = strings.TrimSpace(d.r.Note + " Page for " + e + " left as it was: " + res.Note + ".")
		}
	}
	if err := d.save(ctx); err != nil {
		return err
	}
	return then
}

func (d *dreamer) touch(facts ...memory.Fact) {
	for _, f := range facts {
		if f.Scope != d.r.Scope {
			continue
		}
		for _, e := range f.EntityIDs {
			d.entities[e] = true
		}
	}
}

// dedupe merges f with near-duplicates of its scope, keeping the older fact.
func (d *dreamer) dedupe(ctx context.Context, f memory.Fact) error {
	f, err := d.s.Memory.Fact(ctx, d.env, f.ID)
	if errors.Is(err, memory.ErrNotFound) { // merged away already
		return nil
	}
	if err != nil {
		return err
	}
	d.touch(f)
	related, err := d.s.Memory.RelatedFacts(ctx, d.env, f, []string{f.Scope}, maxRelated)
	if err != nil {
		return err
	}
	for _, o := range related {
		if !nearDuplicate(f, o) {
			continue
		}
		keep, drop := older(o, f)
		ok, err := d.s.Memory.MergeFacts(ctx, d.env, keep.ID, drop.ID)
		if err != nil {
			return err
		}
		if ok {
			d.r.Stats.Merged++
			if drop.ID == f.ID {
				return nil
			}
		}
	}
	return nil
}

// nearDuplicate is a cheap test for facts that say the same: same words, or nearly the
// same words about the same attribute.
func nearDuplicate(a, b memory.Fact) bool {
	if a.Kind != b.Kind {
		return false
	}
	if normalize(a.Text) == normalize(b.Text) {
		return true
	}
	j := jaccard(a.Text, b.Text)
	if a.Attribute != "" && a.Attribute == b.Attribute && j >= 0.8 {
		return true
	}
	return j >= 0.9
}

// older orders two facts: older observation first, then older creation, then ID.
func older(a, b memory.Fact) (memory.Fact, memory.Fact) {
	switch {
	case !a.ObservedAt.Equal(b.ObservedAt):
		if a.ObservedAt.Before(b.ObservedAt) {
			return a, b
		}
		return b, a
	case !a.CreatedAt.Equal(b.CreatedAt):
		if a.CreatedAt.Before(b.CreatedAt) {
			return a, b
		}
		return b, a
	case a.ID < b.ID:
		return a, b
	}
	return b, a
}

// pairs are the related fact pairs of the new facts, older first, each once. Pairs that
// already have a conflict are left out.
func (d *dreamer) pairs(ctx context.Context, fresh []memory.Fact) ([][2]memory.Fact, error) {
	seen := map[string]bool{}
	var out [][2]memory.Fact
	for _, f := range fresh {
		f, err := d.s.Memory.Fact(ctx, d.env, f.ID)
		if errors.Is(err, memory.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if f.Status != memory.StatusActive && f.Status != memory.StatusDisputed {
			continue
		}
		scopes := []string{d.r.Scope}
		if f.Scope == d.r.Scope && f.Scope != memory.SharedScope {
			scopes = append(scopes, memory.SharedScope)
		}
		related, err := d.s.Memory.RelatedFacts(ctx, d.env, f, scopes, maxRelated)
		if err != nil {
			return nil, err
		}
		for _, o := range related {
			a, b := older(f, o)
			key := a.ID + "|" + b.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, err := d.s.Memory.ConflictBetween(ctx, d.env, a.ID, b.ID); err == nil {
				continue
			} else if !errors.Is(err, memory.ErrNotFound) {
				return nil, err
			}
			out = append(out, [2]memory.Fact{a, b})
		}
	}
	return out, nil
}

// setupJudge picks the utility model: the persona's provider, or for shared memory the
// first persona's provider that has one. Subscriptions have none (TODO S8).
func (d *dreamer) setupJudge(ctx context.Context) {
	s := d.s
	var personas []store.Persona
	if id, ok := strings.CutPrefix(d.r.Scope, "persona:"); ok {
		p, err := s.Store.Persona(ctx, d.env, id)
		if err != nil {
			d.noJudge = "the persona is gone"
			return
		}
		personas = []store.Persona{p}
	} else {
		var err error
		if personas, err = s.Store.Personas(ctx, d.env); err != nil {
			d.noJudge = "listing personas failed"
			return
		}
	}
	d.noJudge = "no persona with a utility model (subscriptions have none yet)"
	for _, p := range personas {
		if p.ProviderID == "" {
			continue
		}
		prov, err := s.Store.Provider(ctx, d.env, p.ProviderID)
		if err != nil {
			continue
		}
		if m, ok := UtilityFor(prov); !ok {
			d.noJudge = m.Note
			continue
		}
		key := ""
		if prov.SecretID != "" {
			if key, err = s.Keys.Value(ctx, d.env, prov.SecretID); err != nil {
				d.noJudge = "reading the provider key failed"
				continue
			}
		}
		utility := s.Utility
		if utility == nil {
			utility = DefaultUtility
		}
		llm, m, err := utility(prov, key)
		if err != nil {
			d.noJudge = err.Error()
			continue
		}
		d.llm, d.model, d.noJudge = llm, m, ""
		d.r.Stats.Model = m.Model
		return
	}
}

// --- the judge ---------------------------------------------------------------------------

var verdictSchema = map[string]any{
	"type": "object", "additionalProperties": false, "required": []string{"verdict", "reason"},
	"properties": map[string]any{
		"verdict": map[string]any{"type": "string", "enum": []string{VerdictNone, VerdictDuplicate, VerdictSupersedes, VerdictContradiction}},
		"reason":  map[string]any{"type": "string"},
	},
}

func judgePrompt(a, b memory.Fact) (system, user string) {
	schema, _ := json.Marshal(verdictSchema)
	system = `You compare two memories of a coding agent's team. A is the older one, B the newer.
Answer with one verdict:
- "no_conflict": both can be true together.
- "duplicate": they say the same thing.
- "supersedes": B is a newer value of the same thing and replaces A (A was true until B was observed).
- "contradiction": they cannot both be true and B does not simply replace A, or you cannot tell which is right.
reason is one or two short sentences for the user.
Answer with JSON only, no prose, matching this schema: ` + string(schema)
	side := func(name string, f memory.Fact) string {
		return fmt.Sprintf("%s (%s, tier %s, observed %s, entities %s, attribute %q):\n%s\n", name, f.Kind, f.Tier,
			f.ObservedAt.Format(time.DateOnly), strings.Join(f.EntityIDs, ", "), f.Attribute, f.Text)
	}
	return system, side("A", a) + "\n" + side("B", b)
}

type verdictOut struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// parseVerdict validates the judge's answer strictly; any violation rejects it.
func parseVerdict(text string, a, b memory.Fact) (verdictOut, error) {
	if m := fence.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(text))))
	dec.DisallowUnknownFields()
	var out struct {
		Verdict *string `json:"verdict"`
		Reason  *string `json:"reason"`
	}
	if err := dec.Decode(&out); err != nil {
		return verdictOut{}, fmt.Errorf("not the schema: %v", err)
	}
	if dec.More() {
		return verdictOut{}, errors.New("not the schema: trailing data")
	}
	if out.Verdict == nil || out.Reason == nil {
		return verdictOut{}, errors.New("not the schema: verdict and reason are required")
	}
	v := verdictOut{Verdict: *out.Verdict, Reason: strings.TrimSpace(*out.Reason)}
	switch v.Verdict {
	case VerdictNone, VerdictDuplicate, VerdictContradiction:
	case VerdictSupersedes:
		if !a.ObservedAt.Before(b.ObservedAt) {
			return verdictOut{}, errors.New("supersedes needs B to be observed after A")
		}
	default:
		return verdictOut{}, fmt.Errorf("unknown verdict %q", v.Verdict)
	}
	if v.Reason == "" || len([]rune(v.Reason)) > 1000 {
		return verdictOut{}, errors.New("reason must have 1 to 1000 characters")
	}
	if secretLike.MatchString(v.Reason) {
		return verdictOut{}, errors.New("reason contains something like a credential")
	}
	return v, nil
}

// pairHash identifies a pair by what the judge sees.
func pairHash(a, b memory.Fact) string {
	h := sha256.New()
	for _, f := range []memory.Fact{a, b} {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00", f.Scope, f.Kind, f.Tier, strings.Join(f.EntityIDs, ","), f.Attribute, f.Text, f.ObservedAt.Unix())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (d *dreamer) cached(ctx context.Context, hash string) (verdictOut, bool, error) {
	var v verdictOut
	err := d.s.db.QueryRowContext(ctx, `SELECT verdict, reason FROM memory_judgments
		WHERE environment_id = ? AND pair_hash = ? AND model = ? AND prompt_version = ?`, d.env, hash, d.model.Model, judgePromptVersion).Scan(&v.Verdict, &v.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// judgeAndApply judges one pair, from the cache when it can, and applies the verdict.
// called reports whether the utility model was called.
func (d *dreamer) judgeAndApply(ctx context.Context, p [2]memory.Fact) (called bool, err error) {
	s := d.s
	a, b := p[0], p[1]
	if d.llm == nil {
		return false, nil
	}
	hash := pairHash(a, b)
	v, hit, err := d.cached(ctx, hash)
	if err != nil {
		return false, err
	}
	if hit {
		d.r.Stats.Cached++
	} else {
		system, user := judgePrompt(a, b)
		estIn := (len(system) + len(user)) / 3
		reason, err := s.overBudget(ctx, d.env, d.model, estIn)
		if err != nil {
			return false, err
		}
		if reason != "" {
			s.countSkip(ctx, d.env)
			d.stopNote = reason + "; the next run continues."
			return false, errStop
		}
		resp, err := d.llm.Complete(ctx, Request{System: system, User: user, Schema: verdictSchema, MaxTokens: judgeMaxTokens})
		in, out := resp.InputTokens, resp.OutputTokens
		if in == 0 && out == 0 {
			in, out = estIn, len(resp.Text)/3
		}
		cost := d.model.cost(in, out)
		if uerr := s.spend(context.WithoutCancel(ctx), d.env, in, out, cost); uerr != nil {
			s.Log.Warn("recording judge usage failed", "err", uerr)
		}
		d.r.Stats.InputTokens += in
		d.r.Stats.OutputTokens += out
		d.r.Stats.CostMicros += cost
		if err != nil {
			if ctx.Err() != nil {
				return true, ctx.Err()
			}
			return true, fmt.Errorf("judge call failed: %s", truncate(err.Error(), 200))
		}
		d.r.Stats.Judged++
		if v, err = parseVerdict(resp.Text, a, b); err != nil {
			d.r.Stats.Rejected++
			s.Log.Info("memory judge answer rejected", "env", d.env, "err", err)
			return true, nil // nothing changes; not cached, so a later run asks again
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO memory_judgments (environment_id, pair_hash, model, prompt_version, verdict, reason, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, d.env, hash, d.model.Model, judgePromptVersion, v.Verdict, v.Reason, s.now().Unix()); err != nil {
			return true, err
		}
		called = true
	}
	return called, d.apply(ctx, a, b, v)
}

// apply carries out a verdict on A (older) and B (newer). Automatic changes never lower
// trust: B supersedes A only with at least A's tier, and a persona fact never replaces a
// shared one. Everything else goes to the user as a conflict.
func (d *dreamer) apply(ctx context.Context, a, b memory.Fact, v verdictOut) error {
	s := d.s
	switch v.Verdict {
	case VerdictNone:
		return nil
	case VerdictDuplicate:
		if a.Scope != b.Scope {
			return nil // a persona's copy of a shared fact is harmless
		}
		ok, err := s.Memory.MergeFacts(ctx, d.env, a.ID, b.ID)
		if ok {
			d.r.Stats.Merged++
			d.touch(a)
		}
		return err
	case VerdictSupersedes:
		if tierBoostOf(b.Tier) >= tierBoostOf(a.Tier) && (a.Scope == b.Scope || b.Scope == memory.SharedScope) {
			ok, err := s.Memory.Supersede(ctx, d.env, a.ID, b.ID, v.Reason)
			if ok {
				d.r.Stats.Superseded++
				d.touch(a, b)
			}
			return err
		}
		return d.conflict(ctx, a, b, "temporal_supersession", v.Reason)
	case VerdictContradiction:
		return d.conflict(ctx, a, b, "contradiction", v.Reason)
	}
	return nil
}

func tierBoostOf(tier string) int {
	return map[string]int{memory.TierUser: 4, memory.TierVerified: 3, memory.TierDocument: 2, memory.TierInferred: 1}[tier]
}

func (d *dreamer) conflict(ctx context.Context, a, b memory.Fact, verdict, reason string) error {
	c, created, err := d.s.Memory.OpenConflict(ctx, d.env, a.ID, b.ID, verdict, reason)
	if err != nil {
		return err
	}
	if created {
		d.r.Stats.Conflicts++
		d.touch(a, b)
	}
	if c.ApprovalID == "" { // also when a crash came between the two steps
		return d.s.requestConflictApproval(ctx, d.env, c)
	}
	return nil
}

// --- conflicts and the inbox -------------------------------------------------------------

// ConflictSide is one fact of a conflict as the inbox shows it.
type ConflictSide struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Scope string `json:"scope"`
	Tier  string `json:"tier"`
}

// ConflictPayload is the payload of a memory.conflict approval.
type ConflictPayload struct {
	ConflictID string       `json:"conflictId"`
	Verdict    string       `json:"verdict"`
	Reason     string       `json:"reason,omitempty"`
	FactA      ConflictSide `json:"factA"`
	FactB      ConflictSide `json:"factB"`
}

func (s *Service) requestConflictApproval(ctx context.Context, env string, c memory.Conflict) error {
	side := func(f memory.Fact) ConflictSide {
		return ConflictSide{ID: f.ID, Text: f.Text, Scope: f.Scope, Tier: f.Tier}
	}
	payload, _ := json.Marshal(ConflictPayload{ConflictID: c.ID, Verdict: c.Verdict, Reason: c.Reason, FactA: side(c.FactA), FactB: side(c.FactB)})
	subject := truncate(fmt.Sprintf("%q vs %q", truncate(c.FactA.Text, 120), truncate(c.FactB.Text, 120)), 270) + " (" + c.ID + ")"
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env, Kind: ConflictApprovalKind, Subject: subject, Payload: payload})
	if err != nil {
		return err
	}
	if err := s.Memory.SetConflictApproval(ctx, env, c.ID, a.ID); err != nil {
		return err
	}
	if s.Notify != nil {
		s.Notify(env)
	}
	return nil
}

// decideConflict settles a memory.conflict approval from the inbox. Only dismiss is an
// inbox action: it hides the request while the conflict stays open (and the shared fact
// keeps winning). Resolving goes through ResolveConflict, which needs the user's choice.
func (s *Service) decideConflict(ctx context.Context, a store.Approval, d integrations.Decision) error {
	if d.Action != integrations.Dismiss {
		return fmt.Errorf("%w: resolve the conflict with keep A, keep B, keep both or edit", integrations.ErrAction)
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, ""); err != nil {
		return err
	}
	if s.Notify != nil {
		s.Notify(a.EnvironmentID)
	}
	return nil
}

// ResolveConflict applies the user's resolution (see memory.Service.ResolveConflict) and
// closes the conflict's approval.
func (s *Service) ResolveConflict(ctx context.Context, env, id string, r memory.ConflictResolution) (memory.Conflict, error) {
	c, err := s.Memory.ResolveConflict(ctx, env, id, r)
	if err != nil {
		return c, err
	}
	if c.ApprovalID != "" {
		if err := s.Store.DecideApproval(ctx, env, c.ApprovalID, store.StatusApproved, ""); err == nil && s.Notify != nil {
			s.Notify(env)
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return c, err
		}
	}
	return c, nil
}

// ConflictDetail is a conflict with what the user needs to decide it.
type ConflictDetail struct {
	memory.Conflict
	SourcesA []memory.Source        `json:"sourcesA"`
	SourcesB []memory.Source        `json:"sourcesB"`
	Timeline []memory.TimelineEntry `json:"timeline" doc:"Timeline entries about either fact, oldest first"`
}

// ConflictDetail returns a conflict with the sources of both facts and their timeline.
func (s *Service) ConflictDetail(ctx context.Context, env, id string) (ConflictDetail, error) {
	c, err := s.Memory.Conflict(ctx, env, id)
	if err != nil {
		return ConflictDetail{}, err
	}
	d := ConflictDetail{Conflict: c}
	if d.SourcesA, err = s.Memory.FactSources(ctx, env, c.FactA.ID); err != nil {
		return d, err
	}
	if d.SourcesB, err = s.Memory.FactSources(ctx, env, c.FactB.ID); err != nil {
		return d, err
	}
	d.Timeline, err = s.Memory.FactTimeline(ctx, env, c.FactA.ID, c.FactB.ID)
	return d, err
}

// --- the schedule ------------------------------------------------------------------------

type dueDream struct{ env, scope, trigger string }

// dueDreams lists the scopes to consolidate now: interrupted runs, scopes with
// DreamAfterFacts new facts, and, after DreamHour local time, scopes with new facts that
// haven't dreamt since. Automatic runs of a scope are at least autoDreamGap apart.
func (s *Service) dueDreams(ctx context.Context) ([]dueDream, error) {
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	local := now.In(time.Local)
	nightly := time.Date(local.Year(), local.Month(), local.Day(), DreamHour, 0, 0, 0, time.Local)
	var out []dueDream
	for _, e := range envs {
		personas, err := s.Store.Personas(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		scopes := []string{memory.SharedScope}
		for _, p := range personas {
			scopes = append(scopes, memory.PersonaScope(p.ID))
		}
		for _, scope := range scopes {
			var running int
			var lastStart, lastEnd sql.NullInt64
			if err := s.db.QueryRowContext(ctx, `SELECT
				(SELECT COUNT(*) FROM memory_dream_runs WHERE environment_id = ?1 AND scope = ?2 AND status = 'running'),
				(SELECT MAX(started_at) FROM memory_dream_runs WHERE environment_id = ?1 AND scope = ?2),
				(SELECT MAX(window_end) FROM memory_dream_runs WHERE environment_id = ?1 AND scope = ?2 AND status = 'done')`,
				e.ID, scope).Scan(&running, &lastStart, &lastEnd); err != nil {
				return nil, err
			}
			if running > 0 {
				out = append(out, dueDream{e.ID, scope, ""})
				continue
			}
			if lastStart.Valid && now.Sub(time.Unix(lastStart.Int64, 0)) < autoDreamGap {
				continue
			}
			n, err := s.Memory.CountFactsSince(ctx, e.ID, scope, time.Unix(lastEnd.Int64, 0))
			if err != nil {
				return nil, err
			}
			switch {
			case n >= DreamAfterFacts:
				out = append(out, dueDream{e.ID, scope, TriggerFacts})
			case n > 0 && !local.Before(nightly) && (!lastStart.Valid || time.Unix(lastStart.Int64, 0).Before(nightly)):
				out = append(out, dueDream{e.ID, scope, TriggerNightly})
			}
		}
	}
	return out, nil
}

// DreamDue runs the dreams that are due, one after another.
func (s *Service) DreamDue(ctx context.Context) {
	due, err := s.dueDreams(ctx)
	if err != nil {
		s.Log.Warn("listing due memory dreams failed", "err", err)
		return
	}
	for _, d := range due {
		trigger := d.trigger
		if trigger == "" {
			trigger = TriggerManual // only used if the running row vanished meanwhile
		}
		if _, err := s.Dream(ctx, d.env, d.scope, trigger); err != nil && !errors.Is(err, ErrDreamRunning) && ctx.Err() == nil {
			s.Log.Warn("memory dream failed", "env", d.env, "scope", d.scope, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// RunDreams consolidates memory on schedule until ctx ends; on-demand dreams started
// with StartDream also stop with ctx.
func (s *Service) RunDreams(ctx context.Context) {
	s.dreamMu.Lock()
	s.bg = ctx
	s.dreamMu.Unlock()
	t := time.NewTicker(dreamTick)
	defer t.Stop()
	for {
		s.DreamDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
