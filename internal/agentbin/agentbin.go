// Package agentbin embeds the Linux studio-agent binaries that Studio mounts into sandboxes.
// `make agent` writes them to dist/ before `go build`.
package agentbin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:dist
var dist embed.FS

// Install writes the agent for arch to dir/bin/studio-agent if its content changed.
func Install(dir, arch string) error {
	name := "dist/studio-agent-linux-" + arch
	data, err := dist.ReadFile(name)
	if err != nil {
		if os.Getenv("STUDIO_AGENT_BIN") != "" {
			data, err = os.ReadFile(os.Getenv("STUDIO_AGENT_BIN"))
		}
		if err != nil {
			return fmt.Errorf("guest agent for linux/%s not embedded; run `make agent` before building: %w", arch, err)
		}
	}
	dst := filepath.Join(dir, "bin", "studio-agent")
	if cur, err := os.ReadFile(dst); err == nil && string(cur) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Embedded lists the embedded agent binaries.
func Embedded() []string {
	entries, _ := fs.ReadDir(dist, "dist")
	var out []string
	for _, e := range entries {
		if e.Name() != ".keep" {
			out = append(out, e.Name())
		}
	}
	return out
}
