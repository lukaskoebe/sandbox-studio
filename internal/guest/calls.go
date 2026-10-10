//go:build linux

package guest

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// callRelayTimeout bounds one relayed call; the callers give up sooner.
const callRelayTimeout = 2 * time.Minute

// maxPendingCalls bounds the relayed calls in flight.
const maxPendingCalls = 16

// setSession records the live host session that calls are relayed on.
func (a *Agent) setSession(s *yamux.Session) {
	a.sessMu.Lock()
	a.sess = s
	a.sessMu.Unlock()
}

func (a *Agent) session() *yamux.Session {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	return a.sess
}

// serveCalls relays the calls of `studio-agent hook` and `studio-agent mcp`, which run as
// the agent user inside the harness, to Studio. They reach the host only through this
// daemon's channel, so Studio knows the sandbox without trusting anything they send.
func (a *Agent) serveCalls(ctx context.Context) error {
	_ = os.Remove(agentproto.CallSocket)
	ln, err := net.Listen("unix", agentproto.CallSocket)
	if err != nil {
		return err
	}
	// Everything in the sandbox acts for the same persona; the agent user must connect.
	if err := os.Chmod(agentproto.CallSocket, 0o666); err != nil {
		ln.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	slots := make(chan struct{}, maxPendingCalls)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				a.relayCall(c)
			}()
		default:
			agentproto.WriteJSONLine(c, agentproto.CallReply{Error: "too many pending calls"})
			c.Close()
		}
	}
}

func (a *Agent) relayCall(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(callRelayTimeout))
	reply := func(r agentproto.CallReply) { agentproto.WriteJSONLine(c, r) }
	var call agentproto.Call
	if err := agentproto.ReadJSONLineLimit(bufio.NewReader(c), &call, agentproto.MaxCallLine); err != nil {
		reply(agentproto.CallReply{Error: "bad call: " + err.Error()})
		return
	}
	sess := a.session()
	if sess == nil || sess.IsClosed() {
		reply(agentproto.CallReply{Error: "not connected to Studio"})
		return
	}
	st, err := sess.Open()
	if err != nil {
		reply(agentproto.CallReply{Error: "not connected to Studio"})
		return
	}
	defer st.Close()
	st.SetDeadline(time.Now().Add(callRelayTimeout))
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindCall}); err != nil {
		reply(agentproto.CallReply{Error: err.Error()})
		return
	}
	if err := agentproto.WriteJSONLine(st, call); err != nil {
		reply(agentproto.CallReply{Error: err.Error()})
		return
	}
	var r agentproto.CallReply
	if err := agentproto.ReadJSONLineLimit(bufio.NewReader(st), &r, agentproto.MaxCallLine); err != nil {
		if errors.Is(err, agentproto.ErrLineTooLong) {
			err = errors.New("reply too large")
		}
		reply(agentproto.CallReply{Error: "Studio did not answer: " + err.Error()})
		return
	}
	reply(r)
}
