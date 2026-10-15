package browser

import (
	"context"
	"errors"
	"net"
	"slices"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrNoForward refuses a forwarded connection that leads nowhere: the VM is no browser VM,
// no agent sandbox drove it, or the port was not one the driver listened on.
var ErrNoForward = errors.New("nothing is forwarded to this port")

// forward makes localhost in the browser VM reach sb, the agent sandbox that drives it: the
// VM listens on loopback at the ports sb listens on, and DialForward connects them to sb.
// It runs before each of sb's calls, so a dev server started since is there by the next
// one. A failure only leaves localhost as it was, so it is logged and the call goes on.
func (s *Service) forward(ctx context.Context, st *state, sb store.Sandbox) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listening, err := s.Runner.Ports(ctx, sb.ID)
	if err != nil {
		s.log().Debug("listing the driver's ports failed", "sandbox", sb.ID, "err", err)
		return
	}
	var ports []int
	for _, p := range listening {
		if !agentproto.BrowserReservedPort(p.Port) && !slices.Contains(ports, p.Port) && len(ports) < agentproto.MaxForwards {
			ports = append(ports, p.Port)
		}
	}
	slices.Sort(ports)
	s.mu.Lock()
	vm := st.vm
	// The listeners follow the ports at once; a connection follows the driver it was
	// forwarded for, so another sandbox's port never opens in this one.
	st.fwdFrom, st.fwdPorts = sb.ID, ports
	s.mu.Unlock()
	if err := s.Runner.Forwards(ctx, vm, ports); err != nil {
		s.log().Debug("forwarding the driver's ports failed", "sandbox", sb.ID, "err", err)
	}
}

// DialForward is agentchan.Hub.OnForward: it connects a browser VM's forwarded loopback
// port to the same port in the agent sandbox whose ports the VM forwards. vmID is the VM
// the stream came from, the only identity trusted.
func (s *Service) DialForward(ctx context.Context, vmID string, port int) (net.Conn, error) {
	vm, err := s.Store.LookupSandbox(ctx, vmID)
	if err != nil || vm.Kind != store.SandboxKindBrowser {
		return nil, ErrNoForward
	}
	s.mu.Lock()
	var from string
	if st := s.states[vm.TeammateID]; st != nil && st.env == vm.EnvironmentID && st.vm == vm.ID &&
		slices.Contains(st.fwdPorts, port) {
		from = st.fwdFrom
	}
	s.mu.Unlock()
	if from == "" {
		return nil, ErrNoForward
	}
	// The driver may have been deleted or moved since.
	sb, err := s.Store.LookupSandbox(ctx, from)
	if err != nil || sb.Kind != "" || sb.BuildJobID != "" ||
		sb.EnvironmentID != vm.EnvironmentID || sb.TeammateID != vm.TeammateID {
		return nil, ErrNoForward
	}
	return s.Runner.DialTCP(ctx, sb.ID, port)
}
