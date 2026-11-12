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

// ApprovalStatusTool says what became of one of the sandbox's approvals; internal/sandboxinfo
// serves it, and `studio wait` asks it until the approval is settled.
var ApprovalStatusTool = Tool{agentproto.MethodApprovalStatus,
	"What became of something you asked the user to approve, by the approval ID an answer with \"awaiting approval\" " +
		"gave: its status (pending, approved, denied, dismissed), whether it is settled, and once it is, the result " +
		"(the pull request opened, the review or comment posted, the push pushed, or failed with the forge's answer). " +
		"Don't ask it in a loop: `studio wait <id>` waits for you.",
	obj([]string{"approval"}, map[string]any{
		"approval": str("The approval's ID", "maxLength", 100),
	})}

func init() {
	Tools = append(Tools, SandboxInfoTool, ApprovalStatusTool)
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
