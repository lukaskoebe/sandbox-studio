//go:build !linux

package agentcall

import (
	"context"
	"os"
)

// lockFile is a no-op off Linux; the guest agent only runs in Linux sandboxes.
func lockFile(context.Context, *os.File) error { return nil }
