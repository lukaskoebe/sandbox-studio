package gateway

import (
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
)

// Resolvers runs one DNS resolver per sandbox on its loopback port, so the gateway knows
// which names each sandbox looked up.
type Resolvers struct {
	Upstreams []string
	Log       *slog.Logger

	mu      sync.Mutex
	servers map[string]*dnsproxy.Server
}

// Start runs the resolver of a sandbox unless it is already running.
func (r *Resolvers) Start(sandboxID string, port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.servers[sandboxID]; ok {
		return nil
	}
	s, err := dnsproxy.Listen(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), r.Upstreams, r.Log)
	if err != nil {
		return err
	}
	if r.servers == nil {
		r.servers = map[string]*dnsproxy.Server{}
	}
	r.servers[sandboxID] = s
	return nil
}

// Stop ends the resolver of a deleted sandbox.
func (r *Resolvers) Stop(sandboxID string) {
	r.mu.Lock()
	s, ok := r.servers[sandboxID]
	delete(r.servers, sandboxID)
	r.mu.Unlock()
	if ok {
		s.Close()
	}
}

// Names returns what a sandbox's resolver learned, or nil if it isn't running.
func (r *Resolvers) Names(sandboxID string) Names {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.servers[sandboxID]; ok {
		return s.Names
	}
	return nil
}

// Close stops every resolver.
func (r *Resolvers) Close() {
	r.mu.Lock()
	servers := r.servers
	r.servers = nil
	r.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
}
