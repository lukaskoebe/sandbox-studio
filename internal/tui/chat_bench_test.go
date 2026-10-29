package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// bigChat is sampleChat with n turns of a prompt, a thought, ten tool calls and an answer.
func bigChat(m *app, n int) *app {
	m = sampleChat(m)
	c := &m.chats
	ended := m.now.Add(-time.Minute)
	answer := strings.Repeat("Some **bold** text and `code` that wraps across the width of the screen. ", 8) +
		"\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\n- one\n- two\n- three\n"
	seq := 10
	for i := range n {
		tid := fmt.Sprintf("t%d", i+2)
		c.turns[tid] = chat.Turn{ChatTurn: store.ChatTurn{ID: tid, ChatID: "c1", Seq: i + 2, EndedAt: &ended}}
		add := func(kind string, fill func(*chat.Item)) {
			seq++
			it := item(fmt.Sprintf("x%d", seq), seq, kind)
			it.TurnID = tid
			fill(&it)
			c.items[it.ID] = it
		}
		add(chat.KindUser, func(it *chat.Item) { it.User = &chat.UserBody{Text: "Please do the next thing, carefully."} })
		add(chat.KindThought, func(it *chat.Item) { it.Text = &chat.TextBody{Text: "Thinking about it."} })
		for j := range 10 {
			add(chat.KindTool, func(it *chat.Item) {
				it.Tool = &chat.ToolBody{Title: fmt.Sprintf("Read src/file%d.go", j), Kind: "read", Status: "completed"}
			})
		}
		add(chat.KindAgent, func(it *chat.Item) { it.Text = &chat.TextBody{Text: answer} })
	}
	return m
}

// A long chat's transcript is laid out once; the frames after it, as the spinner turns, reuse it.
func BenchmarkLongChatFrame(b *testing.B) {
	m := bigChat(sampleApp(), 300)
	m.View()
	for b.Loop() {
		m.View()
	}
}
