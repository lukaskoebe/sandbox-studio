package browser

import (
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestOpenSpansPutSessionsOnTheirSandboxes(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	s := &Service{}
	st := s.stateLocked("env", "ada")
	st.vm, st.session, st.driver = "vm", "run1", store.Sandbox{ID: "web"}
	st.url = "https://example.com/"
	eph := &page{id: "eph1", slot: 1, owner: store.Sandbox{ID: "api"}, chat: "c1", opened: since, url: "https://example.com/checkout",
		takeover: true, turn: &userTurn{since: since.Add(time.Minute)}}
	st.eph = map[string]*page{eph.id: eph}
	idle := s.stateLocked("env", "juno") // a browser that doesn't run has no sessions
	idle.session = "run2"

	got := s.openSpans()["env"]
	if b := got[store.SpanBrowser]; len(b) != 2 ||
		b[0].SandboxID != "web" || b[0].Key != "profile:run1" || b[0].Label != "https://example.com/" || b[0].TeammateID != "ada" ||
		b[1].SandboxID != "api" || b[1].Key != "eph1" || b[1].ChatID != "c1" || !b[1].OpenedAt.Equal(since) {
		t.Errorf("browser spans = %+v", b)
	}
	if d := got[store.SpanDrive]; len(d) != 1 || d[0].SandboxID != "api" || d[0].Parent != "eph1" || !d[0].OpenedAt.Equal(since.Add(time.Minute)) {
		t.Errorf("drive spans = %+v", d)
	}
	st.driver = store.Sandbox{}
	if b := s.openSpans()["env"][store.SpanBrowser]; len(b) != 1 || b[0].Key != "eph1" {
		t.Errorf("without a driver, browser spans = %+v, want the ephemeral session alone", b)
	}
}
