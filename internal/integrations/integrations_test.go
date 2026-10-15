package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fakeIntegration struct {
	tools []Tool
	jobs  []Job
}

func (f fakeIntegration) ID() string                    { return "fake" }
func (f fakeIntegration) Routes() []gateway.VirtualHost { return nil }
func (f fakeIntegration) ApprovalKinds() []ApprovalKind { return nil }
func (f fakeIntegration) Tools() []Tool                 { return f.tools }
func (f fakeIntegration) Jobs() []Job                   { return f.jobs }

func TestToolIsFoundByItsCallName(t *testing.T) {
	var got string
	tool := Tool{Def: agentcall.Tool{Name: "fake_tool"}, Handle: func(_ context.Context, sb store.Sandbox, _ json.RawMessage) (any, error) {
		got = sb.ID
		return nil, nil
	}}
	set := Set{fakeIntegration{tools: []Tool{tool}}}
	found, ok := set.Tool("fake_tool")
	if !ok {
		t.Fatal("fake_tool not found")
	}
	if _, err := found.Handle(context.Background(), store.Sandbox{ID: "sb-1"}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "sb-1" {
		t.Fatalf("handler got sandbox %q", got)
	}
	if _, ok := set.Tool("hook"); ok {
		t.Fatal("a method no integration serves was found")
	}
}

func TestOnlyToolErrorsReachTheAgent(t *testing.T) {
	if !IsToolError(ToolError("no push of %s", "main")) {
		t.Fatal("ToolError is not a tool error")
	}
	if IsToolError(errors.New("database is locked")) {
		t.Fatal("an internal error would be shown to the agent")
	}
}

func TestRunJobsRunsOnIntervalUntilTheContextEnds(t *testing.T) {
	var runs, never atomic.Int32
	set := Set{fakeIntegration{jobs: []Job{
		{Name: "tick", Every: 5 * time.Millisecond, Run: func(context.Context) error { runs.Add(1); return errors.New("ignored") }},
		{Name: "off", Every: 0, Run: func(context.Context) error { never.Add(1); return nil }},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		set.RunJobs(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunJobs did not return after its context ended")
	}
	if runs.Load() < 3 {
		t.Fatalf("the job ran %d times", runs.Load())
	}
	if never.Load() != 0 {
		t.Fatal("a job with no interval ran")
	}
}
