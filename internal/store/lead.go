package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Lead's notify choices: when the day is written, a notification comes only if something waits
// for the user, always, or never.
const (
	LeadNotifyWaiting = "waiting"
	LeadNotifyAlways  = "always"
	LeadNotifyNever   = "never"
)

// LeadSettings are how an environment's Lead writes the day (docs/lead.md).
type LeadSettings struct {
	Name         string    `json:"name" minLength:"1" maxLength:"40" doc:"What Lead is called in the sidebar"`
	WriteAt      int       `json:"writeAt" minimum:"0" maximum:"1439" doc:"When the day is written, in minutes after local midnight"`
	Days         int       `json:"days" minimum:"0" maximum:"127" doc:"The days it is written on: bit 0 is Monday, bit 6 Sunday"`
	SkipQuiet    bool      `json:"skipQuiet" doc:"A day without turns gets no page"`
	OwnComputers bool      `json:"ownComputers" doc:"The sessions the user started on their own computers are part of the day"`
	ShowCosts    bool      `json:"showCosts" doc:"The day shows what each teammate and Lead spent"`
	Heading      bool      `json:"heading" doc:"The day gets a short heading in the model's words"`
	DailyLimit   int64     `json:"dailyLimit" minimum:"0" doc:"What Lead's model calls may cost a day, in USD × 10^6; models without a known price are capped by calls instead"`
	Notify       string    `json:"notify" enum:"waiting,always,never" doc:"When writing the day notifies: only when something waits for the user, always, or never"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// DefaultLeadSettings are an environment's settings before anyone changes them: the day at
// 18:00 on weekdays, the user's own computers left out, a $1 daily limit.
func DefaultLeadSettings() LeadSettings {
	return LeadSettings{Name: "Lead", WriteAt: 18 * 60, Days: 0b0011111, SkipQuiet: true, ShowCosts: true, Heading: true,
		DailyLimit: 1_000_000, Notify: LeadNotifyWaiting}
}

// LeadSettings are the environment's settings, the defaults when none were saved.
func (s *Store) LeadSettings(ctx context.Context, envID string) (LeadSettings, error) {
	v := DefaultLeadSettings()
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT name, write_at, days, skip_quiet, own_computers, show_costs, heading, daily_limit, notify, updated_at
		FROM lead_settings WHERE environment_id = ?`, envID).Scan(&v.Name, &v.WriteAt, &v.Days, &v.SkipQuiet, &v.OwnComputers,
		&v.ShowCosts, &v.Heading, &v.DailyLimit, &v.Notify, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if updated != 0 {
		v.UpdatedAt = time.Unix(updated, 0)
	}
	return v, err
}

// SetLeadSettings saves the environment's settings.
func (s *Store) SetLeadSettings(ctx context.Context, envID string, v LeadSettings) (LeadSettings, error) {
	t := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_settings (environment_id, name, write_at, days, skip_quiet, own_computers, show_costs,
		heading, daily_limit, notify, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id) DO UPDATE SET name = excluded.name, write_at = excluded.write_at, days = excluded.days,
			skip_quiet = excluded.skip_quiet, own_computers = excluded.own_computers, show_costs = excluded.show_costs,
			heading = excluded.heading, daily_limit = excluded.daily_limit, notify = excluded.notify, updated_at = excluded.updated_at`,
		envID, v.Name, v.WriteAt, v.Days, v.SkipQuiet, v.OwnComputers, v.ShowCosts, v.Heading, v.DailyLimit, v.Notify, t)
	v.UpdatedAt = time.Unix(t, 0)
	return v, err
}

// LeadDay is a day Lead wrote: the facts as Studio's records had them, and the model's
// sentences about them. Day is a local date, YYYY-MM-DD.
type LeadDay struct {
	EnvironmentID string
	Day           string
	WrittenAt     time.Time
	Trigger       string // schedule or now
	Model         string
	Cost          int64
	Priced        bool
	Facts         json.RawMessage
	Prose         json.RawMessage // empty when the day has no sentences
	Note          string          // why it has none
	Digest        string
}

// LeadDayHead is a written day as the list of days shows it.
type LeadDayHead struct {
	Day       string    `json:"day" doc:"The local date, YYYY-MM-DD"`
	WrittenAt time.Time `json:"writtenAt"`
	Heading   string    `json:"heading,omitempty"`
	Waiting   int       `json:"waiting" doc:"What waited for the user when the day was written"`
}

// PutLeadDay saves a written day, replacing the one written before on the same date.
func (s *Store) PutLeadDay(ctx context.Context, d LeadDay) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_days (environment_id, day, written_at, trigger, model, cost, priced, facts, prose, note, digest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id, day) DO UPDATE SET written_at = excluded.written_at, trigger = excluded.trigger, model = excluded.model,
			cost = excluded.cost, priced = excluded.priced, facts = excluded.facts, prose = excluded.prose, note = excluded.note,
			digest = excluded.digest`,
		d.EnvironmentID, d.Day, d.WrittenAt.Unix(), d.Trigger, d.Model, d.Cost, d.Priced, string(d.Facts), string(d.Prose), d.Note, d.Digest)
	return err
}

// LeadDay is the day written on a date; ErrNotFound when none was.
func (s *Store) LeadDay(ctx context.Context, envID, day string) (LeadDay, error) {
	d := LeadDay{EnvironmentID: envID, Day: day}
	var written int64
	var facts, prose string
	err := s.db.QueryRowContext(ctx, `SELECT written_at, trigger, model, cost, priced, facts, prose, note, digest FROM lead_days
		WHERE environment_id = ? AND day = ?`, envID, day).Scan(&written, &d.Trigger, &d.Model, &d.Cost, &d.Priced, &facts, &prose, &d.Note, &d.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.WrittenAt = time.Unix(written, 0)
	d.Facts = json.RawMessage(facts)
	if prose != "" {
		d.Prose = json.RawMessage(prose)
	}
	return d, err
}

// LeadDays lists the written days, newest first, at most limit (default 60).
func (s *Store) LeadDays(ctx context.Context, envID string, limit int) ([]LeadDayHead, error) {
	if limit <= 0 || limit > 400 {
		limit = 60
	}
	rows, err := s.db.QueryContext(ctx, `SELECT day, written_at,
			CASE WHEN json_valid(prose) THEN IFNULL(json_extract(prose, '$.heading'), '') ELSE '' END,
			CASE WHEN json_valid(facts) THEN IFNULL(json_array_length(facts, '$.waiting'), 0) ELSE 0 END
		FROM lead_days WHERE environment_id = ? ORDER BY day DESC LIMIT ?`, envID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeadDayHead{}
	for rows.Next() {
		var h LeadDayHead
		var written int64
		if err := rows.Scan(&h.Day, &written, &h.Heading, &h.Waiting); err != nil {
			return nil, err
		}
		h.WrittenAt = time.Unix(written, 0)
		out = append(out, h)
	}
	return out, rows.Err()
}

// LeadNote is Lead's short note of what a teammate did on a day; TeammateID is empty for the
// user's own computers.
type LeadNote struct {
	TeammateID string
	Note       string
	Through    time.Time // the newest summary or turn the note has taken in
	UpdatedAt  time.Time
}

// LeadNotes are the notes of a day.
func (s *Store) LeadNotes(ctx context.Context, envID, day string) ([]LeadNote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT teammate_id, note, through, updated_at FROM lead_notes
		WHERE environment_id = ? AND day = ? ORDER BY teammate_id`, envID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeadNote{}
	for rows.Next() {
		var n LeadNote
		var through, updated int64
		if err := rows.Scan(&n.TeammateID, &n.Note, &through, &updated); err != nil {
			return nil, err
		}
		n.Through, n.UpdatedAt = time.Unix(through, 0), time.Unix(updated, 0)
		out = append(out, n)
	}
	return out, rows.Err()
}

// PutLeadNote saves a teammate's note of a day.
func (s *Store) PutLeadNote(ctx context.Context, envID, day string, n LeadNote) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_notes (environment_id, day, teammate_id, note, through, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id, day, teammate_id) DO UPDATE SET note = excluded.note, through = excluded.through, updated_at = excluded.updated_at`,
		envID, day, n.TeammateID, n.Note, n.Through.Unix(), now())
	return err
}

// LeadUsage is what Lead's model calls cost on a day.
type LeadUsage struct {
	Calls        int   `json:"calls"`
	InputTokens  int   `json:"inputTokens"`
	OutputTokens int   `json:"outputTokens"`
	Cost         int64 `json:"cost" doc:"USD × 10^6, for models with a known price"`
	Skipped      int   `json:"skipped" doc:"Calls left out: over the limit, or no model to call"`
}

// LeadUsage is a day's usage; zero when Lead made no call.
func (s *Store) LeadUsage(ctx context.Context, envID, day string) (LeadUsage, error) {
	var u LeadUsage
	err := s.db.QueryRowContext(ctx, `SELECT calls, input_tokens, output_tokens, cost, skipped FROM lead_usage WHERE environment_id = ? AND day = ?`,
		envID, day).Scan(&u.Calls, &u.InputTokens, &u.OutputTokens, &u.Cost, &u.Skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return u, nil
	}
	return u, err
}

// AddLeadUsage counts a call; one with calls 0 counts a skipped call.
func (s *Store) AddLeadUsage(ctx context.Context, envID, day string, calls, in, out int, cost int64) error {
	skipped := 0
	if calls == 0 {
		skipped = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_usage (environment_id, day, calls, input_tokens, output_tokens, cost, skipped)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id, day) DO UPDATE SET calls = calls + excluded.calls, input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens, cost = cost + excluded.cost, skipped = skipped + excluded.skipped`,
		envID, day, calls, in, out, cost, skipped)
	return err
}

// GitPushesIn lists the pushes sandboxes made from since to until, oldest first.
func (s *Store) GitPushesIn(ctx context.Context, envID string, since, until time.Time) ([]GitPush, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pushCols+` FROM git_pushes
		WHERE environment_id = ? AND created_at >= ? AND created_at < ? ORDER BY created_at, id LIMIT 500`, envID, since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitPush{}
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TaskRunsIn lists the task runs logged from since to until, oldest first.
func (s *Store) TaskRunsIn(ctx context.Context, envID string, since, until time.Time) ([]TaskRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskRunCols+` FROM task_runs r
		WHERE r.environment_id = ? AND r.at >= ? AND r.at < ? ORDER BY r.at, r.rowid LIMIT 500`, envID, since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskRun{}
	for rows.Next() {
		r, err := scanTaskRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChatUsageBefore is the usage the chat's last turn to end before before reported; nil when no
// turn did. An agent reports its session's cost so far, so the difference between two of these
// is what the turns between them cost.
func (s *Store) ChatUsageBefore(ctx context.Context, chatID string, before time.Time) ([]byte, error) {
	var usage []byte
	err := s.db.QueryRowContext(ctx, `SELECT usage FROM chat_turns WHERE chat_id = ? AND usage IS NOT NULL AND ended_at IS NOT NULL AND ended_at < ?
		ORDER BY ended_at DESC, seq DESC LIMIT 1`, chatID, before.Unix()).Scan(&usage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return usage, err
}

// Ask Lead's states: an answer is being written, was written, or couldn't be.
const (
	LeadAskAnswering = "answering"
	LeadAskAnswered  = "answered"
	LeadAskFailed    = "failed"
)

// LeadAsk is a question the user asked Lead about a day, and its answer.
type LeadAsk struct {
	ID         string          `json:"id"`
	Day        string          `json:"day" doc:"The day asked about, YYYY-MM-DD"`
	Question   string          `json:"question"`
	Answer     string          `json:"answer" doc:"Lead's answer; refs such as [push:2] point into refs"`
	Refs       json.RawMessage `json:"-"`
	State      string          `json:"state" enum:"answering,answered,failed"`
	Note       string          `json:"note,omitempty" doc:"Why there is no answer"`
	Model      string          `json:"model,omitempty"`
	Cost       int64           `json:"cost" doc:"What the answer cost, USD × 10^6"`
	Priced     bool            `json:"priced"`
	AskedAt    time.Time       `json:"askedAt"`
	AnsweredAt *time.Time      `json:"answeredAt,omitempty"`
}

// AddLeadAsk saves a question; its answer comes with FinishLeadAsk.
func (s *Store) AddLeadAsk(ctx context.Context, envID string, a LeadAsk) (LeadAsk, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	a.State = LeadAskAnswering
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_asks (id, environment_id, day, question, state, asked_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, envID, a.Day, a.Question, a.State, a.AskedAt.Unix())
	return a, err
}

// FinishLeadAsk saves a question's answer, or why it has none.
func (s *Store) FinishLeadAsk(ctx context.Context, a LeadAsk) error {
	answered := int64(0)
	if a.AnsweredAt != nil {
		answered = a.AnsweredAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE lead_asks SET answer = ?, refs = ?, state = ?, note = ?, model = ?, cost = ?, priced = ?, answered_at = ?
		WHERE id = ?`, a.Answer, string(a.Refs), a.State, a.Note, a.Model, a.Cost, a.Priced, answered, a.ID)
	return err
}

// LeadAsks are the questions asked about a day, oldest first.
func (s *Store) LeadAsks(ctx context.Context, envID, day string) ([]LeadAsk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, day, question, answer, refs, state, note, model, cost, priced, asked_at, answered_at
		FROM lead_asks WHERE environment_id = ? AND day = ? ORDER BY asked_at, rowid`, envID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeadAsk{}
	for rows.Next() {
		var a LeadAsk
		var refs string
		var asked, answered int64
		if err := rows.Scan(&a.ID, &a.Day, &a.Question, &a.Answer, &refs, &a.State, &a.Note, &a.Model, &a.Cost, &a.Priced,
			&asked, &answered); err != nil {
			return nil, err
		}
		if refs != "" {
			a.Refs = json.RawMessage(refs)
		}
		a.AskedAt = time.Unix(asked, 0)
		if answered != 0 {
			t := time.Unix(answered, 0)
			a.AnsweredAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FailLeadAsksAnswering gives up on the answers a Studio that stopped was writing.
func (s *Store) FailLeadAsksAnswering(ctx context.Context, note string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE lead_asks SET state = ?, note = ?, answered_at = ? WHERE state = ?`,
		LeadAskFailed, note, now(), LeadAskAnswering)
	return err
}

// LeadSeen is when the user last looked at the environment's day; zero when never.
func (s *Store) LeadSeen(ctx context.Context, envID string) (time.Time, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT seen_at FROM lead_seen WHERE environment_id = ?`, envID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(at, 0), nil
}

// SetLeadSeen records that the user looked at the day at t.
func (s *Store) SetLeadSeen(ctx context.Context, envID string, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_seen (environment_id, seen_at) VALUES (?, ?)
		ON CONFLICT (environment_id) DO UPDATE SET seen_at = excluded.seen_at`, envID, t.Unix())
	return err
}

// Lead plan states.
const (
	LeadPlanDrafting  = "drafting"  // Lead is writing the plan or its reply
	LeadPlanAsking    = "asking"    // Lead asked the user a question first
	LeadPlanProposed  = "proposed"  // the plan waits for the user's approval
	LeadPlanRunning   = "running"   // approved: its steps are being carried out
	LeadPlanDone      = "done"      // carried out, and Lead reported back
	LeadPlanCancelled = "cancelled" // the user cancelled or denied it
	LeadPlanFailed    = "failed"    // Lead couldn't draft or carry it out
)

// LeadPlan is a plan of Lead's: messages to teammates it sends once the user approves them.
type LeadPlan struct {
	ID            string
	EnvironmentID string
	Title         string
	State         string
	Steps         json.RawMessage // lead.PlanStep, each with its state
	ApprovalID    string
	ApprovedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// LeadPlanItem is one entry of a plan's thread: the user's words, Lead's, a plan it proposed, a
// question it asked, its report, or a note from Studio.
type LeadPlanItem struct {
	ID     string
	PlanID string
	Kind   string
	Text   string
	Data   json.RawMessage
	Model  string
	Cost   int64
	Priced bool
	At     time.Time
}

const leadPlanCols = `id, environment_id, title, state, steps, approval_id, approved_at, created_at, updated_at`

func scanLeadPlan(row interface{ Scan(...any) error }) (LeadPlan, error) {
	var p LeadPlan
	var steps string
	var approved, created, updated int64
	if err := row.Scan(&p.ID, &p.EnvironmentID, &p.Title, &p.State, &steps, &p.ApprovalID, &approved, &created, &updated); err != nil {
		return p, err
	}
	p.Steps = json.RawMessage(steps)
	if approved != 0 {
		t := time.Unix(approved, 0)
		p.ApprovedAt = &t
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return p, nil
}

// AddLeadPlan saves a new plan, drafting.
func (s *Store) AddLeadPlan(ctx context.Context, p LeadPlan) (LeadPlan, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.State == "" {
		p.State = LeadPlanDrafting
	}
	if len(p.Steps) == 0 {
		p.Steps = json.RawMessage("[]")
	}
	p.UpdatedAt = p.CreatedAt
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_plans (id, environment_id, title, state, steps, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.ID, p.EnvironmentID, p.Title, p.State, string(p.Steps), p.CreatedAt.Unix(), p.UpdatedAt.Unix())
	return p, err
}

// SaveLeadPlan writes a plan's title, state, steps and approval.
func (s *Store) SaveLeadPlan(ctx context.Context, p LeadPlan) error {
	approved := int64(0)
	if p.ApprovedAt != nil {
		approved = p.ApprovedAt.Unix()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE lead_plans SET title = ?, state = ?, steps = ?, approval_id = ?, approved_at = ?, updated_at = ?
		WHERE id = ? AND environment_id = ?`, p.Title, p.State, string(p.Steps), p.ApprovalID, approved, p.UpdatedAt.Unix(), p.ID, p.EnvironmentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetLeadPlan returns one of an environment's plans.
func (s *Store) GetLeadPlan(ctx context.Context, envID, id string) (LeadPlan, error) {
	p, err := scanLeadPlan(s.db.QueryRowContext(ctx, `SELECT `+leadPlanCols+` FROM lead_plans WHERE id = ? AND environment_id = ?`, id, envID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// LeadPlans are an environment's plans, the last changed first, at most limit.
func (s *Store) LeadPlans(ctx context.Context, envID string, limit int) ([]LeadPlan, error) {
	return s.leadPlans(ctx, `SELECT `+leadPlanCols+` FROM lead_plans WHERE environment_id = ? ORDER BY updated_at DESC, rowid DESC LIMIT ?`, envID, limit)
}

// LeadPlansIn are every environment's plans in one of the states, oldest first.
func (s *Store) LeadPlansIn(ctx context.Context, states ...string) ([]LeadPlan, error) {
	if len(states) == 0 {
		return nil, nil
	}
	args := make([]any, len(states))
	for i, st := range states {
		args[i] = st
	}
	return s.leadPlans(ctx, `SELECT `+leadPlanCols+` FROM lead_plans WHERE state IN (?`+strings.Repeat(", ?", len(states)-1)+`) ORDER BY created_at, rowid`, args...)
}

func (s *Store) leadPlans(ctx context.Context, q string, args ...any) ([]LeadPlan, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeadPlan{}
	for rows.Next() {
		p, err := scanLeadPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddLeadPlanItem adds an entry to a plan's thread.
func (s *Store) AddLeadPlanItem(ctx context.Context, it LeadPlanItem) (LeadPlanItem, error) {
	if it.ID == "" {
		it.ID = NewID()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO lead_plan_items (id, plan_id, kind, text, data, model, cost, priced, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.ID, it.PlanID, it.Kind, it.Text, string(it.Data), it.Model, it.Cost, it.Priced, it.At.Unix())
	return it, err
}

// LeadPlanItems are a plan's thread, oldest first.
func (s *Store) LeadPlanItems(ctx context.Context, planID string) ([]LeadPlanItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, plan_id, kind, text, data, model, cost, priced, at FROM lead_plan_items
		WHERE plan_id = ? ORDER BY at, rowid`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeadPlanItem{}
	for rows.Next() {
		var it LeadPlanItem
		var data string
		var at int64
		if err := rows.Scan(&it.ID, &it.PlanID, &it.Kind, &it.Text, &data, &it.Model, &it.Cost, &it.Priced, &at); err != nil {
			return nil, err
		}
		if data != "" {
			it.Data = json.RawMessage(data)
		}
		it.At = time.Unix(at, 0)
		out = append(out, it)
	}
	return out, rows.Err()
}
