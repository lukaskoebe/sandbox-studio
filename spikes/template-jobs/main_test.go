//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

func TestFindPrivateTemplateInstanceUsesUniqueCatalogOwnership(t *testing.T) {
	rows := []store.Sandbox{
		{ID: "other-env", EnvironmentID: "env-other", Name: "instance", TemplateID: "template"},
		{ID: "other-name", EnvironmentID: "env", Name: "different", TemplateID: "template"},
		{ID: "owned", EnvironmentID: "env", Name: "instance", TemplateID: "template"},
	}
	got, err := findPrivateTemplateInstance(rows, "env", "instance", "template")
	if err != nil || got.ID != "owned" {
		t.Fatalf("findPrivateTemplateInstance() = %+v, %v; want the unique private row", got, err)
	}

	rows = append(rows, store.Sandbox{ID: "duplicate", EnvironmentID: "env", Name: "instance", TemplateID: "template"})
	if _, err := findPrivateTemplateInstance(rows, "env", "instance", "template"); err == nil {
		t.Fatal("findPrivateTemplateInstance accepted multiple rows for one cleanup identity")
	}

	rows = []store.Sandbox{{ID: "wrong-template", EnvironmentID: "env", Name: "instance", TemplateID: "different"}}
	if _, err := findPrivateTemplateInstance(rows, "env", "instance", "template"); err == nil {
		t.Fatal("findPrivateTemplateInstance accepted a name pinned to a different template")
	}
}

func TestBuildSourceVariantsHaveSameCanonicalSpec(t *testing.T) {
	setup := "set -eu\necho TEMPLATE_BUILD_TEST\n"
	blockSpec, err := templatespec.ParseYAML([]byte(buildSource(setup, false)))
	if err != nil {
		t.Fatalf("parse block-form build source: %v", err)
	}
	reorderedSpec, err := templatespec.ParseYAML([]byte(buildSource(setup, true)))
	if err != nil {
		t.Fatalf("parse reordered build source: %v", err)
	}
	blockCanonical, err := templatespec.CanonicalJSON(blockSpec)
	if err != nil {
		t.Fatalf("canonicalize block-form build source: %v", err)
	}
	reorderedCanonical, err := templatespec.CanonicalJSON(reorderedSpec)
	if err != nil {
		t.Fatalf("canonicalize reordered build source: %v", err)
	}
	if !bytes.Equal(blockCanonical, reorderedCanonical) {
		t.Fatalf("build source variants differ after canonicalization:\nblock:     %s\nreordered: %s", blockCanonical, reorderedCanonical)
	}
}

func TestNormalizeRequestedWorkerStopError(t *testing.T) {
	failure := errors.New("worker database failure")
	joined := errors.Join(context.Canceled, failure)
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "direct cancellation", err: context.Canceled},
		{name: "wrapped cancellation", err: fmt.Errorf("worker stopped: %w", context.Canceled)},
		{name: "worker failure", err: failure, want: failure},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "cancellation with worker failure", err: joined, want: joined},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRequestedStopError(tt.err); got != tt.want {
				t.Fatalf("normalizeRequestedStopError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
