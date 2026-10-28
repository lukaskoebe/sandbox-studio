package runtime

import (
	"context"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// SSHKeys are the files an SSH server for a VM authenticates with. Studio's own keep a
// sandbox's host key the same across its VM generations, which msb's per-VM key doesn't.
type SSHKeys struct {
	HostKey        string // the server's private key
	AuthorizedKeys string // the client keys it accepts
}

// ServeSSH serves one SSH connection to VM name over this process's stdin and stdout, as a
// ProxyCommand does, until the client disconnects. The session's user is the one the client
// logs in as; SFTP is on, and so are local and dynamic port forwards to the VM's ports.
func ServeSSH(ctx context.Context, name string, keys SSHKeys) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	sb, err := h.Connect(ctx)
	if err != nil {
		return err
	}
	// Detach, not Close, which would stop the VM.
	defer sb.Detach(context.WithoutCancel(ctx))
	srv, err := sb.SSH().PrepareServer(ctx,
		msb.WithSSHHostKeyPath(keys.HostKey),
		msb.WithSSHAuthorizedKeysPath(keys.AuthorizedKeys),
		msb.WithSSHServerSFTP(true),
		msb.WithSSHServerInactivityTimeout(0),
	)
	if err != nil {
		return err
	}
	defer srv.Close(context.WithoutCancel(ctx))
	return srv.ServeConnection(ctx)
}
