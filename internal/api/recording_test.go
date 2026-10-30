package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A sandbox's traffic is recorded once the user starts it, until it is stopped or cleared.
func TestRecordingTraffic(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "recording")
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: env.ID, Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	base := "http://localhost:7878/api/environments/" + url.PathEscape(env.ID) + "/sandboxes/" + url.PathEscape(sb.ID) + "/recording"
	state := func(rec interface {
		Result() *http.Response
	}) gateway.RecordingState {
		t.Helper()
		var st gateway.RecordingState
		if err := json.NewDecoder(rec.Result().Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}

	if rec := do(h, http.MethodGet, base, ""); rec.Code != http.StatusOK || state(rec).Recording {
		t.Fatalf("before: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, http.MethodPut, base, `{"recording":true}`)
	if st := state(rec); rec.Code != http.StatusOK || !st.Recording || st.Until == nil {
		t.Fatalf("start: %d %+v %s", rec.Code, st, rec.Body)
	}
	if !s.Records.State(sb.ID).Recording {
		t.Error("the gateway doesn't record")
	}
	if rec := do(h, http.MethodGet, base+"/requests", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Errorf("requests: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodPut, base, `{"recording":false}`)
	if st := state(rec); rec.Code != http.StatusOK || st.Recording || st.Expires == nil {
		t.Errorf("stop: %d %+v", rec.Code, st)
	}
	if rec := do(h, http.MethodDelete, base, ""); rec.Code != http.StatusNoContent {
		t.Errorf("clear: %d %s", rec.Code, rec.Body)
	}
	if st := s.Records.State(sb.ID); st.Expires != nil {
		t.Errorf("cleared, it kept %+v", st)
	}
	if rec := do(h, http.MethodPut, "http://localhost:7878/api/environments/"+url.PathEscape(env.ID)+"/sandboxes/nope/recording", `{"recording":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("a missing sandbox: %d", rec.Code)
	}
}
