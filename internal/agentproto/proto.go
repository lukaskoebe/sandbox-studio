// Package agentproto defines the wire protocol between Studio and the guest agent.
//
// The guest agent dials the host over vsock and runs a yamux session on that
// connection; either side may open streams. Every stream starts with one JSON
// header line naming the stream kind. What follows depends on the kind:
//
//   - KindHello (guest → host): the guest writes a Hello line, then closes.
//   - KindPTY (host → guest): framed in both directions (see WriteFrame).
//   - KindTCP (host → guest): raw bytes to a guest loopback port.
//   - KindSessions, KindPorts, KindKill (host → guest): one JSON reply line.
//   - KindConfig (host → guest): the host writes a Config line; the guest applies it and
//     replies like KindKill: {} or an Error.
package agentproto

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// VsockPort is the guest-side vsock port routed to the host agent socket.
const VsockPort = 5000

// Stream kinds.
const (
	KindHello    = "hello"
	KindPTY      = "pty"
	KindTCP      = "tcp"
	KindSessions = "sessions"
	KindPorts    = "ports"
	KindKill     = "kill" // ends the tmux session named in Header.Session
	KindConfig   = "config"
)

// Header opens every stream.
type Header struct {
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"` // KindPTY, KindKill: tmux session name
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
	Port    int    `json:"port,omitempty"` // KindTCP: guest loopback port
}

// Config is what a sandbox gets from its environment. The host sends it whenever the guest
// connects and whenever it changes.
type Config struct {
	CA  string            `json:"ca"`  // PEM certificate of the environment's CA, to trust
	Env map[string]string `json:"env"` // secret names and their placeholders, for new shells
}

// Hello is sent by the guest when its session starts.
type Hello struct {
	Version  string `json:"version"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
}

// Session describes a tmux session in the guest.
type Session struct {
	Name     string `json:"name"`
	Attached int    `json:"attached"`
	Windows  int    `json:"windows"`
	Created  int64  `json:"created"` // unix seconds
}

// Port is a TCP port listening in the guest.
type Port struct {
	Port int `json:"port"`
}

// Error is the reply when a request fails.
type Error struct {
	Error string `json:"error"`
}

// WriteJSONLine writes v as one line of JSON.
func WriteJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadJSONLine reads one line of JSON into v.
func ReadJSONLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

// PTY frame types.
const (
	FrameData   byte = 0
	FrameResize byte = 1
	FrameExit   byte = 2 // guest → host: the shell exited; payload is the exit code as text
)

// MaxFrame bounds a single frame payload.
const MaxFrame = 1 << 20

// Resize is the payload of a FrameResize frame.
type Resize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// WriteFrame writes [type u8][length u32 BE][payload].
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame too large: %d bytes", len(payload))
	}
	hdr := make([]byte, 5, 5+len(payload))
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	_, err := w.Write(append(hdr, payload...))
	return err
}

// ReadFrame reads one frame written by WriteFrame.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, errors.New("frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
