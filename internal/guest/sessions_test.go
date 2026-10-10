//go:build linux

package guest

import (
	"reflect"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestParseSessions(t *testing.T) {
	out := "main|1|2|1791627258|\nclaude-2|0|1|1791627300|claude\nodd name|0|1|1|\nshort|0\n"
	want := []agentproto.Session{
		{Name: "main", Attached: 1, Windows: 2, Created: 1791627258},
		{Name: "claude-2", Windows: 1, Created: 1791627300, Harness: "claude"},
	}
	if got := parseSessions(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := parseSessions(""); len(got) != 0 {
		t.Fatalf("empty output: %+v", got)
	}
}
