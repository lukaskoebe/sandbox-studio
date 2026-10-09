package agentchan

import (
	"errors"
	"io"
	"net"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestPTYReadTransportEOFIsUnexpectedEOF(t *testing.T) {
	st, peer := net.Pipe()
	p := &PTY{st: st, exit: make(chan string, 1)}
	defer p.Close()
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := p.Read(make([]byte, 1))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("bare transport EOF should be retryable, got %v", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("bare transport EOF must not look like a shell exit: %v", err)
	}
}

func TestPTYReadExplicitExitFrameReturnsEOF(t *testing.T) {
	st, peer := net.Pipe()
	p := &PTY{st: st, exit: make(chan string, 1)}
	defer p.Close()
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- agentproto.WriteFrame(peer, agentproto.FrameExit, []byte("0"))
		_ = peer.Close()
	}()

	_, err := p.Read(make([]byte, 1))
	if err != io.EOF {
		t.Fatalf("explicit exit frame should return EOF, got %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-p.exit:
		if code != "0" {
			t.Fatalf("exit code %q, want 0", code)
		}
	default:
		t.Fatal("exit frame did not record its code")
	}
}

func TestPTYReadPartialFramePreservesUnexpectedEOF(t *testing.T) {
	st, peer := net.Pipe()
	p := &PTY{st: st, exit: make(chan string, 1)}
	defer p.Close()
	writeErr := make(chan error, 1)
	go func() {
		_, err := peer.Write([]byte{agentproto.FrameData, 0})
		if err == nil {
			err = peer.Close()
		} else {
			_ = peer.Close()
		}
		writeErr <- err
	}()

	_, err := p.Read(make([]byte, 1))
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("partial frame error should be preserved, got %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
}
