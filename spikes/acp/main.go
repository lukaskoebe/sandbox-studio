//go:build linux

// acp is a disposable live check of chat mode's guest side (docs/design/chat-mode.md): it
// boots one deny-all VM from the local dev base with the production guest agent, starts
// `opencode acp` over KindACP and records its initialize reply. The dev base predates the
// Claude and Codex adapters, so for those it only checks that the guest reports them
// missing. It never sends a prompt, so no model is called.
//
//	S=<scratch>; CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o $S/agent/studio-agent ./cmd/studio-agent
//	PATH=$HOME/.microsandbox/bin:$PATH go run ./spikes/acp -agent $S/agent/studio-agent -state $S/state \
//	    -out internal/harness/testdata/acp/opencode.json
//
// Passed on Linux amd64 with microsandbox 0.7.7 on 2026-10-11 against the dev base
// (OpenCode 1.18.35): ACP v1, loadSession, session resume/close/list/fork, image and
// embedded context. It found that OpenCode rejects an initialize whose clientInfo lacks
// "version" (-32602), which the schema requires.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

const (
	baseImage      = "sandbox-studio-base:dev"
	cleanupTimeout = time.Minute
	readyTimeout   = 90 * time.Second
)

func main() {
	agent := flag.String("agent", "", "absolute path to the compiled Linux studio-agent binary")
	state := flag.String("state", "", "absolute path of an empty private state directory")
	out := flag.String("out", "", "where to write opencode's initialize reply")
	flag.Parse()
	if !filepath.IsAbs(*agent) || !filepath.IsAbs(*state) || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: acp -agent /abs/studio-agent -state /abs/empty-dir -out opencode.json")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := check(ctx, *agent, *state, *out); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL acp:", err)
		os.Exit(1)
	}
}

func check(ctx context.Context, agentPath, root, out string) (err error) {
	suffix, err := randomBase32(5)
	if err != nil {
		return err
	}
	guestDir := filepath.Join(root, "guest")
	if err := copyFile(agentPath, filepath.Join(guestDir, "bin", "studio-agent")); err != nil {
		return err
	}
	socket := filepath.Join(root, "agent.sock")
	id := "acpprobe" + suffix
	owned := runtime.OwnedVM{Name: "spike-acp-" + suffix, Labels: map[string]string{"studio.kind": "spike", "studio.spike": "acp"}}
	rt := runtime.New(runtime.Options{Image: baseImage, GuestDir: guestDir})
	hub := agentchan.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := hub.Listen(id, socket); err != nil {
		return err
	}
	defer func() {
		hub.Close(id)
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if cerr := rt.RemoveOwned(cleanup, owned); cerr != nil {
			err = errors.Join(err, fmt.Errorf("remove %s: %w", owned.Name, cerr))
		} else if err == nil {
			fmt.Println("PASS owned VM removed")
		}
	}()
	// Deny-all in both directions: no rule lets anything leave or enter the VM; the guest
	// agent talks to the hub over vsock only.
	vm, err := msb.CreateSandbox(ctx, owned.Name, msb.WithImage(baseImage), msb.WithPullPolicy(msb.PullPolicyNever),
		msb.WithCPUs(1), msb.WithMemory(1024), msb.WithMaxMemory(1024), msb.WithDetached(), msb.WithLabels(owned.Labels),
		msb.WithMounts(map[string]msb.MountConfig{
			"/workspace":      msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: 256}),
			"/var/lib/docker": msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: 256}),
			"/opt/studio":     msb.Mount.Bind(guestDir, msb.MountOptions{Readonly: true}),
		}),
		msb.WithVsock(msb.VsockRoute{HostSocket: socket, Port: agentproto.VsockPort}),
		msb.WithNetwork(&msb.NetworkConfig{DefaultEgress: msb.PolicyActionDeny, DefaultIngress: msb.PolicyActionDeny}))
	if err != nil {
		return fmt.Errorf("create %s: %w", owned.Name, err)
	}
	boot, err := vm.Exec(ctx, runtime.AgentPath, []string{"boot"}, msb.WithExecUser("root"), msb.WithExecTimeout(60*time.Second))
	if err != nil || !boot.Success() {
		var detail string
		if boot != nil {
			detail = boot.Stderr()
		}
		return errors.Join(fmt.Errorf("boot guest: %v %s", err, detail), vm.Detach(ctx))
	}
	if err := vm.Detach(ctx); err != nil {
		return err
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, readyTimeout)
	defer cancelWait()
	if err := hub.WaitConnected(waitCtx, id); err != nil {
		return fmt.Errorf("wait for the guest agent: %w", err)
	}
	fmt.Println("PASS guest agent connected")
	sb := struct{ ID string }{id}

	// The adapters missing from the dev base are reported as such, not as a dead stream.
	for _, h := range []harness.Harness{harness.Claude{}, harness.Codex{}} {
		_, err := hub.OpenACP(ctx, sb.ID, agentproto.StartACP{Chat: "probe" + h.Name(), Harness: h.Name(), Command: h.ACPCommand(),
			Env: harness.ACPEnvironment(h, harness.Provider{})})
		var he agentchan.HarnessError
		if !errors.As(err, &he) {
			return fmt.Errorf("%s: want a missing-adapter error, got %v", h.Name(), err)
		}
		fmt.Printf("PASS %s adapter reported missing: %s\n", h.Name(), firstLine(err.Error()))
	}

	oc := harness.OpenCode{}
	rwc, err := hub.OpenACP(ctx, sb.ID, agentproto.StartACP{Chat: "probeopencode", Harness: oc.Name(), Command: oc.ACPCommand(),
		Env: harness.ACPEnvironment(oc, harness.Provider{})})
	if err != nil {
		return fmt.Errorf("open opencode acp: %w", err)
	}
	conn := acp.NewConn(rwc, nopHandler{})
	defer conn.Close()
	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var raw json.RawMessage
	if err := conn.Call(initCtx, acp.MethodInitialize, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{Form: &struct{}{}}},
		ClientInfo:         &acp.Implementation{Name: "sandbox-studio-probe", Version: "0"}}, &raw); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var res acp.InitializeResponse
	if err := json.Unmarshal(raw, &res); err != nil || res.ProtocolVersion != acp.ProtocolVersion {
		return fmt.Errorf("initialize reply %s: %v", raw, err)
	}
	var pretty any
	json.Unmarshal(raw, &pretty)
	b, _ := json.MarshalIndent(pretty, "", "  ")
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("PASS opencode acp initialize: version %d, loadSession %t, resume %t, close %t, image %t\n",
		res.ProtocolVersion, res.AgentCapabilities.LoadSession, res.AgentCapabilities.CanResume(),
		res.AgentCapabilities.CanClose(), res.AgentCapabilities.PromptCapabilities.Image)

	// Closing the stream must stop the agent: the slot frees, so the same chat starts again.
	conn.Close()
	time.Sleep(time.Second)
	again, err := hub.OpenACP(ctx, sb.ID, agentproto.StartACP{Chat: "probeopencode", Harness: oc.Name(), Command: oc.ACPCommand()})
	if err != nil {
		return fmt.Errorf("restart after close: %w", err)
	}
	again.Close()
	fmt.Println("PASS closing the stream freed the chat's slot")
	return nil
}

type nopHandler struct{}

func (nopHandler) SessionUpdate(acp.SessionNotification) {}
func (nopHandler) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.Cancelled(), nil
}
func (nopHandler) CreateElicitation(context.Context, acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

func randomBase32(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}
