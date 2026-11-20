//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LeadModel is the e2e Studio's utility model for Lead: it makes no connection, and writes
// the day from the facts it is given, so a spec can check the sentences and their refs.
func LeadModel(store.Provider, string) (agentmem.LLM, agentmem.UtilityModel, error) {
	return leadModel{}, agentmem.UtilityModel{Kind: "e2e", Model: "e2e-writer", PriceInPerM: 1, PriceOutPerM: 5, Priced: true}, nil
}

type leadModel struct{}

func (leadModel) Complete(_ context.Context, r agentmem.Request) (agentmem.Response, error) {
	if body, ok := between(r.User, "<new>\n", "\n</new>"); ok {
		var in struct {
			Turns []struct{ Thread, Prompt string }
		}
		_ = json.Unmarshal([]byte(body), &in)
		if len(in.Turns) == 0 {
			return agentmem.Response{Text: "Read through the day's sessions.", InputTokens: 300, OutputTokens: 10}, nil
		}
		last := in.Turns[len(in.Turns)-1]
		return agentmem.Response{Text: fmt.Sprintf("Working on %s: %s", last.Thread, last.Prompt), InputTokens: 300, OutputTokens: 20}, nil
	}
	if team, ok := between(r.User, "<team>\n", "\n</team>"); ok {
		return leadPlan(team, r.User)
	}
	if threads, ok := between(r.User, "<threads>\n", "\n</threads>"); ok {
		var in []struct{ Teammate, Thread, Answer string }
		_ = json.Unmarshal([]byte(threads), &in)
		var parts []string
		for _, t := range in {
			if t.Answer == "" {
				parts = append(parts, t.Teammate+" hasn't answered yet.")
			} else {
				parts = append(parts, fmt.Sprintf("%s answered in %s.", t.Teammate, t.Thread))
			}
		}
		return agentmem.Response{Text: strings.Join(parts, " "), InputTokens: 800, OutputTokens: 40}, nil
	}
	body, ok := between(r.User, "<facts>\n", "\n</facts>")
	if !ok {
		return agentmem.Response{}, fmt.Errorf("e2e lead model: no facts")
	}
	var f struct {
		Teammates []struct {
			Ref, Name string
			Threads   []struct{ Ref, Title string }
		}
		Pushes  []struct{ Ref string }
		Waiting []struct{ Ref string }
	}
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		return agentmem.Response{}, err
	}
	if q, ok := between(r.User, "<question>\n", "\n</question>"); ok {
		// An answer: the first teammate's first thread, and the question it was asked.
		var question string
		_ = json.Unmarshal([]byte(q), &question)
		text := "Nobody worked on that day."
		if len(f.Teammates) > 0 && len(f.Teammates[0].Threads) > 0 {
			th := f.Teammates[0].Threads[0]
			text = fmt.Sprintf("%s worked on [%s].", f.Teammates[0].Name, th.Ref)
		}
		text += fmt.Sprintf("\n\nYou asked: %s", question)
		return agentmem.Response{Text: text, InputTokens: 2000, OutputTokens: 60}, nil
	}
	type line struct {
		State string `json:"state"`
		Text  string `json:"text"`
	}
	type teammate struct {
		Ref   string `json:"ref"`
		Lines []line `json:"lines"`
	}
	out := struct {
		Heading   string     `json:"heading"`
		Summary   string     `json:"summary"`
		Teammates []teammate `json:"teammates"`
		Tomorrow  []string   `json:"tomorrow"`
	}{Heading: "a steady day", Teammates: []teammate{}, Tomorrow: []string{}}
	var names []string
	for _, t := range f.Teammates {
		names = append(names, t.Name)
		tm := teammate{Ref: t.Ref}
		for _, th := range t.Threads {
			tm.Lines = append(tm.Lines, line{State: "done", Text: fmt.Sprintf("Moved %s along [%s].", th.Title, th.Ref)})
		}
		out.Teammates = append(out.Teammates, tm)
	}
	out.Summary = strings.Join(names, " and ") + " worked today."
	if len(f.Waiting) > 0 {
		out.Summary += fmt.Sprintf(" One thing waits for you [%s].", f.Waiting[0].Ref)
	}
	if len(f.Pushes) > 0 {
		out.Tomorrow = append(out.Tomorrow, fmt.Sprintf("Review [%s] first.", f.Pushes[0].Ref))
	}
	b, _ := json.Marshal(out)
	return agentmem.Response{Text: string(b), InputTokens: len(r.User) / 4, OutputTokens: len(b) / 4}, nil
}

func between(s, open, close string) (string, bool) {
	i := strings.Index(s, open)
	j := strings.LastIndex(s, close)
	if i < 0 || j < i+len(open) {
		return "", false
	}
	return s[i+len(open) : j], true
}

// leadPlan drafts a plan from the team and the conversation: a question when the user's last
// message asks for one, a new thread when it says so, and otherwise a message to a thread,
// with the user's words. The teammate is the one the conversation last names, else the first.
func leadPlan(team, user string) (agentmem.Response, error) {
	var t struct {
		Teammates []struct{ Ref, Name string }
		Computers []struct{ Ref, Teammate string }
		Threads   []struct{ Ref, Title, Teammate string }
	}
	if err := json.Unmarshal([]byte(team), &t); err != nil {
		return agentmem.Response{}, err
	}
	conv, _ := between(user, "<conversation>\n", "\n</conversation>")
	var said []struct{ Who, Text string }
	_ = json.Unmarshal([]byte(conv), &said)
	last := ""
	for _, s := range said {
		if s.Who == "user" {
			last = s.Text
		}
	}
	type step struct {
		Kind     string `json:"kind"`
		Thread   string `json:"thread,omitempty"`
		Teammate string `json:"teammate,omitempty"`
		Computer string `json:"computer,omitempty"`
		Title    string `json:"title,omitempty"`
		Text     string `json:"text"`
	}
	// The teammate the user's latest message naming one names, as another spec's teammates
	// share the e2e Studio.
	tm := -1
	for j := len(said) - 1; j >= 0 && tm < 0; j-- {
		if said[j].Who != "user" {
			continue
		}
		for i, m := range t.Teammates {
			if strings.Contains(said[j].Text, m.Name) {
				tm = i
				break
			}
		}
	}
	if tm < 0 && len(t.Teammates) > 0 {
		tm = 0
	}
	var thread *struct{ Ref, Title, Teammate string }
	for i, th := range t.Threads {
		if tm >= 0 && th.Teammate == t.Teammates[tm].Ref {
			thread = &t.Threads[i]
			break
		}
	}
	out := struct {
		Reply    string `json:"reply"`
		Title    string `json:"title"`
		Steps    []step `json:"steps"`
		Question any    `json:"question,omitempty"`
	}{Title: "Check the tests", Steps: []step{}}
	switch {
	case strings.Contains(last, "ask me"):
		out.Reply = "Which branch do you mean?"
		out.Question = map[string]any{"text": "Which branch?", "options": []string{"main", "release"}}
	case strings.Contains(last, "new thread") && tm >= 0:
		m := t.Teammates[tm]
		for _, c := range t.Computers {
			if c.Teammate == m.Ref {
				out.Steps = append(out.Steps, step{Kind: "start", Teammate: m.Ref, Computer: c.Ref, Title: "Lead's errand", Text: "Please " + last})
				break
			}
		}
		out.Reply = fmt.Sprintf("I'll ask %s in a new thread.", m.Name)
	case thread != nil:
		out.Steps = append(out.Steps, step{Kind: "message", Thread: thread.Ref, Text: "Please " + last})
		out.Reply = fmt.Sprintf("I'll ask in %s.", thread.Title)
	default:
		out.Reply = "Nobody on the team has a thread for that."
	}
	b, _ := json.Marshal(out)
	return agentmem.Response{Text: string(b), InputTokens: len(user) / 4, OutputTokens: len(b) / 4}, nil
}
