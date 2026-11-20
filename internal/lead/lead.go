// Package lead is Lead, the teammate without a computer that writes up each environment's day
// (docs/lead.md). It reads what Studio already records, keeps a short note of each teammate's
// day, and at a set time writes the day: what waits for the user, what each teammate finished,
// has open or is blocked on, what shipped and what it cost. Studio fills in every number,
// state and link from its records; the environment's utility model only writes the sentences.
// Lead is read-only: it has no tool that acts on a teammate, a computer or an approval.
package lead

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	// foldEvery is how often a teammate's note takes in what is new, at most.
	foldEvery = 15 * time.Minute
	// maxUnpricedCalls caps a day's calls to a model without a known price, and
	// maxUnpricedFoldCalls the notes' share of them; the rest are kept for writing the day.
	maxUnpricedCalls     = 60
	maxUnpricedFoldCalls = 40
	// foldSharePercent is the notes' share of a priced model's daily limit.
	foldSharePercent = 60
	// turnsPerFold is how many of a teammate's turns a fold reads, the newest.
	turnsPerFold  = 100
	dayOutTokens  = 1500
	foldOutTokens = 200
	writeTimeout  = 5 * time.Minute
	foldTimeout   = 2 * time.Minute
	// A scheduled write whose call failed for a reason that may pass, such as a busy provider,
	// is tried again every retryEvery, maxRetries times.
	retryEvery = 15 * time.Minute
	maxRetries = 3
)

// ErrWriting means the day is being written already.
var ErrWriting = errors.New("the day is being written already")

// Service writes the environments' days.
type Service struct {
	Store *store.Store
	// AgentMem gives the session summaries memory writes; nil leaves them out.
	AgentMem *agentmem.Service
	Keys     agentmem.Keys
	// Utility returns the client of a provider's utility model, as memory calls it.
	Utility func(p store.Provider, key string) (agentmem.LLM, agentmem.UtilityModel, error)
	Bus     *events.Bus
	Log     *slog.Logger
	// LowDisks are a sandbox's nearly full disks; nil leaves disks out.
	LowDisks func(sandboxID string) []agentproto.Disk
	// Now is the local time; time.Now when nil.
	Now func() time.Time

	mu      sync.Mutex
	writing map[string]bool       // environment -> a write runs
	quiet   map[string]bool       // environment/day@writeAt -> found without turns at its time
	retry   map[string]retryState // environment -> a scheduled write to try again
	folded  map[string]time.Time  // environment/teammate -> the last fold
	// foldMu keeps two folds from reading the same note; callMu keeps two calls from passing
	// the daily limit on the same usage.
	foldMu, callMu sync.Mutex
}

// retryState is a day whose scheduled write failed: how often, and when it is tried next.
type retryState struct {
	day   string
	tries int
	next  time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func (s *Service) publish(env, day string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicLead, EnvironmentID: env, ID: day})
	}
}

// Run writes each environment's day at its time, and keeps the notes, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick folds what is new into the notes and writes the days that are due.
func (s *Service) Tick(ctx context.Context) {
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		s.log().Warn("lead: listing environments failed", "err", err)
		return
	}
	for _, e := range envs {
		set, err := s.Store.LeadSettings(ctx, e.ID)
		if err != nil {
			s.log().Warn("lead: reading settings failed", "env", e.ID, "err", err)
			continue
		}
		now := s.now()
		if notesNow(set, now) {
			s.fold(ctx, e.ID, set, now, false)
		}
		if day, ok := s.due(ctx, e.ID, set, now); ok {
			if err := s.write(ctx, e.ID, "schedule", day); err != nil && !errors.Is(err, ErrWriting) {
				s.log().Warn("lead: writing the day failed", "env", e.ID, "err", err)
			}
		}
	}
}

// weekdayBit is the bit of t's weekday in LeadSettings.Days: Monday is bit 0.
func weekdayBit(t time.Time) int { return 1 << ((int(t.Weekday()) + 6) % 7) }

// notesNow says whether the notes are kept in the background now: on a day the day is written
// on, before its time. Writing the day brings them up to date in any case.
func notesNow(set store.LeadSettings, now time.Time) bool {
	return set.Days&weekdayBit(now) != 0 && now.Hour()*60+now.Minute() < set.WriteAt
}

// due says which day should be written now, if any: today once its time has come, or the day
// before, when Studio wasn't running from its time to midnight.
func (s *Service) due(ctx context.Context, env string, set store.LeadSettings, now time.Time) (time.Time, bool) {
	if s.dayDue(ctx, env, set, now, now) {
		return now, true
	}
	if y := now.AddDate(0, 0, -1); s.dayDue(ctx, env, set, y, now) {
		return y, true
	}
	return time.Time{}, false
}

// dayDue says whether day should be written now: it is a day it is written on, its time has
// come, and it wasn't written since, or its scheduled write failed for a reason that may pass
// and is to be tried again. A day without turns is looked at once.
func (s *Service) dayDue(ctx context.Context, env string, set store.LeadSettings, day, now time.Time) bool {
	at := time.Date(day.Year(), day.Month(), day.Day(), set.WriteAt/60, set.WriteAt%60, 0, 0, day.Location())
	if set.Days&weekdayBit(day) == 0 || now.Before(at) {
		return false
	}
	key := day.Format(time.DateOnly)
	s.mu.Lock()
	quiet := set.SkipQuiet && s.quiet[quietKey(env, key, set)]
	r := s.retry[env]
	s.mu.Unlock()
	if quiet {
		return false
	}
	d, err := s.Store.LeadDay(ctx, env, key)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return true
	case err != nil:
		return false
	case d.WrittenAt.Before(at):
		return true
	}
	return r.day == key && r.tries <= maxRetries && !now.Before(r.next)
}

func quietKey(env, day string, set store.LeadSettings) string {
	return fmt.Sprintf("%s/%s@%d", env, day, set.WriteAt)
}

// retryLater notes that day's scheduled write is to be tried again, and returns when.
func (s *Service) retryLater(env, day string, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.retry[env]
	if r.day != day {
		r = retryState{day: day}
	}
	r.tries++
	r.next = now.Add(retryEvery)
	if s.retry == nil {
		s.retry = map[string]retryState{}
	}
	s.retry[env] = r
	return r.next, r.tries <= maxRetries
}

// retryable says whether a failed call may pass when tried again: not without a model, and
// not over the limit.
func retryable(err error) bool {
	var limit limitError
	return !errors.Is(err, errNoModel) && !errors.As(err, &limit)
}

// Summarize writes today's day now, in the background. It returns ErrWriting when a write runs.
func (s *Service) Summarize(env string) error {
	if !s.begin(env) {
		return ErrWriting
	}
	s.publish(env, s.now().Format(time.DateOnly))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		defer s.end(env)
		if err := s.writeDay(ctx, env, "now", s.now()); err != nil {
			s.log().Warn("lead: writing the day failed", "env", env, "err", err)
		}
	}()
	return nil
}

// Writing says whether the environment's day is being written.
func (s *Service) Writing(env string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writing[env]
}

func (s *Service) begin(env string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writing[env] {
		return false
	}
	if s.writing == nil {
		s.writing = map[string]bool{}
	}
	s.writing[env] = true
	return true
}

func (s *Service) end(env string) {
	s.mu.Lock()
	delete(s.writing, env)
	s.mu.Unlock()
	s.publish(env, s.now().Format(time.DateOnly))
}

// write writes day unless a write runs.
func (s *Service) write(ctx context.Context, env, trigger string, day time.Time) error {
	if !s.begin(env) {
		return ErrWriting
	}
	s.publish(env, day.Format(time.DateOnly))
	defer s.end(env)
	return s.writeDay(ctx, env, trigger, day)
}

// writeDay folds what is new into the notes, queries the facts, has the model write about them
// and saves the day. A day whose facts and notes are the ones the saved sentences were written
// from keeps them without a call.
func (s *Service) writeDay(ctx context.Context, env, trigger string, day time.Time) error {
	set, err := s.Store.LeadSettings(ctx, env)
	if err != nil {
		return err
	}
	if now := s.now(); day.Format(time.DateOnly) == now.Format(time.DateOnly) {
		// The notes take in what is new, within a part of the write's time.
		fctx, cancel := context.WithTimeout(ctx, foldTimeout)
		s.fold(fctx, env, set, day, true)
		cancel()
	}
	now := s.now()
	f, err := s.facts(ctx, env, day, set)
	if err != nil {
		return err
	}
	if trigger == "schedule" && set.SkipQuiet && f.Turns == 0 {
		s.mu.Lock()
		if s.quiet == nil {
			s.quiet = map[string]bool{}
		}
		s.quiet[quietKey(env, f.Day, set)] = true
		s.mu.Unlock()
		return nil
	}
	input := dayInput(f)
	sum := sha256.Sum256([]byte(dayPrompt + input))
	d := store.LeadDay{EnvironmentID: env, Day: f.Day, WrittenAt: now, Trigger: trigger, Digest: hex.EncodeToString(sum[:])}
	if d.Facts, err = json.Marshal(f); err != nil {
		return err
	}
	prev, err := s.Store.LeadDay(ctx, env, f.Day)
	switch {
	case err == nil && prev.Digest == d.Digest && prev.Prose != nil:
		d.Prose, d.Model, d.Cost, d.Priced = prev.Prose, prev.Model, prev.Cost, prev.Priced
	case len(f.Teammates) == 0:
		d.Note = "Nobody worked on this day, so there is nothing to write about."
	default:
		resp, m, err := s.call(ctx, env, set, agentmem.Request{System: dayPrompt, User: input, MaxTokens: dayOutTokens}, false)
		if err != nil {
			d.Note = err.Error()
			if trigger == "schedule" && retryable(err) {
				if next, ok := s.retryLater(env, f.Day, now); ok {
					d.Note = fmt.Sprintf("%s. %s tries again at %s.", strings.TrimSuffix(d.Note, "."), set.Name, next.Format("15:04"))
				}
			}
			break
		}
		d.Model, d.Priced, d.Cost = m.Model, m.Priced, m.Cost(resp.InputTokens, resp.OutputTokens)
		p, err := parseProse(resp.Text, f)
		if err != nil {
			d.Note = err.Error() + ", so this day has its facts without sentences."
			break
		}
		if d.Prose, err = json.Marshal(p); err != nil {
			return err
		}
	}
	// Saved even when the write ran out of time: the day keeps its facts.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Store.PutLeadDay(sctx, d); err != nil {
		return err
	}
	if d.Prose != nil {
		s.mu.Lock()
		if s.retry[env].day == f.Day {
			delete(s.retry, env)
		}
		s.mu.Unlock()
	}
	return nil
}

// facts are the day's facts with Lead's notes of each teammate.
func (s *Service) facts(ctx context.Context, env string, day time.Time, set store.LeadSettings) (LeadFacts, error) {
	f, err := s.gather(ctx, env, day, set)
	if err != nil {
		return f, err
	}
	notes, err := s.Store.LeadNotes(ctx, env, f.Day)
	if err != nil {
		return f, err
	}
	for i := range f.Teammates {
		for _, n := range notes {
			if n.TeammateID == f.Teammates[i].ID && n.TeammateID != "" {
				f.Teammates[i].Note = n.Note
			}
		}
	}
	return f, nil
}

// errNoModel says why Lead has no model to call.
var errNoModel = errors.New("no teammate's provider has a utility model Lead can call, so the day has its facts without sentences")

// limitError is a call Lead's daily limit stopped.
type limitError struct{ reason string }

func (e limitError) Error() string { return e.reason }

// call makes a model call within Lead's daily limit and counts it. A fold's calls have a share
// of the limit, so that the day can still be written.
func (s *Service) call(ctx context.Context, env string, set store.LeadSettings, r agentmem.Request, fold bool) (agentmem.Response, agentmem.UtilityModel, error) {
	var m agentmem.UtilityModel
	day := s.now().Format(time.DateOnly)
	p, ok, err := agentmem.EnvironmentUtility(ctx, s.Store, env)
	if err != nil {
		return agentmem.Response{}, m, err
	}
	if !ok || s.Utility == nil {
		return agentmem.Response{}, m, errNoModel
	}
	key := ""
	if p.SecretID != "" && s.Keys != nil {
		if key, err = s.Keys.Value(ctx, env, p.SecretID); err != nil {
			return agentmem.Response{}, m, errors.New("reading the provider's key failed") // never the key or its error
		}
	}
	llm, m, err := s.Utility(p, key)
	if err != nil {
		return agentmem.Response{}, m, fmt.Errorf("Lead's model can't be called: %w", err)
	}
	s.callMu.Lock()
	defer s.callMu.Unlock()
	u, err := s.Store.LeadUsage(ctx, env, day)
	if err != nil {
		return agentmem.Response{}, m, err
	}
	estIn := (len(r.System) + len(r.User)) / 3
	if reason := overLimit(u, set, m, estIn, r.MaxTokens, fold); reason != "" {
		_ = s.Store.AddLeadUsage(ctx, env, day, 0, 0, 0, 0)
		return agentmem.Response{}, m, limitError{reason}
	}
	resp, err := llm.Complete(ctx, r)
	if resp.InputTokens > 0 || resp.OutputTokens > 0 || err == nil {
		if uerr := s.Store.AddLeadUsage(ctx, env, day, 1, resp.InputTokens, resp.OutputTokens, m.Cost(resp.InputTokens, resp.OutputTokens)); uerr != nil {
			s.log().Warn("lead: counting a call failed", "err", uerr)
		}
	}
	if err != nil {
		if key != "" {
			err = errors.New(strings.ReplaceAll(err.Error(), key, "[key]"))
		}
		return resp, m, fmt.Errorf("Lead's model call failed: %w", err)
	}
	return resp, m, nil
}

// overLimit says why a call would go over the day's limit, or a fold's call over the notes'
// share of it.
func overLimit(u store.LeadUsage, set store.LeadSettings, m agentmem.UtilityModel, in, out int, fold bool) string {
	if m.Priced {
		limit := set.DailyLimit
		if fold {
			limit = limit * foldSharePercent / 100
		}
		if u.Cost+m.Cost(in, out) > limit {
			if fold {
				return "the notes used their share of the daily limit"
			}
			return fmt.Sprintf("Lead's daily limit of $%.2f is used up, so the day has its facts without sentences", float64(set.DailyLimit)/1e6)
		}
		return ""
	}
	if fold && u.Calls >= maxUnpricedFoldCalls {
		return "the notes made their share of the day's calls"
	}
	if u.Calls >= maxUnpricedCalls {
		return fmt.Sprintf("Lead made its %d calls for the day, so the day has its facts without sentences", maxUnpricedCalls)
	}
	return ""
}

// fold brings each teammate's note of today up to date with the sessions memory summed up and
// the turns that ended since. Without force a teammate's note is looked at every foldEvery at
// most. When the model can't be called, a note that has nothing yet takes memory's latest
// summary as it is.
func (s *Service) fold(ctx context.Context, env string, set store.LeadSettings, now time.Time, force bool) {
	s.foldMu.Lock()
	defer s.foldMu.Unlock()
	from, _ := span(now, now)
	day := from.Format(time.DateOnly)
	teammates, err := s.Store.Teammates(ctx, env)
	if err != nil {
		s.log().Warn("lead: listing teammates failed", "err", err)
		return
	}
	var notes map[string]store.LeadNote
	for _, p := range teammates {
		if ctx.Err() != nil {
			return
		}
		key := env + "/" + p.ID
		s.mu.Lock()
		last := s.folded[key]
		if !force && now.Sub(last) < foldEvery {
			s.mu.Unlock()
			continue
		}
		if s.folded == nil {
			s.folded = map[string]time.Time{}
		}
		s.folded[key] = now
		s.mu.Unlock()
		if notes == nil {
			list, err := s.Store.LeadNotes(ctx, env, day)
			if err != nil {
				s.log().Warn("lead: reading notes failed", "err", err)
				return
			}
			notes = map[string]store.LeadNote{}
			for _, n := range list {
				notes[n.TeammateID] = n
			}
		}
		note := notes[p.ID]
		through := from
		if note.Through.After(through) {
			through = note.Through
		}
		newest := through
		turns, err := s.Store.TeammateTurns(ctx, env, p.ID, from, turnsPerFold)
		if err != nil {
			s.log().Warn("lead: reading turns failed", "err", err)
			continue
		}
		var fresh []store.TurnDigest
		for _, t := range turns {
			if t.EndedAt != nil && t.EndedAt.After(through) {
				fresh = append(fresh, t)
				if t.EndedAt.After(newest) {
					newest = *t.EndedAt
				}
			}
		}
		var summaries []string
		if s.AgentMem != nil {
			sessions, err := s.AgentMem.Sessions(ctx, env, p.ID, 50)
			if err != nil {
				s.log().Warn("lead: reading session summaries failed", "err", err)
			}
			for _, v := range sessions {
				if v.SummaryAt != nil && v.SummaryAt.After(through) && v.Summary != "" {
					summaries = append(summaries, v.Summary)
					if v.SummaryAt.After(newest) {
						newest = *v.SummaryAt
					}
				}
			}
		}
		if len(fresh) == 0 && len(summaries) == 0 {
			continue
		}
		var text string
		resp, _, err := s.call(ctx, env, set, agentmem.Request{System: foldPrompt,
			User: foldInput(note.Note, summaries, fresh, now.Location()), MaxTokens: foldOutTokens}, true)
		switch {
		case err == nil && strings.TrimSpace(resp.Text) != "":
			text = clip(redact(oneLine(resp.Text)), maxNote)
		case note.Note == "" && len(summaries) > 0:
			text = clip(redact(oneLine(summaries[0])), maxNote) // the newest session's
		default:
			continue // tried again at the next fold
		}
		if err := s.Store.PutLeadNote(ctx, env, day, store.LeadNote{TeammateID: p.ID, Note: text, Through: newest}); err != nil {
			s.log().Warn("lead: saving a note failed", "err", err)
		}
	}
}
