package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// diskAsk is the longest a failed turn waits to hear how full the sandbox's disks are.
const diskAsk = 3 * time.Second

// diskFull says which of the sandbox's disks is full, and what to do, as a sentence for a
// failed turn; "" when none is, or the guest can't say. An agent on a full disk can't
// write its session, and fails with an error of its own that doesn't say why.
func (s *Service) diskFull(ctx context.Context, sandboxID string) string {
	ctx, cancel := context.WithTimeout(ctx, diskAsk)
	defer cancel()
	disks, err := s.Guest.Disks(ctx, sandboxID)
	if err != nil {
		return ""
	}
	for _, d := range disks {
		if !d.Full() {
			continue
		}
		free := fmt.Sprintf("%s free of %s", sizeText(d.Free), sizeText(d.Total))
		if d.Name == agentproto.DiskHome {
			return "The sandbox's home disk is full (" + free + "), so the agent can't save its work. " +
				"Free some space in the terminal, such as with rm -rf ~/.cache ~/.npm, then send the message again."
		}
		return "The sandbox's workspace disk is full (" + free + "). " +
			"Free some space in /workspace from the terminal, then send the message again."
	}
	return ""
}

// sizeText is n bytes in MiB or GiB, as a disk's free space reads.
func sizeText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MiB", n>>20)
}
