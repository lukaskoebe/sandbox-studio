package sandboxes

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestToolOrigins(t *testing.T) {
	pins := map[string]string{"node": "24", "python": "3.12"}
	for _, tc := range []struct {
		tool agentproto.MiseTool
		want string
	}{
		{agentproto.MiseTool{Name: "node", Requested: "24", Config: globalConfig}, ToolTemplate},
		// `mise use -g node@22` replaced the template's pin.
		{agentproto.MiseTool{Name: "node", Requested: "22", Config: globalConfig}, ToolAdded},
		{agentproto.MiseTool{Name: "go", Requested: "1.25", Config: globalConfig}, ToolAdded},
		{agentproto.MiseTool{Name: "npm:prettier", Requested: "latest", Config: keptConfig}, ToolKept},
		{agentproto.MiseTool{Name: "python", Requested: "3.12", Config: "/workspace/app/mise.toml"}, ToolProject},
		{agentproto.MiseTool{Name: "node", Version: "22.1.0"}, ToolUnpinned},
	} {
		if got := origin(tc.tool, pins); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.tool, got, tc.want)
		}
	}
	// Without the template's pins, the global config's tools were added.
	if got := origin(agentproto.MiseTool{Name: "node", Requested: "24", Config: globalConfig}, nil); got != ToolAdded {
		t.Errorf("no template: %s", got)
	}
}

func TestGuestErrorTellsAnOlderGuest(t *testing.T) {
	if err := guestError(errors.New(`unknown stream kind "tools"`)); !errors.Is(err, ErrGuestTooOld) {
		t.Fatalf("got %v", err)
	}
	other := errors.New("mise ls: exit code 1")
	if err := guestError(other); err != other {
		t.Fatalf("got %v", err)
	}
}
