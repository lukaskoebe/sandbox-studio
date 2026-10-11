package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestEncodeBodyCapsEscapedTextAndPreservesKind(t *testing.T) {
	options := []acp.PermissionOption{{OptionID: "allow", Name: "Allow", Kind: acp.AllowOnce}}
	cases := []struct {
		kind string
		body any
		ok   func(Item) bool
	}{
		{KindAgent, &TextBody{Text: strings.Repeat("\n", MaxBody)}, func(item Item) bool {
			return item.Text != nil && item.Text.Truncated && len(item.Text.Text) < MaxBody
		}},
		{KindTool, &ToolBody{Title: strings.Repeat("\x00", MaxBody), Input: json.RawMessage(`{"x":1}`)}, func(item Item) bool {
			return item.Tool != nil && item.Tool.Truncated
		}},
		{KindQuestion, &QuestionBody{Message: strings.Repeat("\x00", MaxBody), Schema: json.RawMessage(`{"type":"object"}`), Answer: json.RawMessage(`{"x":1}`)}, func(item Item) bool {
			return item.Question != nil
		}},
		{KindUser, &UserBody{Text: strings.Repeat("\n", MaxBody), Attachments: []Attachment{{Name: "notes.txt"}}}, func(item Item) bool {
			return item.User != nil
		}},
		{KindPlan, &PlanBody{Entries: []acp.PlanEntry{{Content: strings.Repeat("\x00", MaxBody), Priority: "high", Status: "pending"}}}, func(item Item) bool {
			return item.Plan != nil
		}},
		{KindPermission, &PermissionBody{ToolCall: acp.ToolCallUpdate{ToolCallID: "t1", Title: strings.Repeat("\x00", MaxBody)}, Options: options}, func(item Item) bool {
			return item.Permission != nil && len(item.Permission.Options) == 1 && item.Permission.Options[0].OptionID == "allow"
		}},
		{KindMarker, &MarkerBody{Kind: MarkerNotice, Text: strings.Repeat("\n", MaxBody)}, func(item Item) bool {
			return item.Marker != nil && item.Marker.Kind == MarkerNotice
		}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			raw := encodeBody(tc.body)
			if len(raw) > MaxBody {
				t.Fatalf("encoded body is %d bytes, cap is %d", len(raw), MaxBody)
			}
			item := ItemView(store.ChatItem{Kind: tc.kind, Body: raw})
			if !tc.ok(item) {
				t.Fatalf("body type or retained metadata was lost: %s", raw)
			}
		})
	}
}
