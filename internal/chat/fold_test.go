package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// foldStream folds a stream of updates, one JSON object per line, as a live turn does.
func foldStream(t *testing.T, path string, users bool) *folder {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	f := newFolder(func() string { n++; return "i" + strconv.Itoa(n) })
	f.users = users
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var u acp.SessionUpdate
		if err := json.Unmarshal(sc.Bytes(), &u); err != nil {
			t.Fatalf("%s: %v", sc.Text(), err)
		}
		f.apply(u)
	}
	f.finish()
	return f
}

type goldenItem struct {
	Kind       string          `json:"kind"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Status     string          `json:"status,omitempty"`
	Body       json.RawMessage `json:"body"`
}

// Recorded update streams per harness fold into the expected items. The streams are
// written after the adapters' sources and the ACP schema, not recorded from live runs.
func TestFoldGolden(t *testing.T) {
	streams, _ := filepath.Glob("testdata/fold/*.jsonl")
	if len(streams) != 3 {
		t.Fatalf("streams: %v", streams)
	}
	for _, path := range streams {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f := foldStream(t, path, false)
			out := struct {
				Items []goldenItem `json:"items"`
				Title string       `json:"title,omitempty"`
				Usage TurnUsage    `json:"usage"`
			}{Title: f.title, Usage: f.usage}
			for _, e := range f.entries {
				out.Items = append(out.Items, goldenItem{Kind: e.kind, ToolCallID: e.toolCallID, Status: e.status, Body: encodeBody(e.body)})
			}
			got, _ := json.MarshalIndent(out, "", "  ")
			golden := strings.TrimSuffix(path, ".jsonl") + ".golden.json"
			if *update {
				os.WriteFile(golden, append(got, '\n'), 0o644)
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(got)+"\n" {
				t.Errorf("%s differs; got\n%s", golden, got)
			}
		})
	}
}

func TestFoldOpenTextAndTools(t *testing.T) {
	f := newFolder(func() string { return "x" })
	chunk := func(kind, text string) acp.SessionUpdate {
		raw, _ := json.Marshal(acp.ContentBlock{Type: "text", Text: text})
		return acp.SessionUpdate{Kind: kind, Content: raw}
	}
	if ch := f.apply(chunk(acp.UpdateAgentMessageChunk, "a")); len(ch) != 1 || ch[0].ended {
		t.Fatalf("first chunk: %+v", ch)
	}
	if ch := f.apply(chunk(acp.UpdateAgentMessageChunk, "b")); len(ch) != 1 || ch[0].ended {
		t.Fatalf("second chunk: %+v", ch)
	}
	// A thought closes the message.
	ch := f.apply(chunk(acp.UpdateAgentThoughtChunk, "hm"))
	if len(ch) != 2 || !ch[0].ended || ch[0].e.body.(*TextBody).Text != "ab" || ch[1].e.kind != KindThought {
		t.Fatalf("thought: %+v", ch)
	}
	// User chunks are folded only in replays.
	if ch := f.apply(chunk(acp.UpdateUserMessageChunk, "hi")); ch != nil {
		t.Fatalf("user chunk: %+v", ch)
	}
	title := "Run"
	ch = f.apply(acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: "t1", Title: &title, Status: "completed"})
	if len(ch) != 2 || !ch[1].ended || ch[1].e.status != "completed" {
		t.Fatalf("finished tool: %+v", ch)
	}
	// An update for an unknown call starts it.
	ch = f.apply(acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: "t2", Status: "in_progress"})
	if len(ch) != 1 || ch[0].e.toolCallID != "t2" || ch[0].ended {
		t.Fatalf("unknown tool: %+v", ch)
	}
	if len(f.entries) != 4 {
		t.Fatalf("entries: %d", len(f.entries))
	}
}

func TestFoldCapsText(t *testing.T) {
	f := newFolder(func() string { return "x" })
	raw, _ := json.Marshal(acp.ContentBlock{Type: "text", Text: strings.Repeat("é", MaxBody/4)})
	for i := 0; i < 4; i++ {
		f.apply(acp.SessionUpdate{Kind: acp.UpdateAgentMessageChunk, Content: raw})
	}
	b := f.entries[0].body.(*TextBody)
	if !b.Truncated || len(b.Text) > MaxBody {
		t.Fatalf("len %d truncated %v", len(b.Text), b.Truncated)
	}
	if len(encodeBody(b)) > MaxBody {
		t.Fatal("body over the cap")
	}
}

func TestEncodeBodyTruncatesTools(t *testing.T) {
	big := strings.Repeat("x", MaxBody)
	b := &ToolBody{Title: "cat", Input: json.RawMessage(`{"a":1}`), Content: []acp.ToolCallContent{
		{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: big}},
		{Type: "diff", Path: "/workspace/a", NewText: big},
	}}
	raw := encodeBody(b)
	if len(raw) > MaxBody {
		t.Fatalf("%d bytes", len(raw))
	}
	var got ToolBody
	if err := json.Unmarshal(raw, &got); err != nil || !got.Truncated || got.Input != nil || len(got.Content) != 2 || got.Title != "cat" {
		t.Fatalf("%v %+v", err, got)
	}
}
