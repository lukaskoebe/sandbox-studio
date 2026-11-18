package chat

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestImageSources(t *testing.T) {
	text := "Before ![the login page](/tmp/login.png) and after ![](<shots/after login.png> \"title\").\n" +
		"Again ![x](/tmp/login.png), a [link](/tmp/a.png) and ![remote](https://example.com/a.png)."
	got := ImageSources(text)
	if !slices.Equal(got, []string{"/tmp/login.png", "shots/after login.png", "https://example.com/a.png"}) {
		t.Fatalf("sources %q", got)
	}
	for src, want := range map[string]string{
		"/tmp/login.png":        "/tmp/login.png",
		"shots/../a.png":        "/workspace/a.png",
		"file:///tmp/a.png":     "/tmp/a.png",
		"https://example.com/a": "",
		"data:image/png;base64": "",
	} {
		if got := imagePath(src); got != want {
			t.Errorf("imagePath(%q) = %q, want %q", src, got, want)
		}
	}
}

// png is a picture of size bytes: PNG's signature, then padding.
func png(size int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, size-8)...)
}

// A picture an agent's message shows is copied out of the sandbox once the message is written,
// in parts when it is larger than one reply, and served from the copy after the file is gone.
func TestImagesAreCopiedOnceTheMessageIsWritten(t *testing.T) {
	f := newFixture(t, &acptest.Agent{Turns: []acptest.Turn{{
		Steps: steps(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done. ![the login page](/tmp/login.png)"}}`,
			`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" and ![a note](notes.txt) ![gone](/tmp/gone.png)"}}`),
	}}})
	f.s.Images = t.TempDir()
	big := png(3*agentproto.MaxRangeBytes + 5)
	f.g.pics = map[string][]byte{"/tmp/login.png": big, "/workspace/notes.txt": []byte("just words")}
	turn := f.send(t, "Show me")
	_, log := f.wait(t, turn.ID)
	item := log.Items[1]
	ctx := context.Background()
	var img store.ChatImage
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var err error
		img, err = f.s.Store.ChatImage(ctx, item.ID, "/tmp/gone.png")
		if err == nil || time.Now().After(deadline) {
			break
		}
	}
	if img.File != "" || img.Error != "/tmp/gone.png isn't there." {
		t.Fatalf("missing picture %+v", img)
	}
	if img, err := f.s.Store.ChatImage(ctx, item.ID, "notes.txt"); err != nil || !strings.Contains(img.Error, "isn't a PNG") {
		t.Fatalf("not a picture %+v %v", img, err)
	}
	f.g.mu.Lock()
	delete(f.g.pics, "/tmp/login.png")
	f.g.mu.Unlock()
	file, mimeType, err := f.s.Image(ctx, f.sb.EnvironmentID, f.chat.ID, item.ID, "/tmp/login.png")
	if err != nil || mimeType != "image/png" {
		t.Fatalf("image %q %q %v", file, mimeType, err)
	}
	if b, err := os.ReadFile(file); err != nil || !bytes.Equal(b, big) {
		t.Fatalf("copy of %d bytes, want %d: %v", len(b), len(big), err)
	}
	// What the message doesn't show isn't served, and a picture that wasn't copied says why,
	// trying again: here it is there now.
	if _, _, err := f.s.Image(ctx, f.sb.EnvironmentID, f.chat.ID, item.ID, "/etc/passwd"); err != store.ErrNotFound {
		t.Fatalf("a path the message doesn't show: %v", err)
	}
	if _, _, err := f.s.Image(ctx, f.sb.EnvironmentID, f.chat.ID, item.ID, "notes.txt"); !IsImageError(err) {
		t.Fatalf("not a picture: %v", err)
	}
	f.g.mu.Lock()
	f.g.pics["/tmp/gone.png"] = png(100)
	f.g.mu.Unlock()
	if _, _, err := f.s.Image(ctx, f.sb.EnvironmentID, f.chat.ID, item.ID, "/tmp/gone.png"); err != nil {
		t.Fatalf("a picture that came back: %v", err)
	}
	// A restart removes pictures no chat keeps.
	stray := f.s.Images + "/stray.png"
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.s.sweepImages(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("stray picture kept: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("kept picture removed: %v", err)
	}
}

// A guest from before range reads sends a small picture whole, and a larger one never: it
// would wait on the stream window.
func TestImagesFromAnOlderGuest(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	f.s.Images = t.TempDir()
	f.g.older = true
	f.g.pics = map[string][]byte{"/tmp/small.png": png(1000), "/tmp/big.png": png(smallRead + 1)}
	ctx := context.Background()
	if data, mimeType, err := f.s.readImage(ctx, f.sb.ID, "/tmp/small.png"); err != nil || len(data) != 1000 || mimeType != "image/png" {
		t.Fatalf("small %d %q %v", len(data), mimeType, err)
	}
	if _, _, err := f.s.readImage(ctx, f.sb.ID, "/tmp/big.png"); !IsImageError(err) || !strings.Contains(err.Error(), "Restart the computer") {
		t.Fatalf("big %v", err)
	}
}
