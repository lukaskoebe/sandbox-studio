package api

import (
	"net/http"
	"testing"
)

func TestUISettings(t *testing.T) {
	h, _ := newTestServer(t)
	const path = "http://localhost:7878/api/settings/ui"
	if got := decode[UISettings](t, do(h, "GET", path, "")); got != (UISettings{AgentView: "chat"}) {
		t.Fatalf("default: %+v", got)
	}
	if rec := do(h, "PUT", path, `{"agentView":"tui"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid view: %d %s", rec.Code, rec.Body)
	}
	if got := decode[UISettings](t, do(h, "PUT", path, `{"agentView":"terminal"}`)); got != (UISettings{AgentView: "terminal", Asked: true}) {
		t.Fatalf("set: %+v", got)
	}
	if got := decode[UISettings](t, do(h, "GET", path, "")); got != (UISettings{AgentView: "terminal", Asked: true}) {
		t.Fatalf("stored: %+v", got)
	}
	if got := decode[UISettings](t, do(h, "PUT", path, `{"agentView":"chat"}`)); got != (UISettings{AgentView: "chat", Asked: true}) {
		t.Fatalf("chat: %+v", got)
	}
}
