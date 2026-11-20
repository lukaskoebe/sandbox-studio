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
