//go:build e2e

package e2e

import (
	"os"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Egress attaches sandboxes to the real gateway like gateway.Gateway.Attach, but without
// their DNS resolvers: those listen on fixed loopback ports (17100 and up) that another
// Studio on the same machine may hold. Fake guests name hosts in SOCKS5 instead.
type Egress struct{ Gateway *gateway.Gateway }

func (e Egress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	env := gateway.PasswordEnv(sb.ID)
	if err := os.Setenv(env, gateway.Password(e.Gateway.Key, sb.ID)); err != nil {
		return runtime.Egress{}, err
	}
	return runtime.Egress{Proxy: e.Gateway.Addr, User: sb.ID, PasswordEnv: env}, nil
}

func (e Egress) Detach(sandboxID string) {
	os.Unsetenv(gateway.PasswordEnv(sandboxID))
	e.Gateway.Conns.Forget(sandboxID)
}
