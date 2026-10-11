package main

import (
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

func TestDefaultImage(t *testing.T) {
	v, base := version.Version, version.BaseImage
	t.Cleanup(func() { version.Version, version.BaseImage = v, base })
	version.BaseImage = ""
	for ver, want := range map[string]string{
		"dev":               "sandbox-studio-base:dev",
		"704c0c9":           "sandbox-studio-base:dev",
		"704c0c9-dirty":     "sandbox-studio-base:dev",
		"v0.1.0-3-g704c0c9": "sandbox-studio-base:dev",
		"v0.1.0-dirty":      "sandbox-studio-base:dev",
		"v0.1.0":            "ghcr.io/lukaskoebe/sandbox-studio-base:v0.1.0",
		"v0.1.0-rc.1":       "ghcr.io/lukaskoebe/sandbox-studio-base:v0.1.0-rc.1",
	} {
		version.Version = ver
		if got := defaultImage(); got != want {
			t.Errorf("defaultImage() for %q = %q, want %q", ver, got, want)
		}
	}
	version.Version = "v0.1.0"
	version.BaseImage = "ghcr.io/lukaskoebe/sandbox-studio-base@sha256:abc"
	if got := defaultImage(); got != version.BaseImage {
		t.Errorf("defaultImage() = %q, want the pinned digest", got)
	}
}
