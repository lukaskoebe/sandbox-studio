package lead

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LeadView is Lead as its page and the sidebar show it.
type LeadView struct {
	Settings store.LeadSettings     `json:"settings"`
	Today    LeadToday              `json:"today"`
	Days     []store.LeadDayHead    `json:"days" doc:"The written days, newest first"`
	Writing  bool                   `json:"writing" doc:"Lead is writing today's day"`
	Usage    store.LeadUsage        `json:"usage" doc:"Today's model calls"`
	Model    *agentmem.UtilityModel `json:"model,omitempty" doc:"The model Lead calls: the utility model of the first teammate's provider that has one; absent when none has"`
	Zone     string                 `json:"zone" doc:"Studio's time zone, as its abbreviation, such as CET; writeAt and the days are in it"`
	Offset   int                    `json:"offset" doc:"Studio's time zone's offset from UTC now, in minutes"`
}

// LeadToday is the day so far, as Home's card shows it.
type LeadToday struct {
	Day         string          `json:"day"`
	Notes       []LeadTodayNote `json:"notes" doc:"Each working teammate's note, most turn time first"`
	Summary     string          `json:"summary,omitempty" doc:"The written day's summary, its refs written out as names, once today is written"`
	WrittenAt   *time.Time      `json:"writtenAt,omitempty"`
	Trigger     string          `json:"trigger,omitempty" enum:"schedule,now"`
	Waiting     int             `json:"waiting"`
	Pushes      int             `json:"pushes"`
	PRs         int             `json:"prs" doc:"Pushes that opened or updated a pull request"`
	Threads     int             `json:"threads"`
	Turns       int             `json:"turns"`
	TurnSeconds int64           `json:"turnSeconds"`
	Cost        int64           `json:"cost"`
	Costed      bool            `json:"costed"`
	Since       *LeadSince      `json:"since,omitempty" doc:"What happened since the user last looked at the day, when that was today"`
}

// LeadTodayNote is a teammate's note of today.
type LeadTodayNote struct {
	TeammateID string `json:"teammateId"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar,omitempty"`
	Note       string `json:"note"`
}

// LeadDayView is one day's page.
type LeadDayView struct {
	Day       string     `json:"day"`
	Written   bool       `json:"written" doc:"Lead wrote the day; today's facts so far when not"`
	WrittenAt *time.Time `json:"writtenAt,omitempty"`
	Trigger   string     `json:"trigger,omitempty" enum:"schedule,now"`
	Model     string     `json:"model,omitempty" doc:"The model that wrote the sentences"`
	Cost      int64      `json:"cost" doc:"What the call that wrote the sentences cost, USD × 10^6"`
	Priced    bool       `json:"priced"`
	LeadCost  int64      `json:"leadCost" doc:"What all of Lead's calls of the day cost, USD × 10^6"`
	Facts     LeadFacts  `json:"facts"`
	Prose     *LeadProse `json:"prose,omitempty"`
	Note      string     `json:"note,omitempty" doc:"Why the day has no sentences"`
	Markdown  string     `json:"markdown" doc:"The day as Markdown, for Copy as Markdown"`
	Prev      string     `json:"prev,omitempty" doc:"The written day before it"`
	Next      string     `json:"next,omitempty" doc:"The written day after it, or today"`
}

// View is the environment's Lead.
func (s *Service) View(ctx context.Context, env string) (LeadView, error) {
	set, err := s.Store.LeadSettings(ctx, env)
	if err != nil {
		return LeadView{}, err
	}
	now := s.now()
	v := LeadView{Settings: set, Writing: s.Writing(env)}
	zone, off := now.Zone()
	v.Zone, v.Offset = zone, off/60
	if v.Days, err = s.Store.LeadDays(ctx, env, 60); err != nil {
		return v, err
	}
	if !set.Heading {
		for i := range v.Days {
			v.Days[i].Heading = ""
		}
	}
	if v.Usage, err = s.Store.LeadUsage(ctx, env, now.Format(time.DateOnly)); err != nil {
		return v, err
	}
	if p, ok, err := agentmem.EnvironmentUtility(ctx, s.Store, env); err != nil {
		return v, err
	} else if ok {
		m, _ := agentmem.UtilityFor(p)
		if m.Provider == "" {
			m.Provider = p.Name
		}
		v.Model = &m
	}
	f, err := s.facts(ctx, env, now, set)
	if err != nil {
		return v, err
	}
	v.Today = LeadToday{Day: f.Day, Notes: []LeadTodayNote{}, Waiting: len(f.Waiting), Pushes: len(f.Pushes), Threads: f.Threads,
		Turns: f.Turns, TurnSeconds: f.TurnSeconds, Cost: f.Cost, Costed: f.Costed}
	for _, p := range f.Pushes {
		if p.PRURL != "" {
			v.Today.PRs++
		}
	}
	for _, t := range f.Teammates {
		if t.Note != "" {
			v.Today.Notes = append(v.Today.Notes, LeadTodayNote{TeammateID: t.ID, Name: t.Name, Avatar: t.Avatar, Note: t.Note})
		}
	}
	if d, err := s.Store.LeadDay(ctx, env, f.Day); err == nil {
		v.Today.WrittenAt, v.Today.Trigger = &d.WrittenAt, d.Trigger
		var p LeadProse
		var written LeadFacts
		if d.Prose != nil && json.Unmarshal(d.Prose, &p) == nil && json.Unmarshal(d.Facts, &written) == nil {
			// The card has no facts to link refs by, so they are names here.
			v.Today.Summary = named(p.Summary, refNames(written))
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	if seen, err := s.Store.LeadSeen(ctx, env); err != nil {
		return v, err
	} else if seen.Before(now) {
		v.Today.Since = since(f, seen)
	}
	if !set.ShowCosts {
		v.Today.Cost, v.Today.Costed = 0, false
	}
	return v, nil
}

// Day is a day's page: the day as written, or for today before it is, the facts so far.
func (s *Service) Day(ctx context.Context, env, day string) (LeadDayView, error) {
	at, err := time.ParseInLocation(time.DateOnly, day, s.now().Location())
	if err != nil {
		return LeadDayView{}, fmt.Errorf("%w: a day is YYYY-MM-DD", store.ErrNotFound)
	}
	set, err := s.Store.LeadSettings(ctx, env)
	if err != nil {
		return LeadDayView{}, err
	}
	today := s.now().Format(time.DateOnly)
	v := LeadDayView{Day: day}
	d, err := s.Store.LeadDay(ctx, env, day)
	switch {
	case err == nil:
		v.Written, v.WrittenAt, v.Trigger, v.Model, v.Cost, v.Priced, v.Note = true, &d.WrittenAt, d.Trigger, d.Model, d.Cost, d.Priced, d.Note
		if err := json.Unmarshal(d.Facts, &v.Facts); err != nil {
			return v, fmt.Errorf("reading the day's facts: %w", err)
		}
		if d.Prose != nil {
			var p LeadProse
			if err := json.Unmarshal(d.Prose, &p); err == nil {
				v.Prose = &p
			}
		}
	case errors.Is(err, store.ErrNotFound) && day == today:
		if v.Facts, err = s.facts(ctx, env, at, set); err != nil {
			return v, err
		}
		now := s.now()
		switch {
		case set.Days&weekdayBit(at) == 0:
			v.Note = fmt.Sprintf("%s doesn't write the day on %ss. These are today's facts so far; Summarize now writes them.", set.Name, at.Weekday())
		case set.SkipQuiet && v.Facts.Turns == 0 && now.Hour()*60+now.Minute() >= set.WriteAt:
			v.Note = fmt.Sprintf("Nobody worked today, so %s didn't write the day; Summarize now writes it anyway.", set.Name)
		default:
			v.Note = fmt.Sprintf("%s writes the day at %02d:%02d. These are today's facts so far.", set.Name, set.WriteAt/60, set.WriteAt%60)
		}
	case err != nil:
		return v, err
	}
	if v.Prose != nil && !set.Heading {
		v.Prose.Heading = ""
	}
	if u, err := s.Store.LeadUsage(ctx, env, day); err == nil {
		v.LeadCost = u.Cost
	}
	if !set.ShowCosts {
		hideCosts(&v)
	}
	days, err := s.Store.LeadDays(ctx, env, 400)
	if err != nil {
		return v, err
	}
	for _, h := range days {
		if h.Day < day && v.Prev == "" {
			v.Prev = h.Day
		}
		if h.Day > day {
			v.Next = h.Day
		}
	}
	if v.Next == "" && day < today {
		v.Next = today
	}
	v.Markdown = Markdown(v, set.Name, s.now().Location())
	return v, nil
}

func hideCosts(v *LeadDayView) {
	v.Cost, v.LeadCost = 0, 0
	v.Facts.hideCosts()
}

// hideCosts takes the costs out of the facts.
func (f *LeadFacts) hideCosts() {
	f.Cost, f.Costed = 0, false
	for i := range f.Teammates {
		t := &f.Teammates[i]
		t.Cost, t.Costed = 0, false
		for j := range t.Threads {
			t.Threads[j].Cost = 0
		}
	}
}

// named writes the model's refs out as the names of what they stand for; a ref the day
// doesn't have is left out.
func named(s string, names map[string]string) string {
	return refToken.ReplaceAllStringFunc(s, func(tok string) string {
		return names[tok[1:len(tok)-1]]
	})
}

// mdSpecial are the characters escaped in a text of the model's or an agent's, so that a
// pasted day has no link, picture or markup it didn't write itself.
var mdSpecial = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "#", `\#`, "\n", " ")

// Markdown is a day as Markdown, its refs written out as names and links. Every text that
// isn't Studio's own is escaped.
func Markdown(v LeadDayView, lead string, loc *time.Location) string {
	f := v.Facts
	names := refNames(f)
	esc := mdSpecial.Replace
	text := func(s string) string { return esc(named(s, names)) }
	var b strings.Builder
	date, _ := time.ParseInLocation(time.DateOnly, v.Day, loc)
	title := date.Format("Monday 2 January 2006")
	if v.Prose != nil && v.Prose.Heading != "" {
		title += " · " + esc(v.Prose.Heading)
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	if v.Prose != nil && v.Prose.Summary != "" {
		b.WriteString(text(v.Prose.Summary) + "\n\n")
	}
	fmt.Fprintf(&b, "%s across %s on %s",
		duration(f.TurnSeconds), plural(f.Threads, "thread", "threads"), plural(f.Computers, "computer", "computers"))
	if len(f.Pushes) > 0 {
		fmt.Fprintf(&b, " · %s", plural(len(f.Pushes), "push", "pushes"))
	}
	if f.Costed {
		fmt.Fprintf(&b, " · %s reported", dollars(f.Cost))
	}
	b.WriteString("\n")
	if len(f.Waiting) > 0 {
		b.WriteString("\n## Waiting on you\n\n")
		for _, w := range f.Waiting {
			fmt.Fprintf(&b, "- %s: %s (since %s)\n", w.Kind, esc(w.Subject), w.Since.In(loc).Format("15:04"))
		}
	}
	lines := map[string][]LeadProseLine{}
	if v.Prose != nil {
		for _, t := range v.Prose.Teammates {
			lines[t.Ref] = t.Lines
		}
	}
	for _, t := range f.Teammates {
		fmt.Fprintf(&b, "\n## %s\n\n", esc(t.Name))
		if ls := lines[t.Ref]; len(ls) > 0 {
			for _, l := range ls {
				fmt.Fprintf(&b, "- %s: %s\n", l.State, text(l.Text))
			}
		} else {
			if t.Note != "" {
				b.WriteString(esc(t.Note) + "\n\n")
			}
			for _, th := range t.Threads {
				fmt.Fprintf(&b, "- %s: %s, %s\n", esc(th.Title), plural(th.Turns, "turn", "turns"), duration(th.Seconds))
			}
		}
	}
	if len(f.Pushes) > 0 {
		b.WriteString("\n## Shipped\n\n")
		for _, p := range f.Pushes {
			where := esc(p.Branch) + " to " + esc(p.Repo)
			if p.PRURL != "" {
				where += fmt.Sprintf(" ([#%d](%s))", p.PRNumber, p.PRURL)
			}
			fmt.Fprintf(&b, "- %s: %s\n", where, p.State)
		}
	}
	if len(f.Blockers) > 0 {
		b.WriteString("\n## Blockers\n\n")
		for _, bl := range f.Blockers {
			fmt.Fprintf(&b, "- %s\n", esc(bl.Text))
		}
	}
	if v.Prose != nil && len(v.Prose.Tomorrow) > 0 {
		b.WriteString("\n## Tomorrow\n\n")
		for _, t := range v.Prose.Tomorrow {
			fmt.Fprintf(&b, "- %s\n", text(t))
		}
	}
	if v.WrittenAt != nil {
		fmt.Fprintf(&b, "\n_Written by %s at %s._\n", esc(lead), v.WrittenAt.In(loc).Format("15:04"))
	}
	return b.String()
}

// refNames are the names refs stand for in plain text.
func refNames(f LeadFacts) map[string]string {
	out := map[string]string{}
	for _, t := range f.Teammates {
		out[t.Ref] = t.Name
		for _, th := range t.Threads {
			out[th.Ref] = "“" + th.Title + "”"
		}
		for _, r := range t.Tasks {
			out[r.Ref] = "“" + r.Title + "”"
		}
	}
	for _, p := range f.Pushes {
		if p.PRNumber > 0 {
			out[p.Ref] = fmt.Sprintf("#%d", p.PRNumber)
		} else {
			out[p.Ref] = p.Branch
		}
	}
	for _, w := range f.Waiting {
		out[w.Ref] = w.Subject
	}
	return out
}

// duration is how long turns lasted, as the web writes it (web/src/lib/time.ts): "<1 min",
// "12 min" or "1 h 20 min".
func duration(sec int64) string {
	h, m := sec/3600, sec%3600/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d h", h)
	case m > 0:
		return fmt.Sprintf("%d min", m)
	default:
		return "<1 min"
	}
}

// dollars is USD × 10^6 as "$3.62"; a cost under a cent as "<$0.01".
func dollars(micros int64) string {
	if micros > 0 && micros < 5000 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", float64(micros)/1e6)
}
