package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type chatApprovalFixture struct {
	store *Store
	env   Environment
	sb    Sandbox
	chat  Chat
	turn  ChatTurn
	item  ChatItem
}

func newChatApprovalFixture(t *testing.T) chatApprovalFixture {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(ctx, Chat{EnvironmentID: env.ID, SandboxID: sb.ID, TeammateID: "tm", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.StartChatTurn(ctx, chat.ID, OriginChat, "")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"options": []any{}})
	item, err := s.AddChatItem(ctx, ChatItem{ChatID: chat.ID, TurnID: turn.ID, Kind: "permission", Status: "pending", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return chatApprovalFixture{store: s, env: env, sb: sb, chat: chat, turn: turn, item: item}
}

func TestRequestApprovalAndUpdateChatItemBindingAndRollback(t *testing.T) {
	f := newChatApprovalFixture(t)
	ctx := context.Background()
	initial, err := f.store.Chat(ctx, f.env.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, approval := range []Approval{
		{EnvironmentID: f.env.ID, SandboxID: f.sb.ID, Kind: "chat.permission", Subject: "chat/another-chat/" + f.item.ID},
		{EnvironmentID: f.env.ID, SandboxID: f.sb.ID, Kind: "chat.permission", Subject: "chat/" + f.chat.ID + "/another-item"},
		{EnvironmentID: "another-environment", SandboxID: f.sb.ID, Kind: "chat.permission", Subject: "chat/" + f.chat.ID + "/" + f.item.ID},
	} {
		approval.Payload = json.RawMessage(`{}`)
		if _, _, _, err := f.store.RequestApprovalAndUpdateChatItem(ctx, approval, f.item); !errors.Is(err, ErrConflict) {
			t.Fatalf("mismatched binding accepted: %v", err)
		}
	}
	if approvals, err := f.store.Approvals(ctx, f.env.ID, StatusPending, 10); err != nil || len(approvals) != 0 {
		t.Fatalf("mismatched requests left approvals %+v: %v", approvals, err)
	}

	missing := f.item
	missing.ID = "missing-item"
	approval := Approval{EnvironmentID: f.env.ID, SandboxID: f.sb.ID, Kind: "chat.permission",
		Subject: "chat/" + f.chat.ID + "/" + missing.ID, Payload: json.RawMessage(`{}`)}
	if _, _, _, err := f.store.RequestApprovalAndUpdateChatItem(ctx, approval, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing item update: %v", err)
	}
	if approvals, err := f.store.Approvals(ctx, f.env.ID, StatusPending, 10); err != nil || len(approvals) != 0 {
		t.Fatalf("failed item update left approval rows %+v: %v", approvals, err)
	}
	after, err := f.store.Chat(ctx, f.env.ID, f.chat.ID)
	if err != nil || after.Rev != initial.Rev {
		t.Fatalf("failed request was not rolled back: rev %d to %d, err %v", initial.Rev, after.Rev, err)
	}
}

func TestDecideApprovalAndUpdateChatItemBindingAndRollback(t *testing.T) {
	f := newChatApprovalFixture(t)
	ctx := context.Background()
	a, _, err := f.store.RequestApproval(ctx, Approval{EnvironmentID: f.env.ID, SandboxID: f.sb.ID,
		Kind: "chat.permission", Subject: "chat/" + f.chat.ID + "/missing-item", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := f.store.Chat(ctx, f.env.ID, f.chat.ID)
	if err != nil {
		t.Fatal(err)
	}

	wrongLink := f.item
	wrongLink.ApprovalID = a.ID
	if _, err := f.store.DecideApprovalAndUpdateChatItem(ctx, f.env.ID, a.ID, StatusApproved, "", wrongLink); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched subject accepted: %v", err)
	}
	missing := f.item
	missing.ID = "missing-item"
	missing.ApprovalID = a.ID
	// A matching subject with no corresponding item reaches the UPDATE after the
	// approval transition; the transaction must roll that transition back.
	if _, err := f.store.DecideApprovalAndUpdateChatItem(ctx, f.env.ID, a.ID, StatusApproved, "", missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing item update: %v", err)
	}
	if got, err := f.store.Approval(ctx, f.env.ID, a.ID); err != nil || got.Status != StatusPending {
		t.Fatalf("failed item update changed approval: %+v, %v", got, err)
	}
	after, err := f.store.Chat(ctx, f.env.ID, f.chat.ID)
	if err != nil || after.Rev != initial.Rev {
		t.Fatalf("failed decision was not rolled back: rev %d to %d, err %v", initial.Rev, after.Rev, err)
	}
}

// ChatItemsAfter reads the newest items after the cursor, in order, and no more than asked.
func TestChatItemsAfterKeepsTheNewest(t *testing.T) {
	f := newChatApprovalFixture(t)
	ctx := context.Background()
	for range 4 {
		if _, err := f.store.AddChatItem(ctx, ChatItem{ChatID: f.chat.ID, TurnID: f.turn.ID, Kind: "agent", Body: json.RawMessage(`{"text":"x"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := f.store.ChatItemsAfter(ctx, f.chat.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	last, err := f.store.ChatItemsAfter(ctx, f.chat.ID, all[0].Seq, 2)
	if err != nil || len(last) != 2 || last[0].Seq != all[len(all)-2].Seq || last[1].Seq != all[len(all)-1].Seq {
		t.Fatalf("the last two of %d: %+v, %v", len(all), last, err)
	}
}
