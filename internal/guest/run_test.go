package guest

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestCheckRun(t *testing.T) {
	ok := agentproto.RunRequest{Args: []string{"claude", "-p"}, Env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "studio-x"}, TimeoutSec: 60}
	if err := checkRun(ok); err != nil {
		t.Fatalf("a plain run: %v", err)
	}
	long := strings.Repeat("a", agentproto.MaxRunArg+1)
	for name, req := range map[string]agentproto.RunRequest{
		"no program":       {TimeoutSec: 1},
		"program outside":  {Args: []string{"sh", "-c", "id"}, TimeoutSec: 1},
		"a path":           {Args: []string{"/bin/cat"}, TimeoutSec: 1},
		"too many args":    {Args: append([]string{"cat"}, make([]string, agentproto.MaxRunArgs)...), TimeoutSec: 1},
		"long arg":         {Args: []string{"cat", long}, TimeoutSec: 1},
		"NUL in arg":       {Args: []string{"cat", "a\x00b"}, TimeoutSec: 1},
		"no timeout":       {Args: []string{"cat"}},
		"timeout too long": {Args: []string{"cat"}, TimeoutSec: agentproto.MaxRunTimeout + 1},
		"bad env name":     {Args: []string{"cat"}, TimeoutSec: 1, Env: map[string]string{"lower": "x"}},
		"PATH":             {Args: []string{"cat"}, TimeoutSec: 1, Env: map[string]string{"PATH": "/tmp"}},
		"LD_ preload":      {Args: []string{"cat"}, TimeoutSec: 1, Env: map[string]string{"LD_PRELOAD": "/tmp/x.so"}},
		"empty env value":  {Args: []string{"cat"}, TimeoutSec: 1, Env: map[string]string{"X": ""}},
		"big stdin":        {Args: []string{"cat"}, TimeoutSec: 1, Stdin: strings.Repeat("a", agentproto.MaxRunStdin+1)},
	} {
		if err := checkRun(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// execRun returns what the command printed, its exit code, and cuts output at the limit.
func TestExecRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := agentproto.RunRequest{Args: []string{"cat"}, Stdin: "hello", TimeoutSec: 5}
	res, err := execRun(ctx, exec.Command("cat"), req)
	if err != nil || res.Stdout != "hello" || res.ExitCode != 0 || res.Truncated {
		t.Fatalf("cat: %+v, %v", res, err)
	}
	res, err = execRun(ctx, exec.Command("sh", "-c", "echo oops >&2; exit 3"), agentproto.RunRequest{Args: []string{"sh"}, TimeoutSec: 5})
	if err != nil || res.ExitCode != 3 || res.Stderr != "oops\n" {
		t.Fatalf("exit status: %+v, %v", res, err)
	}
	res, err = execRun(ctx, exec.Command("head", "-c", strconv.Itoa(agentproto.MaxRunOutput+10), "/dev/zero"), agentproto.RunRequest{Args: []string{"head"}, TimeoutSec: 5})
	if err != nil || len(res.Stdout) != agentproto.MaxRunOutput || !res.Truncated {
		t.Fatalf("output cap: %d bytes, truncated %v, %v", len(res.Stdout), res.Truncated, err)
	}
}

func TestExecRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := execRun(ctx, exec.Command("sleep", "30"), agentproto.RunRequest{Args: []string{"sleep"}, TimeoutSec: 1}); err == nil {
		t.Fatal("a command past its timeout returned no error")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the command was not killed: %v", time.Since(start))
	}
}
