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

func TestServiceAddrs(t *testing.T) {
	for addr, want := range map[string][2]string{
		"127.0.0.1:7878": {"127.0.0.1:7879", "127.0.0.1:7880"},
		"127.0.0.1:7978": {"127.0.0.1:7979", "127.0.0.1:7980"},
		"[::1]:9000":     {"127.0.0.1:9001", "127.0.0.1:9002"},
	} {
		gateway, registry, err := serviceAddrs(addr)
		if err != nil || gateway != want[0] || registry != want[1] {
			t.Errorf("serviceAddrs(%q) = %q, %q, %v; want %q, %q", addr, gateway, registry, err, want[0], want[1])
		}
	}
	for _, addr := range []string{"127.0.0.1", "127.0.0.1:http", "127.0.0.1:0", "127.0.0.1:65534"} {
		if _, _, err := serviceAddrs(addr); err == nil {
			t.Errorf("serviceAddrs(%q) succeeded", addr)
		}
	}
}
