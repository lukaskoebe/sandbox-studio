package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/lukaskoebe/sandbox-studio/internal/forkmerge"
	"github.com/lukaskoebe/sandbox-studio/internal/guest"
)

// merge runs a merge-* subcommand (docs/design/fork-merge.md). Studio runs them as the agent
// user, so a symlink in the trees can't make them write where that user may not.
func merge(cmd string, args []string, stdin io.Reader, stdout io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("runs as the agent user, not root")
	}
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	forkID := flags.String("fork-id", "", "the fork's ID")
	start := flags.Bool("start", false, "record a scratch sandbox's start instead of a fork's base")
	fork := flags.String("fork", "", "the fork's name")
	ours := flags.String("ours", "", "the name of the sandbox merged into")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg := forkmerge.Config{Workspace: guest.Workdir, Home: guest.Home}
	out := bufio.NewWriterSize(stdout, 1<<20)
	in := bufio.NewReaderSize(stdin, 1<<20)
	var err error
	switch cmd {
	case "merge-base":
		state := forkmerge.StateBase
		if *start {
			state = forkmerge.StateStart
		}
		_, err = forkmerge.RecordBase(cfg, state, *forkID)
	case "merge-export":
		err = forkmerge.ExportChanges(cfg, *forkID, out)
	case "merge-apply":
		var rep *forkmerge.Report
		if rep, err = forkmerge.Apply(cfg, forkmerge.ApplyOptions{Fork: *fork, Ours: *ours}, in); err == nil {
			err = json.NewEncoder(out).Encode(rep)
		}
	case "merge-status":
		var rep *forkmerge.Report
		if rep, err = forkmerge.Status(cfg); err == nil {
			err = json.NewEncoder(out).Encode(rep)
		}
	case "merge-replay-export":
		err = forkmerge.ExportReplay(cfg, out)
	case "merge-replay":
		// A refused replay is an answer, not a failure: it lists what changed here.
		result := forkmerge.ReplayResult{Conflicts: []string{}}
		var conflict *forkmerge.ReplayConflictError
		if err = forkmerge.Replay(cfg, in); errors.As(err, &conflict) {
			result.Conflicts, err = conflict.Paths, nil
		}
		if err == nil {
			err = json.NewEncoder(out).Encode(result)
		}
	default:
		return errors.New("unknown merge command")
	}
	if err != nil {
		return err
	}
	return out.Flush()
}
