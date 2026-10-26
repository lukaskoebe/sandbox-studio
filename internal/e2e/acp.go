//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The scripted agent's texts, which the Playwright suite looks for.
const (
	ACPGreeting = "Hello from the scripted agent. I need to run the tests first."
	ACPTool     = "Run make test"
	ACPDone     = "The tests passed."
	// ACPTable follows ACPDone in the same message: a markdown table, with a pipe in a code span.
	ACPTable = "\n\n| Package | Result |\n| --- | :---: |\n| `./... | tee` | ok |\n"
	// ACPPreview ends the message with a link to a server in the sandbox, as agents write them.
	ACPPreview = "\nThe app runs at http://localhost:5173/login."
)

// acpTurns is the script of every prompt: the agent thinks a moment, answers, asks to run a
// command, runs it once it gets an answer, and says so. Each turn has its own tool call ID.
func acpTurns(n int) []acptest.Turn {
	raw := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return b
	}
	text := func(s string) json.RawMessage {
		return raw(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": s}})
	}
	turns := make([]acptest.Turn, n)
	for i := range turns {
		id := fmt.Sprintf("e2e-call-%d", i+1)
		turns[i] = acptest.Turn{Steps: []acptest.Step{
			// Long enough for the chat to show the agent starting.
			{Pause: 300 * time.Millisecond},
			{Update: text(ACPGreeting)},
			{Update: raw(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": ACPTool, "kind": "execute", "status": "pending"})},
			{Permission: raw(map[string]any{
				"toolCall": map[string]any{"toolCallId": id, "title": ACPTool, "kind": "execute"},
				"options": []map[string]string{
					{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
					{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
				},
			})},
			{Update: raw(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "completed"})},
			{Update: text(ACPDone)},
			{Update: text(ACPTable)},
			{Update: text(ACPPreview)},
		}}
	}
	return turns
}

// ACPAgent is what the control API reports about one chat's scripted agent.
type ACPAgent struct {
	Chat     string            `json:"chat"`
	Harness  string            `json:"harness"`
	Prompts  []json.RawMessage `json:"prompts"`
	Outcomes []json.RawMessage `json:"outcomes"`
}

// acpInitWithoutImages is acptest.DefaultInit for an adapter that takes no images.
var acpInitWithoutImages = json.RawMessage(strings.Replace(string(acptest.DefaultInit), `"image":true`, `"image":false`, 1))

type acpProcess struct {
	start agentproto.StartACP
	agent *acptest.Agent
}

// serveACP answers an acp stream with a scripted ACP agent (acptest) in place of the
// harness's adapter: it acks the start like studio-agent does, then speaks ACP on the
// stream until the host closes it.
func (g *Guest) serveACP(st net.Conn, br *bufio.Reader) error {
	var req agentproto.StartACP
	if err := agentproto.ReadJSONLine(br, &req); err != nil {
		return err
	}
	a := &acptest.Agent{SessionID: "e2e-session-" + req.Chat, Turns: acpTurns(20)}
	g.mu.Lock()
	if g.noImages {
		a.Init = acpInitWithoutImages
	}
	g.acp = append(g.acp, &acpProcess{start: req, agent: a})
	g.mu.Unlock()
	if err := agentproto.WriteJSONLine(st, struct{}{}); err != nil {
		return err
	}
	a.Serve(&bufferedConn{Conn: st, r: br})
	return nil
}

// bufferedConn reads through the reader that held the start line.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// acpState reports the scripted agents of the guest, oldest first.
func (g *Guest) acpState() []ACPAgent {
	g.mu.Lock()
	procs := append([]*acpProcess(nil), g.acp...)
	g.mu.Unlock()
	out := []ACPAgent{}
	for _, p := range procs {
		prompts, outcomes, _ := p.agent.Snapshot()
		out = append(out, ACPAgent{Chat: p.start.Chat, Harness: p.start.Harness, Prompts: orEmpty(prompts), Outcomes: orEmpty(outcomes)})
	}
	return out
}

func orEmpty(v []json.RawMessage) []json.RawMessage {
	if v == nil {
		return []json.RawMessage{}
	}
	return v
}
