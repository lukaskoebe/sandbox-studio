package gateway

import (
	"io"
	"testing"
	"time"
)

func TestMaskerStreamsWithoutWaitingForEOF(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	m := &masker{r: r, values: [][]byte{[]byte("a-much-longer-secret-than-the-first-event")}}
	const event = "data: ready\n\n"
	go func() { _, _ = io.WriteString(w, event) }()
	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, len(event))
		_, err := io.ReadFull(m, buf)
		done <- result{string(buf), err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.body != event {
			t.Fatalf("first event: %q, %v", got.body, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("masker held a harmless event while the upstream was still open")
	}
}
