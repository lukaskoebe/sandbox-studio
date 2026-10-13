package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSecurityLevelsAndCuratedOff(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	if env.SecurityLevel != LevelGuarded || len(env.CuratedOff) != 0 {
		t.Fatalf("new environment: %+v", env)
	}
	if err := s.SetSecurityLevel(ctx, env.ID, LevelOpen); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecurityLevel(ctx, "missing", LevelOpen); err != ErrNotFound {
		t.Errorf("unknown environment: %v", err)
	}
	if err := s.SetCuratedOff(ctx, env.ID, []string{"npm", "pypi"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Environment(ctx, env.ID)
	if err != nil || got.SecurityLevel != LevelOpen || !slices.Equal(got.CuratedOff, []string{"npm", "pypi"}) {
		t.Fatalf("read back %+v: %v", got, err)
	}
	if err := s.SetCuratedOff(ctx, env.ID, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Environments(ctx)
	if len(list) != 1 || list[0].SecurityLevel != LevelOpen || list[0].CuratedOff == nil || len(list[0].CuratedOff) != 0 {
		t.Fatalf("list %+v", list)
	}
}

func TestTeammateSecurityLevelOverride(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	tm, err := s.CreateTeammate(ctx, Teammate{EnvironmentID: env.ID, Name: "iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if tm.SecurityLevel != "" {
		t.Fatalf("a new teammate overrides the level: %q", tm.SecurityLevel)
	}
	tm, err = s.SetTeammateSecurityLevel(ctx, env.ID, tm.ID, LevelLocked)
	if err != nil || tm.SecurityLevel != LevelLocked {
		t.Fatalf("override: %+v %v", tm, err)
	}
	// Editing the teammate keeps the override.
	tm.Name = "Iris"
	if tm, err = s.UpdateTeammate(ctx, tm); err != nil || tm.SecurityLevel != LevelLocked {
		t.Fatalf("after an edit: %+v %v", tm, err)
	}
	if tm, err = s.SetTeammateSecurityLevel(ctx, env.ID, tm.ID, ""); err != nil || tm.SecurityLevel != "" {
		t.Fatalf("back to the environment's: %+v %v", tm, err)
	}
	if _, err := s.SetTeammateSecurityLevel(ctx, env.ID, "missing", LevelOpen); err != ErrNotFound {
		t.Errorf("unknown teammate: %v", err)
	}
}

func TestRecordDecidedCountsAttempts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	a := Approval{EnvironmentID: env.ID, Kind: "network", Subject: "registry.npmjs.org:443", Payload: []byte(`{}`)}
	for i := range 3 {
		added, err := s.RecordDecided(ctx, a, StatusApproved, "")
		if err != nil || added != (i == 0) {
			t.Fatalf("record %d: added %v, %v", i, added, err)
		}
	}
	// A decision with a rule is a row of its own.
	if added, err := s.RecordDecided(ctx, a, StatusApproved, "rule1"); err != nil || !added {
		t.Fatalf("with a rule: added %v, %v", added, err)
	}
	list, _ := s.Approvals(ctx, env.ID, "", 10)
	if len(list) != 2 {
		t.Fatalf("rows %+v", list)
	}
	counts := map[string]int{}
	for _, row := range list {
		counts[row.RuleID] = row.Attempts
	}
	if counts[""] != 3 || counts["rule1"] != 1 {
		t.Fatalf("attempts %v", counts)
	}
	// A reverted row no longer absorbs attempts.
	var ruled Approval
	for _, row := range list {
		if row.RuleID == "rule1" {
			ruled = row
		}
	}
	if err := s.RevertApproval(ctx, env.ID, ruled.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevertApproval(ctx, env.ID, ruled.ID); err != ErrNotFound {
		t.Errorf("reverted twice: %v", err)
	}
	if added, err := s.RecordDecided(ctx, a, StatusApproved, "rule1"); err != nil || !added {
		t.Fatalf("after a revert: added %v, %v", added, err)
	}
	if got, _ := s.Approval(ctx, env.ID, ruled.ID); !got.Reverted {
		t.Error("the reverted flag is lost")
	}
}

func TestLastUserPrompt(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := s.LastUserPrompt(ctx, env.ID, sb.ID); err != nil || text != "" {
		t.Fatalf("no chat: %q %v", text, err)
	}
	chat, err := s.CreateChat(ctx, Chat{EnvironmentID: env.ID, SandboxID: sb.ID, Harness: "claude", State: ChatIdle, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.StartChatTurn(ctx, chat.ID, OriginChat, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first task", "fix the build"} {
		if _, err := s.AddChatItem(ctx, ChatItem{ChatID: chat.ID, TurnID: turn.ID, Kind: "user", Body: []byte(`{"text":"` + text + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if text, err := s.LastUserPrompt(ctx, env.ID, sb.ID); err != nil || text != "fix the build" {
		t.Fatalf("last prompt %q: %v", text, err)
	}
}
