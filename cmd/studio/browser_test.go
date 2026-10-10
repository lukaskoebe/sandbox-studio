package main

import (
	"testing"

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
