//go:build linux

package guest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// devToolsActivePort is the file Chromium writes into its profile when its debugging server
// listens: the port on the first line, the browser target's path on the second. agent-browser
// starts Chromium with --remote-debugging-port=0 and removes a stale file before each launch.
const devToolsActivePort = "DevToolsActivePort"

// devToolsPort answers [BrowserDevToolsPort] for the session whose profile is profile, in
// agent-browser's JSON envelope, from the file Chromium wrote. It reads no more than that: the
// host dials the port itself, as it dials the live view's.
func devToolsPort(req agentproto.BrowserCommand, profile string) (json.RawMessage, error) {
	if len(req.Args) != 1 || req.Stdin != "" || req.Fresh || req.Screenshot {
		return nil, errors.New("devtools-port takes no arguments")
	}
	b, err := os.ReadFile(filepath.Join(profile, devToolsActivePort))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("the browser has not started")
	}
	if err != nil {
		return nil, err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("the browser's debugging port can't be read")
	}
	return json.Marshal(map[string]any{"success": true, "data": map[string]int{"port": port}, "error": nil})
}
