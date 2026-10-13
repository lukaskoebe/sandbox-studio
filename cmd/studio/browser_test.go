package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tasks"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

func TestBrowserImage(t *testing.T) {
	for base, want := range map[string]string{
		"sandbox-studio-base:dev":                           "sandbox-studio-browser:dev",
		"ghcr.io/lukaskoebe/sandbox-studio-base:1.2.3":      "ghcr.io/lukaskoebe/sandbox-studio-browser:1.2.3",
		"ghcr.io/lukaskoebe/sandbox-studio-base@sha256:abc": "ghcr.io/lukaskoebe/sandbox-studio-browser:dev",
	} {
		if got := browserImage(base); got != want {
			t.Errorf("browserImage(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestBrowserImagePinned(t *testing.T) {
	base, brw := version.BaseImage, version.BrowserImage
	t.Cleanup(func() { version.BaseImage, version.BrowserImage = base, brw })
	version.BaseImage = "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:abc"
	version.BrowserImage = "ghcr.io/lukaskoebe/sandbox-studio-browser@sha256:def"
	if got := browserImage(version.BaseImage); got != version.BrowserImage {
		t.Errorf("own base: got %q", got)
	}
	if got := browserImage("sandbox-studio-base:dev"); got != "sandbox-studio-browser:dev" {
		t.Errorf("other base: got %q", got)
	}
}

// Task tools go to the task service and the other tools to the memory service.
func TestRouteCallsSendsTaskToolsToTheTaskService(t *testing.T) {
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tsk := &tasks.Service{Store: st}
	var next []string
	route := routeCalls(nil, tsk, func(_ context.Context, _, method string, _ json.RawMessage) (any, error) {
		next = append(next, method)
		return nil, nil
	})
	// The sandbox is unknown, so the task service answers with its own refusal.
	if _, err := route(context.Background(), "no-such-sandbox", "task_create", json.RawMessage(`{"title":"x"}`)); err == nil {
		t.Fatal("a task call from an unknown sandbox was accepted")
	}
	if _, err := route(context.Background(), "no-such-sandbox", "remember", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0] != "remember" {
		t.Fatalf("the memory service got %v", next)
	}
}
