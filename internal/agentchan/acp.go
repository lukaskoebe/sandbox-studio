package agentchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// OpenACP starts the ACP agent of a chat in the guest and returns its stdio: writes go to
// the agent's stdin, reads come from its stdout. Closing the stream stops the agent; the
// agent exiting ends reads with io.EOF. A missing adapter is a HarnessError, and a sandbox
// that runs agentproto.MaxACPProcesses agents already is ErrACPFull.
func (h *Hub) OpenACP(ctx context.Context, id string, req agentproto.StartACP) (io.ReadWriteCloser, error) {
	st, err := h.open(ctx, id, agentproto.Header{Kind: agentproto.KindACP})
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		st.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { st.SetDeadline(time.Now()) })
	err = agentproto.WriteJSONLine(st, req)
	var br *bufio.Reader
	var line json.RawMessage
	if err == nil {
		br = bufio.NewReader(st)
		err = agentproto.ReadJSONLineLimit(br, &line, 64<<10)
	}
	stop()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		st.Close()
		return nil, err
	}
	var e agentproto.Error
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		st.Close()
		switch e.Code {
		case agentproto.CodeHarness:
			return nil, HarnessError(e.Error)
		case agentproto.CodeFull:
			return nil, fmt.Errorf("%w: %s", ErrACPFull, e.Error)
		}
		return nil, errors.New(e.Error)
	}
	st.SetDeadline(time.Time{})
	return &acpStream{Conn: st, r: br}, nil
}

// ErrACPFull is OpenACP's error when the sandbox runs as many chat agents as it takes.
var ErrACPFull = errors.New("no room for another chat agent")

// acpStream reads through the reader that held the ack, which may have buffered the
// agent's first bytes.
type acpStream struct {
	net.Conn
	r *bufio.Reader
}

func (s *acpStream) Read(p []byte) (int, error) { return s.r.Read(p) }
