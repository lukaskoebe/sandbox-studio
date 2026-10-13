package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// toolIntegration serves one tool.
type toolIntegration struct{ tool integrations.Tool }

func (toolIntegration) ID() string                                 { return "tool" }
func (toolIntegration) Routes() []gateway.VirtualHost              { return nil }
func (toolIntegration) ApprovalKinds() []integrations.ApprovalKind { return nil }
func (t toolIntegration) Tools() []integrations.Tool               { return []integrations.Tool{t.tool} }
func (toolIntegration) Jobs() []integrations.Job                   { return nil }

// A guest's call goes to the integration that serves its tool, with the sandbox the channel named.
// An internal error of a tool is shown as "<tool> failed", and a browser VM makes no calls.
func TestCallsRouteToolsByNameWithTheCallersSandbox(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	ada, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "agent", TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "other", TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	browserVM, err := st.CreateBrowserSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, TeammateID: ada.ID, Name: "browser-ada", CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}

	var seen []string
	tool := integrations.Tool{Def: agentcall.GitPushStatusTool, Handle: func(_ context.Context, sb store.Sandbox, _ json.RawMessage) (any, error) {
		seen = append(seen, sb.ID)
		if sb.ID == other.ID {
			return nil, errors.New("database is locked")
		}
		return map[string]string{"ok": "yes"}, nil
	}}
	var memory []string
	next := func(_ context.Context, _, method string, _ json.RawMessage) (any, error) {
		memory = append(memory, method)
		return "memory", nil
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	route := routeCalls(st, integrations.Set{toolIntegration{tool}}, nil, log, next)
	args := json.RawMessage(`{}`)

	if res, err := route(ctx, agent.ID, agentcall.GitPushStatusTool.Name, args); err != nil || res.(map[string]string)["ok"] != "yes" || seen[0] != agent.ID {
		t.Fatalf("tool call = %v, %v; handled for %v", res, err, seen)
	}
	if _, err := route(ctx, other.ID, agentcall.GitPushStatusTool.Name, args); err == nil || err.Error() != agentcall.GitPushStatusTool.Name+" failed" {
		t.Fatalf("an internal error reached the agent as %v", err)
	}
	if _, err := route(ctx, browserVM.ID, agentcall.GitPushStatusTool.Name, args); err == nil || len(seen) != 2 {
		t.Fatalf("a browser VM made a tool call: %v", err)
	}
	if _, err := route(ctx, "nope", agentcall.GitPushStatusTool.Name, args); err == nil {
		t.Fatal("a call from an unknown sandbox was answered")
	}
	if res, err := route(ctx, agent.ID, "memory_search", args); err != nil || res != "memory" || len(memory) != 1 {
		t.Fatalf("memory call = %v, %v; routed %v", res, err, memory)
	}
}
