package main

import "testing"

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
