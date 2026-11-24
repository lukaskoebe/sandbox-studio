package lead

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LeadFacts are a day as Studio's records have it. Every number, state and link the day page
// shows comes from here; the model only writes sentences about them. Each item has a ref, such
// as "push:2", that the sentences point at it with.
type LeadFacts struct {
	Day         string         `json:"day" doc:"The local date, YYYY-MM-DD"`
	From        time.Time      `json:"from"`
	Until       time.Time      `json:"until"`
	Waiting     []LeadWaiting  `json:"waiting" doc:"What waited for the user, oldest first"`
	Teammates   []LeadTeammate `json:"teammates" doc:"Who worked, most turn time first; the user's own computers have no id"`
	Pushes      []LeadPush     `json:"pushes"`
	Blockers    []LeadBlocker  `json:"blockers"`
	Threads     int            `json:"threads" doc:"Threads with a turn on the day"`
	Turns       int            `json:"turns"`
	TurnSeconds int64          `json:"turnSeconds"`
	Computers   int            `json:"computers" doc:"Computers with a turn on the day"`
	Cost        int64          `json:"cost" doc:"What the agents reported their turns cost, in USD × 10^6"`
	Costed      bool           `json:"costed" doc:"At least one agent reported a cost"`
}

// LeadWaiting is an approval that waits for the user.
type LeadWaiting struct {
	Ref         string    `json:"ref"`
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Subject     string    `json:"subject"`
	SandboxID   string    `json:"sandboxId,omitempty"`
	SandboxName string    `json:"sandboxName,omitempty"`
	TeammateID  string    `json:"teammateId,omitempty"`
	Since       time.Time `json:"since"`
}

// LeadTeammate is what one teammate did on the day.
type LeadTeammate struct {
	Ref         string        `json:"ref"`
	ID          string        `json:"id,omitempty" doc:"Empty for the user's own computers"`
	Name        string        `json:"name"`
	Avatar      string        `json:"avatar,omitempty"`
	Threads     []LeadThread  `json:"threads"`
	Computers   []string      `json:"computers" doc:"Names of the computers its turns ran on"`
	Turns       int           `json:"turns"`
	TurnSeconds int64         `json:"turnSeconds"`
	Cost        int64         `json:"cost"`
	Costed      bool          `json:"costed"`
	Tasks       []LeadTaskRun `json:"tasks"`
	Note        string        `json:"note,omitempty" doc:"Lead's note of the teammate's day"`
}

// LeadThread is a thread with turns on the day.
type LeadThread struct {
	Ref         string    `json:"ref"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	SandboxID   string    `json:"sandboxId"`
	SandboxName string    `json:"sandboxName"`
	Turns       int       `json:"turns"`
	Seconds     int64     `json:"seconds"`
	Running     bool      `json:"running" doc:"A turn still runs"`
	Stop        string    `json:"stop,omitempty" doc:"How the last turn ended, as the agent said"`
	Scheduled   bool      `json:"scheduled,omitempty"`
	Cost        int64     `json:"cost"`
	Last        time.Time `json:"last"`
}

// LeadPush is a push a teammate made on the day.
type LeadPush struct {
	Ref        string    `json:"ref"`
	ID         string    `json:"id"`
	SandboxID  string    `json:"sandboxId"`
	TeammateID string    `json:"teammateId,omitempty"`
	Forge      string    `json:"forge"`
	Repo       string    `json:"repo" doc:"owner/name"`
	Branch     string    `json:"branch"`
	State      string    `json:"state" enum:"pending,pushed,failed,rejected,superseded"`
	PRURL      string    `json:"prUrl,omitempty"`
	PRNumber   int       `json:"prNumber,omitempty"`
	At         time.Time `json:"at"`
}

// LeadTaskRun is a scheduled task's run on the day.
type LeadTaskRun struct {
	Ref     string    `json:"ref"`
	TaskID  string    `json:"taskId"`
	Title   string    `json:"title"`
	Outcome string    `json:"outcome" enum:"started,done,skipped,missed"`
	Reason  string    `json:"reason,omitempty"`
	ChatID  string    `json:"chatId,omitempty"`
	At      time.Time `json:"at"`
}

// LeadBlocker is something in the way, in Studio's words.
type LeadBlocker struct {
	Kind       string   `json:"kind" enum:"push,disk,denied,failed,stopped,task"`
	Text       string   `json:"text"`
	TeammateID string   `json:"teammateId,omitempty"`
	Refs       []string `json:"refs,omitempty"`
}

// span is the local day of t: from its midnight to the next, or to now for today.
func span(day time.Time, now time.Time) (from, until time.Time) {
	from = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	until = from.AddDate(0, 0, 1)
	if now.Before(until) {
		until = now
	}
	return from, until
}

// gather queries the day's facts from Studio's records. Notes are added by the caller.
func (s *Service) gather(ctx context.Context, env string, day time.Time, set store.LeadSettings) (LeadFacts, error) {
	now := s.now()
	from, until := span(day, now)
	// Records are queried to the next midnight: for today nothing is later than now, and a
	// record of this very second is in.
	end := from.AddDate(0, 0, 1)
	f := LeadFacts{Day: from.Format(time.DateOnly), From: from, Until: until,
		Waiting: []LeadWaiting{}, Teammates: []LeadTeammate{}, Pushes: []LeadPush{}, Blockers: []LeadBlocker{}}
	st := s.Store

	teammates, err := st.Teammates(ctx, env)
	if err != nil {
		return f, err
	}
	sandboxes, err := st.Sandboxes(ctx, env)
	if err != nil {
		return f, err
	}
	owner := map[string]string{} // sandbox -> teammate
	sbName := map[string]string{}
	for _, sb := range sandboxes {
		owner[sb.ID], sbName[sb.ID] = sb.TeammateID, sb.Name
	}
	byID := map[string]*LeadTeammate{}
	member := func(id string) *LeadTeammate {
		if t, ok := byID[id]; ok {
			return t
		}
		t := &LeadTeammate{ID: id, Name: "Your computers", Threads: []LeadThread{}, Computers: []string{}, Tasks: []LeadTaskRun{}}
		for _, p := range teammates {
			if p.ID == id {
				t.Name, t.Avatar = p.Name, p.Avatar
			}
		}
		byID[id] = t
		return t
	}
	include := func(teammate string) bool { return teammate != "" || set.OwnComputers }

	// Turns, folded per thread.
	turns, err := st.EnvironmentTurns(ctx, env, from, end)
	if err != nil {
		return f, err
	}
	threads := map[string]*LeadThread{}
	threadOwner := map[string]string{}
	var order []string
	computers := map[string]bool{}
	for _, t := range turns {
		if !include(t.TeammateID) || t.StartedAt.Before(from) && t.EndedAt != nil && t.EndedAt.Before(from) {
			continue
		}
		th := threads[t.ChatID]
		if th == nil {
			th = &LeadThread{ID: t.ChatID, Title: t.Title, SandboxID: t.SandboxID, SandboxName: sbName[t.SandboxID], Scheduled: t.Scheduled}
			if th.Title == "" {
				th.Title = "Untitled thread"
			}
			threads[t.ChatID] = th
			threadOwner[t.ChatID] = t.TeammateID
			order = append(order, t.ChatID)
		}
		start, end := t.StartedAt, until
		if t.EndedAt != nil {
			end = *t.EndedAt
		} else {
			th.Running = true
		}
		if start.Before(from) {
			start = from
		}
		if end.After(until) {
			end = until
		}
		th.Turns++
		if d := int64(end.Sub(start).Seconds()); d > 0 {
			th.Seconds += d
		}
		if end.After(th.Last) {
			th.Last = end
		}
		th.Stop = t.StopReason
		computers[t.SandboxID] = true
	}
	for _, id := range order {
		th := threads[id]
		th.Cost, _ = s.threadCost(ctx, id, from, until)
		t := member(threadOwner[id])
		t.Threads = append(t.Threads, *th)
		t.Turns += th.Turns
		t.TurnSeconds += th.Seconds
		if th.Cost > 0 {
			t.Cost += th.Cost
			t.Costed = true
		}
		if name := th.SandboxName; name != "" && !slices.Contains(t.Computers, name) {
			t.Computers = append(t.Computers, name)
		}
		f.Threads++
		f.Turns += th.Turns
		f.TurnSeconds += th.Seconds
		if th.Cost > 0 {
			f.Cost += th.Cost
			f.Costed = true
		}
		if th.Stop == "max_turn_requests" || th.Stop == "refusal" {
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "stopped", TeammateID: t.ID,
				Text: fmt.Sprintf("“%s” stopped early: %s.", th.Title, stopText(th.Stop))})
		}
	}
	f.Computers = len(computers)

	// Scheduled task runs.
	runs, err := st.TaskRunsIn(ctx, env, from, end)
	if err != nil {
		return f, err
	}
	titles := map[string]string{}
	for _, r := range runs {
		if _, ok := titles[r.TaskID]; !ok {
			if task, err := st.Task(ctx, env, r.TaskID); err == nil {
				titles[r.TaskID] = task.Title
			} else {
				titles[r.TaskID] = "a task"
			}
		}
		t := member(r.TeammateID)
		tr := LeadTaskRun{TaskID: r.TaskID, Title: titles[r.TaskID], Outcome: r.Outcome, Reason: r.Reason, ChatID: r.ChatID, At: r.At}
		t.Tasks = append(t.Tasks, tr)
		if r.Outcome == "missed" || r.Outcome == "skipped" {
			why := r.Reason
			if why == "" {
				why = r.Outcome
			}
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "task", TeammateID: r.TeammateID,
				Text: fmt.Sprintf("The run of “%s” at %s was %s: %s.", tr.Title, r.At.In(now.Location()).Format("15:04"), r.Outcome, why)})
		}
	}

	// Pushes.
	pushes, err := st.GitPushesIn(ctx, env, from, end)
	if err != nil {
		return f, err
	}
	for _, p := range pushes {
		tm := owner[p.SandboxID]
		if !include(tm) || p.State == store.PushSuperseded {
			continue
		}
		push := LeadPush{ID: p.ID, SandboxID: p.SandboxID, TeammateID: tm, Forge: p.ForgeName, Repo: p.Owner + "/" + p.Repo,
			Branch: strings.TrimPrefix(p.Ref, "refs/heads/"), State: p.State, PRURL: p.PRURL, PRNumber: prNumber(p.PRURL), At: p.CreatedAt}
		f.Pushes = append(f.Pushes, push)
		// The day's Shipped lists only what was pushed; what failed or was turned down is in the way.
		switch p.State {
		case store.PushFailed:
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "push", TeammateID: tm,
				Text: fmt.Sprintf("The push of %s to %s failed.", push.Branch, push.Repo)})
		case store.PushRejected:
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "push", TeammateID: tm,
				Text: fmt.Sprintf("The push of %s to %s was declined.", push.Branch, push.Repo)})
		}
	}

	// What waits for the user, whenever it began.
	pending, err := st.Approvals(ctx, env, "pending", 200)
	if err != nil {
		return f, err
	}
	for _, a := range pending {
		tm := owner[a.SandboxID]
		if a.SandboxID != "" && !include(tm) {
			continue
		}
		f.Waiting = append(f.Waiting, LeadWaiting{ID: a.ID, Kind: a.Kind, Subject: waitingSubject(a), SandboxID: a.SandboxID,
			SandboxName: sbName[a.SandboxID], TeammateID: tm, Since: a.CreatedAt})
	}
	slices.SortFunc(f.Waiting, func(a, b LeadWaiting) int { return a.Since.Compare(b.Since) })

	// Requests the network denied, and commands that failed again and again.
	flags, err := st.SessionFlags(ctx, env, from, end)
	if err != nil {
		return f, err
	}
	denied, failed := map[string]int{}, map[string]int{}
	for _, fl := range flags {
		if !include(owner[fl.SandboxID]) {
			continue
		}
		switch fl.Kind {
		case "blocked":
			denied[fl.SandboxID]++
		case "failed":
			failed[fl.SandboxID]++
		}
	}
	for _, sb := range sandboxes {
		if n := denied[sb.ID]; n > 0 {
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "denied", TeammateID: sb.TeammateID,
				Text: fmt.Sprintf("%s had %s denied by the network rules.", sb.Name, plural(n, "request", "requests"))})
		}
		if n := failed[sb.ID]; n >= 3 {
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "failed", TeammateID: sb.TeammateID,
				Text: fmt.Sprintf("%s ran %s that failed.", sb.Name, plural(n, "command", "commands"))})
		}
		if s.LowDisks == nil || !include(sb.TeammateID) {
			continue
		}
		for _, d := range s.LowDisks(sb.ID) {
			f.Blockers = append(f.Blockers, LeadBlocker{Kind: "disk", TeammateID: sb.TeammateID,
				Text: fmt.Sprintf("%s's %s disk is %d %% full.", sb.Name, d.Name, fullPercent(d))})
		}
	}

	for _, t := range byID {
		f.Teammates = append(f.Teammates, *t)
	}
	slices.SortFunc(f.Teammates, func(a, b LeadTeammate) int {
		if c := int(b.TurnSeconds - a.TurnSeconds); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	f.number()
	return f, nil
}

// number gives each item its ref, in the order the page shows them.
func (f *LeadFacts) number() {
	thread, task := 0, 0
	for i := range f.Teammates {
		t := &f.Teammates[i]
		t.Ref = "teammate:" + strconv.Itoa(i+1)
		for j := range t.Threads {
			thread++
			t.Threads[j].Ref = "thread:" + strconv.Itoa(thread)
		}
		for j := range t.Tasks {
			task++
			t.Tasks[j].Ref = "task:" + strconv.Itoa(task)
		}
	}
	for i := range f.Pushes {
		f.Pushes[i].Ref = "push:" + strconv.Itoa(i+1)
	}
	for i := range f.Waiting {
		f.Waiting[i].Ref = "approval:" + strconv.Itoa(i+1)
	}
}

// refs are the refs the facts have.
func (f *LeadFacts) refs() map[string]bool {
	out := map[string]bool{}
	for _, t := range f.Teammates {
		out[t.Ref] = true
		for _, th := range t.Threads {
			out[th.Ref] = true
		}
		for _, r := range t.Tasks {
			out[r.Ref] = true
		}
	}
	for _, p := range f.Pushes {
		out[p.Ref] = true
	}
	for _, w := range f.Waiting {
		out[w.Ref] = true
	}
	return out
}

// waitingSubject is what an approval asks, in words: an agent's permission request keeps its
// chat and item in the subject, so it says the tool call's title instead.
func waitingSubject(a store.Approval) string {
	if a.Kind == "chat.permission" {
		var p struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(a.Payload, &p) == nil && strings.TrimSpace(p.Title) != "" {
			return strings.TrimSpace(p.Title)
		}
		return "A tool call"
	}
	return a.Subject
}

// threadCost is what a thread's turns between from and until cost, by the cost the agent
// reports for its session so far: the last report before until less the last before from.
func (s *Service) threadCost(ctx context.Context, chatID string, from, until time.Time) (int64, error) {
	after, err := s.Store.ChatUsageBefore(ctx, chatID, until.Add(time.Second))
	if err != nil || after == nil {
		return 0, err
	}
	before, err := s.Store.ChatUsageBefore(ctx, chatID, from)
	if err != nil {
		return 0, err
	}
	a, b := usageCost(after), usageCost(before)
	if b > a { // a new session started over
		return a, nil
	}
	return a - b, nil
}

// usageCost reads a turn usage's cost in USD × 10^6; 0 without one in dollars.
func usageCost(usage []byte) int64 {
	if usage == nil {
		return 0
	}
	var u struct {
		Cost struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"cost"`
	}
	if json.Unmarshal(usage, &u) != nil || u.Cost.Amount <= 0 || u.Cost.Currency != "" && !strings.EqualFold(u.Cost.Currency, "USD") {
		return 0
	}
	return int64(u.Cost.Amount * 1e6)
}

// prNumber is the number at the end of a pull request's URL, 0 without one.
func prNumber(url string) int {
	i := strings.LastIndexByte(url, '/')
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(url[i+1:])
	return n
}

func stopText(reason string) string {
	switch reason {
	case "max_turn_requests":
		return "the agent reached its limit of requests in a turn"
	case "refusal":
		return "the agent refused to go on"
	}
	return reason
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func fullPercent(d agentproto.Disk) int {
	if d.Total <= 0 {
		return 100
	}
	return int(100 - d.Free*100/d.Total)
}
