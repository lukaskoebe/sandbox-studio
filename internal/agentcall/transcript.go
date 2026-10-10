package agentcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// Bounds of what one hook sends to extraction.
const (
	// MaxRawDelta is the most transcript bytes read per hook. A longer delta keeps its
	// most recent part; the rest is counted as skipped.
	MaxRawDelta = 256 << 10
	// MaxCondensed is the most text sent per hook, after condensing to the messages.
	MaxCondensed = 64 << 10
	// Stop fires after every turn; it sends only when this much time passed since the
	// last send and the delta has at least minStopDelta bytes. PreCompact and SessionEnd
	// always send.
	stopDebounce = 5 * time.Minute
	minStopDelta = 2 << 10

	maxTracked = 500
	trackedTTL = 30 * 24 * time.Hour
)

// tracked is the extraction progress of one harness session.
type tracked struct {
	Offset   int64     `json:"offset"`   // bytes of the transcript file, or UTF-16 units of OpenCode's text
	LastSent time.Time `json:"lastSent"` // last delta Studio accepted
	Seen     time.Time `json:"seen"`
}

// Tracker keeps an offset per session in a small JSON file, so each hook sends only what
// is new. Hooks of one sandbox run one at a time per file lock.
type Tracker struct {
	Path string
	Now  func() time.Time
}

// update runs fn on the session's entry under the lock; fn returns whether to save.
func (t Tracker) update(ctx context.Context, key string, fn func(e *tracked) (bool, error)) error {
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(t.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(ctx, f); err != nil {
		return err
	}
	state := map[string]*tracked{}
	if b, err := io.ReadAll(io.LimitReader(f, 1<<20)); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &state) // a broken file starts over
	}
	now := t.now()
	e := state[key]
	if e == nil {
		e = &tracked{}
		state[key] = e
	}
	save, err := fn(e)
	if err != nil || !save {
		return err
	}
	e.Seen = now
	prune(state, now)
	b, _ := json.Marshal(state)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}

func (t Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func prune(state map[string]*tracked, now time.Time) {
	for k, e := range state {
		if now.Sub(e.Seen) > trackedTTL {
			delete(state, k)
		}
	}
	if len(state) <= maxTracked {
		return
	}
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return state[keys[i]].Seen.Before(state[keys[j]].Seen) })
	for _, k := range keys[:len(keys)-maxTracked] {
		delete(state, k)
	}
}

// fileDelta reads the complete lines of path after offset, at most MaxRawDelta bytes of
// them (the most recent). A file shorter than offset was replaced and is read from the start.
func fileDelta(path string, offset int64) (data []byte, end, skipped int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, offset, 0, err
	}
	size := st.Size()
	if size < offset {
		offset = 0
	}
	start := offset
	if size-start > MaxRawDelta {
		start = size - MaxRawDelta
	}
	if size == start {
		return nil, offset, 0, nil
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, offset, 0, err
	}
	if start > offset {
		// Start at a line boundary inside the window.
		var prev [1]byte
		if _, err := f.ReadAt(prev[:], start-1); err == nil && prev[0] != '\n' {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				return nil, offset, 0, nil
			}
			buf, start = buf[i+1:], start+int64(i+1)
		}
		skipped = start - offset
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		return nil, offset, 0, nil // a line still being written
	}
	return buf[:last+1], start + int64(last+1), skipped, nil
}

// inlineDelta is the part of OpenCode's text after stored. text starts at UTF-16 unit
// textOffset of the session's whole text, which is how the plugin counts.
func inlineDelta(text string, textOffset, stored int64) (delta string, end, skipped int64) {
	u := utf16.Encode([]rune(text))
	end = textOffset + int64(len(u))
	if stored > end {
		stored = 0 // a new or shorter session text
	}
	from := stored - textOffset
	if from < 0 {
		skipped, from = -from, 0
	}
	return string(utf16.Decode(u[from:])), end, skipped
}

// allowedTranscript resolves path and checks that it is a regular file inside one of the
// harness's state directories under home. Hooks never read anything else.
func allowedTranscript(home string, stateDirs []string, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("transcript path %q is not absolute", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	for _, d := range stateDirs {
		base, err := filepath.EvalSymlinks(filepath.Join(home, d))
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(base, real); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			st, err := os.Stat(real)
			if err != nil {
				return "", err
			}
			if !st.Mode().IsRegular() {
				return "", fmt.Errorf("transcript %q is not a regular file", path)
			}
			return real, nil
		}
	}
	return "", fmt.Errorf("transcript %q is outside the harness's state directories", path)
}

// injected matches the context Studio added, so it is not extracted again.
var injected = regexp.MustCompile(`(?s)<studio-memory>.*?</studio-memory>`)

// harnessNoise are messages the harnesses add themselves. verify (S7): Codex's
// environment and instruction messages in rollout files of 0.162.1.
var harnessNoise = []string{"<environment_context>", "<user_instructions>", "# AGENTS.md instructions", "<system-reminder>"}

// Condense turns transcript lines into "role: text" lines of the conversation: the user's
// and the assistant's messages, without tool calls, tool output, thinking or Studio's own
// context. JSON lines are read as Claude Code transcripts (message.content) or Codex
// rollouts (payload.content); other lines pass through. The result keeps the most recent
// MaxCondensed bytes.
func Condense(data []byte) string {
	var b strings.Builder
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			writeMessage(&b, "", string(line))
			continue
		}
		role, texts := messageOf(line)
		for _, t := range texts {
			writeMessage(&b, role, t)
		}
	}
	out := b.String()
	if len(out) > MaxCondensed {
		out = out[len(out)-MaxCondensed:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out
}

func writeMessage(b *strings.Builder, role, text string) {
	text = strings.TrimSpace(injected.ReplaceAllString(text, ""))
	if text == "" {
		return
	}
	for _, n := range harnessNoise {
		if strings.HasPrefix(text, n) {
			return
		}
	}
	if role != "" {
		b.WriteString(role + ": ")
	}
	b.WriteString(strings.ReplaceAll(text, "\n", "\n  "))
	b.WriteByte('\n')
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type transcriptLine struct {
	// Claude Code
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// Codex
	Payload *struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
}

func messageOf(line []byte) (string, []string) {
	var l transcriptLine
	if json.Unmarshal(line, &l) != nil {
		return "", nil
	}
	var role string
	var content json.RawMessage
	switch {
	case l.Message != nil && (l.Type == "user" || l.Type == "assistant") && !l.IsMeta && !l.IsSidechain:
		role, content = l.Message.Role, l.Message.Content
	case l.Type == "response_item" && l.Payload != nil && l.Payload.Type == "message":
		role, content = l.Payload.Role, l.Payload.Content
	default:
		return "", nil
	}
	if role != "user" && role != "assistant" {
		return "", nil
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return role, []string{s}
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return "", nil
	}
	var texts []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text", "input_text", "output_text":
			texts = append(texts, bl.Text)
		}
	}
	return role, texts
}
