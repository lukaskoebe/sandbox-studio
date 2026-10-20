package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TestReviewFeedbackToANewChat: feedback can start a chat in the push's sandbox, with the
// review as its first prompt.
func TestReviewFeedbackToANewChat(t *testing.T) {
	agent := &acptest.Agent{SessionID: "ses_1", Turns: []acptest.Turn{
		{Steps: []acptest.Step{{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"On it"}}`)}}},
	}}
	h, s, _, env := newChatServer(t, agent)
	s.Git = &gitreview.Service{Store: s.Store, Secrets: s.Vault, Bus: s.Bus, Dir: t.TempDir()}
	s.Integrations = integrations.Set{s.Git}
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	sb := newChatSandbox(t, h, s, env, prov.ID, "claude")
	ctx := context.Background()
	payload, _ := json.Marshal(gitreview.PushReview{Sandbox: sb.Name, Branch: "main", New: strings.Repeat("b", 40)})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: gitreview.KindPush, Subject: "cb/o/r:main", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CreateGitPush(ctx, store.GitPush{ID: store.NewID(), EnvironmentID: env.ID, SandboxID: sb.ID, ForgeName: "cb",
		Owner: "o", Repo: "r", Ref: "refs/heads/main", NewSHA: strings.Repeat("b", 40), ApprovalID: a.ID}); err != nil {
		t.Fatal(err)
	}
	envBase := testOrigin + "/api/environments/" + env.ID
	url := envBase + "/approvals/" + a.ID + "/feedback"
	for _, body := range []string{`{"note":"x"}`, `{"newChat":true,"chat":"c1"}`, `{"newChat":true,"terminal":"claude"}`} {
		if rec := do(h, "POST", url, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", url, `{"newChat":true,"note":"Split the migration out."}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("feedback: %d %s", rec.Code, rec.Body)
	}
	out := decode[FeedbackOut](t, rec)
	if out.Chat == "" || out.Queued {
		t.Fatalf("feedback = %+v", out)
	}
	log := waitIdle(t, h, envBase+"/chats/"+out.Chat)
	if log.Chat.SandboxID != sb.ID || len(log.Items) == 0 || log.Items[0].User == nil ||
		!strings.Contains(log.Items[0].User.Text, "Review of your push") || !strings.Contains(log.Items[0].User.Text, "Split the migration out.") {
		t.Fatalf("log: %+v", log)
	}
}
