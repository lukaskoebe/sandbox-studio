package agentchan

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Run runs one command in the system VM id and returns what it printed (agentproto.KindRun).
func (h *Hub) Run(ctx context.Context, id string, req agentproto.RunRequest) (agentproto.RunResult, error) {
	var out agentproto.RunResult
	err := h.requestLimit(ctx, id, agentproto.Header{Kind: agentproto.KindRun}, req, &out, agentproto.MaxRunLine)
	return out, err
}
