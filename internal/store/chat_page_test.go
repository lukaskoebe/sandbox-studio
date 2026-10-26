package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestPageFrom(t *testing.T) {
	turns := []ChatTurn{{ID: "a", Seq: 1}, {ID: "b", Seq: 2}, {ID: "c", Seq: 3}, {ID: "d", Seq: 4}}
	counts := map[string]int{"a": 2, "b": 5, "c": 1, "d": 3}
	for _, tc := range []struct {
		name                   string
		last, before, maxItems int
		want                   int
	}{
		{"the last two", 2, 0, 100, 3},
		{"every turn", 10, 0, 100, 1},
		{"before a turn", 2, 3, 100, 1},
		{"before the first", 2, 1, 100, 1},
		{"the items stop it", 10, 0, 4, 3},
		{"one turn past the items", 10, 0, 2, 4},
		{"one turn past the items, paging back", 10, 3, 1, 2},
	} {
		if got := pageFrom(turns, counts, tc.last, tc.before, tc.maxItems); got != tc.want {
			t.Errorf("%s: from %d, want %d", tc.name, got, tc.want)
		}
	}
	if got := pageFrom(nil, nil, 40, 0, 500); got != 0 {
		t.Errorf("no turns: from %d", got)
	}
}

func TestChatPage(t *testing.T) {
	f := newChatApprovalFixture(t)
	ctx := context.Background()
	s := f.store
	// The fixture's turn 1 has a permission item and no message; turns 2 to 5 have a
	// message and an answer each.
	for i := 2; i <= 5; i++ {
		turn, err := s.StartChatTurn(ctx, f.chat.ID, OriginChat, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range []struct{ kind, text string }{{"user", fmt.Sprintf("ask %d", i)}, {"agent", "done"}, {"user", "a second message"}} {
			body, _ := json.Marshal(map[string]string{"text": it.text})
			if _, err := s.AddChatItem(ctx, ChatItem{ChatID: f.chat.ID, TurnID: turn.ID, Kind: it.kind, Body: body}); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, err := s.ChatPage(ctx, f.env.ID, f.chat.ID, 2, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Turns) != 5 || p.From != 4 || len(p.Items) != 6 || p.Chat.ID != f.chat.ID {
		t.Fatalf("page: %d turns, from %d, %d items", len(p.Turns), p.From, len(p.Items))
	}
	if p.Items[0].TurnID != p.Turns[3].ID || p.Items[5].TurnID != p.Turns[4].ID {
		t.Fatalf("items of turns %s to %s", p.Items[0].TurnID, p.Items[5].TurnID)
	}
	if len(p.Prompts) != 4 || p.Prompts[p.Turns[1].ID] != "ask 2" || p.Prompts[p.Turns[4].ID] != "ask 5" {
		t.Fatalf("prompts %v", p.Prompts)
	}
	if latest, err := s.Chat(ctx, f.env.ID, f.chat.ID); err != nil || latest.Rev != p.Chat.Rev {
		t.Fatalf("rev %d, want %d (%v)", p.Chat.Rev, latest.Rev, err)
	}

	// Back from turn 4: turns 2 and 3; then turn 1, which has the permission item.
	p, err = s.ChatPage(ctx, f.env.ID, f.chat.ID, 2, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != 2 || len(p.Items) != 6 || p.Items[0].TurnID != p.Turns[1].ID {
		t.Fatalf("page back: from %d, %d items", p.From, len(p.Items))
	}
	p, err = s.ChatPage(ctx, f.env.ID, f.chat.ID, 2, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != 1 || len(p.Items) != 1 || p.Items[0].ID != f.item.ID {
		t.Fatalf("first page: from %d, %d items", p.From, len(p.Items))
	}
	p, err = s.ChatPage(ctx, f.env.ID, f.chat.ID, 2, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != 1 || len(p.Items) != 0 {
		t.Fatalf("before the first: from %d, %d items", p.From, len(p.Items))
	}
	if _, err := s.ChatPage(ctx, f.env.ID, "nope", 2, 0, 100); err != ErrNotFound {
		t.Fatalf("missing chat: %v", err)
	}
}
