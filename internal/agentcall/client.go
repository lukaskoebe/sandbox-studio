// Package agentcall is the guest side of memory in agent sessions (PLAN §6.7): the
// `studio-agent hook` and `studio-agent mcp` commands that harnesses run as the agent
// user. Both reach Studio through the connect daemon's call socket, so Studio learns the
// sandbox from the channel and never from what they send.
package agentcall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Caller makes one call to Studio and decodes its result into result (when not nil).
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// SocketCaller calls through the connect daemon's Unix socket.
type SocketCaller struct {
	Path string // defaults to agentproto.CallSocket
}

// Call sends one call and waits for the reply or ctx.
func (c SocketCaller) Call(ctx context.Context, method string, params, result any) error {
	path := c.Path
	if path == "" {
		path = agentproto.CallSocket
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := agentproto.WriteJSONLine(conn, agentproto.Call{Method: method, Params: raw}); err != nil {
		return err
	}
	var reply agentproto.CallReply
	if err := agentproto.ReadJSONLineLimit(bufio.NewReader(conn), &reply, agentproto.MaxCallLine); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	if result != nil && len(reply.Result) > 0 {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}
