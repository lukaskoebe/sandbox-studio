package claudeacp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

// The plugin is what Claude Code loads with --plugin-dir for one session: a manifest named
// studio and a .mcp.json with the session's MCP servers, which Studio sends in session/new
// (its memory and task tools), and the permissions server. Plugin servers start with the
// session and need no approval, unlike a project's .mcp.json. Their tools are named
// mcp__plugin_studio_<server>__<tool>.

var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// writePlugin writes the plugin of a session into dir.
func writePlugin(dir, socket, self string, servers []acp.MCPServer) error {
	mcp := map[string]any{}
	for _, s := range servers {
		if !serverName.MatchString(s.Name) || s.Name == permissionServer {
			return fmt.Errorf("MCP server name %q is not allowed", s.Name)
		}
		if s.Command == "" {
			return fmt.Errorf("MCP server %s has no command", s.Name)
		}
		env := map[string]string{}
		for _, v := range s.Env {
			env[v.Name] = v.Value
		}
		args := s.Args
		if args == nil {
			args = []string{}
		}
		mcp[s.Name] = map[string]any{"command": s.Command, "args": args, "env": env}
	}
	mcp[permissionServer] = map[string]any{
		"command": self,
		"args":    []string{"claude-permission"},
		"env":     map[string]string{SocketEnv: socket},
	}
	manifest := map[string]any{
		"name":        pluginName,
		"description": "Studio's tools and permission prompts for a chat.",
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, ".claude-plugin", "plugin.json"), manifest); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, ".mcp.json"), map[string]any{"mcpServers": mcp})
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
