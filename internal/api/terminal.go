package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// attachTerminal bridges a websocket to a tmux session in the guest.
//
// Binary messages carry terminal bytes in both directions. The client sends text messages
// for control: {"type":"resize","cols":N,"rows":N}. When the shell exits the server closes
// with status 1000 and reason "exited".
func (s *Server) attachTerminal(w http.ResponseWriter, r *http.Request) {
	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	pty, err := s.Sandboxes.OpenTerminal(r.Context(), r.PathValue("env"), r.PathValue("id"), r.PathValue("name"), uint16(cols), uint16(rows))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	case errors.Is(err, agentchan.ErrNotConnected):
		http.Error(w, "the sandbox is not running or still booting", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer pty.Close()

	// Accept verifies that Origin matches Host, so other sites can't attach.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
			if errors.Is(err, io.EOF) {
				c.Close(websocket.StatusNormalClosure, "exited")
				return
			}
			if err != nil {
				c.Close(websocket.StatusInternalError, "terminal channel lost")
				return
			}
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := pty.Write(data); err != nil {
				return
			}
			continue
		}
		var msg struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
			pty.Resize(msg.Cols, msg.Rows)
		}
	}
}
