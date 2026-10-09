package templatebuild

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

const (
	exportIdleTimeout = 60 * time.Second
	exportMinRate     = 2_000_000 // bytes per second at the export byte limit
	exportStartup     = 2 * time.Minute
)

var (
	errExportIdle    = fmt.Errorf("export sent no data for %s", exportIdleTimeout)
	errExportTooLong = errors.New("export exceeded its time limit")
)

// exportCommand streams the guest's framed root-layer export on stdout. The
// guest may hold its root frozen for the whole export.
func exportCommand(timeout time.Duration) runtime.RunCommand {
	return runtime.RunCommand{
		Path: runtime.AgentPath, Args: []string{"export-layer", "--max-freeze=" + timeout.String()},
		User: "root", Cwd: "/", Timeout: timeout,
	}
}

// exportTimeout allows the export byte limit at exportMinRate, up to the
// guest's freeze cap.
func exportTimeout(limits ocilayer.Limits) time.Duration {
	maxBytes := limits.MaxInputBytes
	if maxBytes <= 0 {
		maxBytes = ocilayer.DefaultLimits().MaxInputBytes
	}
	return min(exportStartup+time.Duration(maxBytes/exportMinRate)*time.Second, agentproto.ExportMaxFreeze)
}

// export runs export-layer in the owned build VM over msb exec and validates
// its stdout with templateexport.Receive. Stderr goes to the build log.
func (w *Worker) export(ctx context.Context, owned runtime.OwnedVM, dir string, log *buildLog) (templateexport.Layer, error) {
	timeout := exportTimeout(w.ExportLimits)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopTimer := time.AfterFunc(timeout, func() { cancel(errExportTooLong) })
	defer stopTimer.Stop()

	reader, writer := io.Pipe()
	stdout := &idleWriter{w: writer, timer: time.AfterFunc(exportIdleTimeout, func() { cancel(errExportIdle) })}
	defer stdout.timer.Stop()
	command := exportCommand(timeout)
	command.Stdout = stdout

	type runOutcome struct {
		result runtime.RunResult
		err    error
	}
	ran := make(chan runOutcome, 1)
	go func() {
		result, err := w.Runtime.Run(ctx, owned, command, log.output)
		// A stream that ends before its completion trailer is a Receive error.
		_ = writer.CloseWithError(err)
		ran <- runOutcome{result: result, err: err}
	}()
	layer, err := templateexport.Receive(ctx, dir, reader, w.ExportLimits)
	// Unblock a Run still writing stdout, whether Receive failed or stopped at
	// the trailer.
	_ = reader.CloseWithError(errors.New("export receiver stopped"))
	if err != nil {
		cancel(err)
	}
	run := <-ran
	if run.result.OutputDropped {
		log.truncated.Store(true)
	}
	switch {
	case err != nil:
	case run.err != nil || run.result.CleanupPending:
		err = errors.Join(run.err, errors.New("export command did not complete cleanly"))
	case !run.result.ExitCodeKnown || run.result.ExitCode != 0:
		err = fmt.Errorf("export command exited with status %d (known: %t)", run.result.ExitCode, run.result.ExitCodeKnown)
	}
	if err != nil {
		if layer.Path != "" {
			_ = os.Remove(layer.Path)
		}
		if cause := context.Cause(ctx); errors.Is(cause, errExportIdle) || errors.Is(cause, errExportTooLong) {
			log.message("Export stopped: " + cause.Error() + "\n")
			err = errors.Join(cause, err)
		}
		return templateexport.Layer{}, err
	}
	return layer, nil
}

// idleWriter restarts the idle timer around every write. Run writes stdout from
// one goroutine.
type idleWriter struct {
	w     io.Writer
	timer *time.Timer
}

func (i *idleWriter) Write(p []byte) (int, error) {
	i.timer.Reset(exportIdleTimeout)
	n, err := i.w.Write(p)
	i.timer.Reset(exportIdleTimeout)
	return n, err
}
