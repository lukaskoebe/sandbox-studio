package agentproto

import (
	"bufio"
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameData, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, FrameResize, []byte(`{"cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	typ, p, err := ReadFrame(&buf)
	if err != nil || typ != FrameData || string(p) != "hello" {
		t.Fatalf("got %d %q %v", typ, p, err)
	}
	typ, p, err = ReadFrame(&buf)
	if err != nil || typ != FrameResize || string(p) != `{"cols":80,"rows":24}` {
		t.Fatalf("got %d %q %v", typ, p, err)
	}
	if _, _, err := ReadFrame(&buf); err == nil {
		t.Fatal("expected EOF")
	}
}

func TestJSONLine(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSONLine(&buf, Header{Kind: KindPTY, Session: "main", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	var h Header
	if err := ReadJSONLine(bufio.NewReader(&buf), &h); err != nil || h.Session != "main" || h.Cols != 80 {
		t.Fatalf("got %+v %v", h, err)
	}
}

func TestFrameTooLarge(t *testing.T) {
	if err := WriteFrame(&bytes.Buffer{}, FrameData, make([]byte, MaxFrame+1)); err == nil {
		t.Fatal("expected error")
	}
}
