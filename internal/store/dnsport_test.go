package store

import (
	"context"
	"net"
	"strconv"
	"testing"
)

// A port something else on the host listens on, such as another Studio's resolver, is skipped.
func TestDNSPortSkipsPortsInUseOnTheHost(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	create := func() int {
		t.Helper()
		sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "sb-" + NewID(), CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
		if err != nil {
			t.Fatal(err)
		}
		return sb.DNSPort
	}
	first := create()
	next := first + 1
	for !portFree(next) {
		next++
	}
	held, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(next)))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if got := create(); got == next || got <= first {
		t.Fatalf("second sandbox got port %d; first has %d and %d is held", got, first, next)
	}
}
