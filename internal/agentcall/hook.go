package agentcall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// HookDeadline bounds a hook from start to answer. The harness configs allow more
// (harness.hookTimeouts), so a slow Studio costs a few seconds, never the session.
const HookDeadline = 5 * time.Second

// sessionEndDeadline is shorter: Codex allows SessionEnd hooks at most 3 seconds.
const sessionEndDeadline = 2500 * time.Millisecond

// HookOptions configures RunHook. Zero values take the guest's defaults.
type HookOptions struct {
	Caller    Caller
	Home      string // $HOME
	StatePath string // the Tracker's file; default ~/.local/state/studio-agent/transcripts.json
	Now       func() time.Time
	GitRemote func(ctx context.Context, dir string) string
	Deadline  time.Duration
}

func (o *HookOptions) defaults(event string) {
	if o.Caller == nil {
		o.Caller = SocketCaller{}
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.StatePath == "" {
		o.StatePath = filepath.Join(o.Home, ".local", "state", "studio-agent", "transcripts.json")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.GitRemote == nil {
		o.GitRemote = gitRemote
	}
	if o.Deadline == 0 {
		o.Deadline = HookDeadline
		if event == agenthook.SessionEnd {
			o.Deadline = sessionEndDeadline
		}
	}
}

// RunHook handles one `studio-agent hook <event>`: it reads the harness's payload from
// stdin, asks Studio for context or hands it the transcript delta, and writes the answer in
// the harness's format. Whatever fails, it writes the neutral answer in time; the returned
// error is only for the log, and the command exits 0 either way.
func RunHook(ctx context.Context, h agenthook.Format, event string, stdin io.Reader, stdout io.Writer, o HookOptions) error {
	o.defaults(event)
	ctx, cancel := context.WithTimeout(ctx, o.Deadline)
	defer cancel()
	type outcome struct {
		res agenthook.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := hook(ctx, h, event, stdin, o)
		done <- outcome{res, err}
	}()
	var out outcome
	select {
	case out = <-done:
	case <-ctx.Done():
		out.err = fmt.Errorf("gave up after %s", o.Deadline)
	}
	if out.err != nil {
		out.res = agenthook.Result{Event: event}
	}
	_, werr := stdout.Write(h.Render(out.res))
	return errors.Join(out.err, werr)
}

func hook(ctx context.Context, h agenthook.Format, event string, stdin io.Reader, o HookOptions) (agenthook.Result, error) {
	neutral := agenthook.Result{Event: event}
	in, err := io.ReadAll(io.LimitReader(stdin, agenthook.MaxInput+1))
	if err != nil {
		return neutral, err
	}
	ev, err := h.Parse(event, in)
	if err != nil {
		return neutral, err
	}
	if !agenthook.Extracts(event) {
		if event == agenthook.SessionStart && ev.Cwd != "" {
			ev.GitRemote = agenthook.NormalizeRemote(o.GitRemote(ctx, ev.Cwd))
		}
		var res agenthook.Result
		if err := o.Caller.Call(ctx, agentproto.MethodHook, ev, &res); err != nil {
			return neutral, err
		}
		res.Event = event
		return res, nil
	}
	return neutral, sendTranscript(ctx, h, ev, o)
}

// sendTranscript hands the session's transcript since the last accepted send to Studio and
// advances the offset once Studio has it.
func sendTranscript(ctx context.Context, h agenthook.Format, ev agenthook.Event, o HookOptions) error {
	if ev.SessionID == "" {
		return errors.New("the hook names no session")
	}
	t := Tracker{Path: o.StatePath, Now: o.Now}
	return t.update(ctx, h.Name+":"+ev.SessionID, func(e *tracked) (bool, error) {
		var raw int64
		var end int64
		switch {
		case ev.TranscriptPath != "":
			path, err := allowedTranscript(o.Home, h.StateDirs, ev.TranscriptPath)
			if err != nil {
				return false, err
			}
			data, n, skipped, err := fileDelta(path, e.Offset)
			if err != nil {
				return false, err
			}
			raw, end = int64(len(data)), n
			ev.Transcript, ev.TranscriptSkipped = Condense(data), skipped
		case ev.Transcript != "":
			delta, n, skipped := inlineDelta(ev.Transcript, ev.TranscriptOffset, e.Offset)
			raw, end = int64(len(delta)), n
			ev.Transcript, ev.TranscriptSkipped = Condense([]byte(delta)), skipped
		default:
			return false, nil
		}
		ev.TranscriptPath, ev.TranscriptOffset = "", 0
		if ev.Event == agenthook.Stop && (o.Now().Sub(e.LastSent) < stopDebounce || raw < minStopDelta) {
			return false, nil
		}
		if ev.Transcript == "" {
			e.Offset = end // nothing worth sending, e.g. only tool output
			return true, nil
		}
		if err := o.Caller.Call(ctx, agentproto.MethodHook, ev, nil); err != nil {
			return false, err
		}
		e.Offset, e.LastSent = end, o.Now()
		return true, nil
	})
}

// gitRemote is the origin URL of the repository at dir, or "".
func gitRemote(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
