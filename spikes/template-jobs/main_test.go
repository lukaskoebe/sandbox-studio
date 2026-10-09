//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

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
