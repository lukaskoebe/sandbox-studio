package chat

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

func TestPromptBlocksLinkMentions(t *testing.T) {
	p := Prompt{Text: "Why does @web/checkout.tsx validate before @web/coupon code.ts:20-28?", Mentions: []Mention{
		{Path: "web/checkout.tsx"},
		{Path: "web/coupon code.ts", Start: 20, End: 28},
		{Path: "README.md", Start: 3},
	}}
	blocks, _, err := promptBlocks(acp.AgentCapabilities{}, p)
	if err != nil {
		t.Fatal(err)
	}
	want := []acp.ContentBlock{
		{Type: "text", Text: p.Text},
		{Type: "resource_link", URI: "file:///workspace/web/checkout.tsx", Name: "checkout.tsx"},
		{Type: "resource_link", URI: "file:///workspace/web/coupon%20code.ts#L20-L28", Name: "coupon code.ts"},
		{Type: "resource_link", URI: "file:///workspace/README.md#L3", Name: "README.md"},
	}
	if len(blocks) != len(want) {
		t.Fatalf("blocks = %+v", blocks)
	}
	for i := range want {
		if blocks[i].Type != want[i].Type || blocks[i].Text != want[i].Text || blocks[i].URI != want[i].URI || blocks[i].Name != want[i].Name {
			t.Errorf("block %d = %+v, want %+v", i, blocks[i], want[i])
		}
	}

	for _, m := range []Mention{
		{Path: ""}, {Path: "/etc/passwd"}, {Path: "../home/agent/.ssh/id"}, {Path: "a/../../b"},
		{Path: "a//b"}, {Path: "."}, {Path: "a\nb"}, {Path: "a", Start: 5, End: 2}, {Path: "a", End: 3},
		{Path: "a", Start: -1},
	} {
		if _, _, err := promptBlocks(acp.AgentCapabilities{}, Prompt{Text: "x", Mentions: []Mention{m}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", m, err)
		}
	}
}
