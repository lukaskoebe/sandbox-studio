package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestTeammateTimeline(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	s.Memory = memory.New(s.Store.DB(), nil, nil)
	env, _ := s.Store.CreateEnvironment(ctx, "work")
	iris, _ := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	other, _ := s.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Otto", Harness: "claude", GitName: "Otto", GitEmail: "otto@agents.invalid"})
	sb, _ := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "docs-site", CPUs: 1, TeammateID: iris.ID})

	chat := func(teammate string, user, agent string, tools ...string) store.Chat {
		c, err := s.Store.CreateChat(ctx, store.Chat{EnvironmentID: env.ID, SandboxID: sb.ID, TeammateID: teammate, Harness: "claude", Title: "Links"})
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := s.Store.StartChatTurn(ctx, c.ID, "chat", "")
		add := func(kind, body string) {
			if _, err := s.Store.AddChatItem(ctx, store.ChatItem{ChatID: c.ID, TurnID: turn.ID, Kind: kind, Body: json.RawMessage(body)}); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := json.Marshal(map[string]string{"text": user})
		add("user", string(b))
		add("thought", `{"text":"hidden thinking"}`)
		for _, tool := range tools {
			b, _ := json.Marshal(map[string]any{"title": tool, "kind": "execute", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": "big output"}}}})
			add("tool", string(b))
		}
		b, _ = json.Marshal(map[string]string{"text": agent})
		add("agent", string(b))
		if _, err := s.Store.EndChatTurn(ctx, c.ID, turn.ID, "end_turn", nil); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := chat(iris.ID, "Fix the broken links on the docs site", "Fixed 3 links.", "rg -n href", "pnpm build")
	chat(other.ID, "Otto's question", "Otto's answer")
	if _, err := s.Memory.CreateFact(ctx, env.ID, memory.FactInput{Scope: memory.TeammateScope(iris.ID), Kind: "preference", Text: "Lukas likes short commit messages"},
		memory.Origin{Tier: memory.TierUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CreateTask(ctx, store.Task{EnvironmentID: env.ID, TeammateID: iris.ID, Title: "Bump vite", Status: store.TaskOpen, CreatedBy: store.TaskByUser}); err != nil {
		t.Fatal(err)
	}

	rec := do(h, "GET", "http://localhost:7878/api/environments/"+env.ID+"/teammates/"+iris.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body)
	}
	var list []TeammateEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range list {
		kinds[e.Kind]++
		if e.Kind == "turn" {
			tr := e.Turn
			if tr.ChatID != c.ID || tr.Prompt != "Fix the broken links on the docs site" || tr.Answer != "Fixed 3 links." || tr.ToolCount != 2 ||
				len(tr.Tools) != 2 || tr.Tools[1].Title != "pnpm build" || tr.Tools[1].Status != "completed" || e.SandboxName != "docs-site" || tr.EndedAt == nil {
				t.Errorf("turn %+v %+v", e, tr)
			}
		}
	}
	if kinds["turn"] != 1 || kinds["fact"] != 1 || kinds["task"] != 1 {
		t.Fatalf("kinds %v in %s", kinds, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hidden thinking") || strings.Contains(rec.Body.String(), "big output") || strings.Contains(rec.Body.String(), "Otto") {
		t.Fatalf("timeline shows too much: %s", rec.Body)
	}
	if rec := do(h, "GET", "http://localhost:7878/api/environments/"+env.ID+"/teammates/nope/timeline", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown teammate: %d", rec.Code)
	}
}
