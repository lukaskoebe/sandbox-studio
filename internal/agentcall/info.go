package agentcall

import (
	"context"
	"encoding/json"
	"io"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// SandboxInfoTool describes the sandbox the agent runs in; internal/sandboxinfo serves it.
var SandboxInfoTool = Tool{agentproto.MethodSandboxInfo,
	"This sandbox as Studio sees it: its name and ID, your teammate, the TCP ports listening in it, and its network " +
		"policy, the security level and the rules that apply. Each port has a link to give the user: they can't open the " +
		"sandbox's localhost themselves, but Studio's chat turns the link into a button that opens the port for them. " +
		"Ask again after starting a server; nothing here changes what you may do.",
	obj(nil, map[string]any{})}

func init() {
	Tools = append(Tools, SandboxInfoTool)
}

// RunInfo is `studio-agent info`: it prints what sandbox_info returns, as indented JSON.
func RunInfo(ctx context.Context, c Caller, w io.Writer) error {
	var info json.RawMessage
	if err := c.Call(ctx, agentproto.MethodSandboxInfo, struct{}{}, &info); err != nil {
		return err
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(out, '\n'))
	return err
}
