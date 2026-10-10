package agentchan

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Browser runs one agent-browser command in the browser VM id (agentproto.KindBrowser).
func (h *Hub) Browser(ctx context.Context, id string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	var out agentproto.BrowserResult
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindBrowser}, cmd, &out)
	return out, err
}
