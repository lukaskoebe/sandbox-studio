package browser

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A download is a file the browser saved (docs/browser.md#downloads). The broker lists them
// and copies one into downloads/ of a sandbox's workspace, from the user's Browser page or an
// agent's browser_download_to_workspace. It copies bytes only: a download is never opened,
// run or extracted on the host.

// downloadsFolder is where a copied download goes, under the sandbox's workspace.
const downloadsFolder = "downloads"

// Download is a file a teammate's browser saved, as the Browser page and browser_downloads show it.
type Download struct {
	Session string    `json:"session" doc:"The ephemeral session that saved it; empty for the persistent profile"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Saved   time.Time `json:"saved"`
}

// Tools are the download tools of the browser: browser_downloads and browser_download_to_workspace.
func (s *Service) Tools() []integrations.Tool {
	return []integrations.Tool{
		{Def: agentcall.BrowserDownloadsTool, Handle: s.toolDownloads},
		{Def: agentcall.BrowserDownloadToWorkspaceTool, Handle: s.toolDownloadToWorkspace},
	}
}

// sessionSlot is a session of a teammate's browser and the guest's slot that holds its downloads.
type sessionSlot struct {
	session string
	slot    int
}

// Downloads lists a teammate's downloads for the Browser page, newest first. A browser that is
// not running has none to show.
func (s *Service) Downloads(ctx context.Context, envID, teammateID string) ([]Download, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return nil, err
	}
	sb, status, err := s.VMs.BrowserStatus(ctx, envID, teammateID)
	if err != nil || status != runtime.StatusRunning {
		return []Download{}, nil
	}
	s.mu.Lock()
	st := s.states[teammateID]
	s.mu.Unlock()
	if st == nil || st.env != envID {
		return []Download{}, nil
	}
	return s.listDownloads(ctx, sb.ID, s.sessions(st, nil))
}

// SendDownload copies a download of a teammate's browser into one of the teammate's sandboxes,
// as the user asks on the Browser page.
func (s *Service) SendDownload(ctx context.Context, envID, teammateID, session, name, sandboxID string) (agentproto.UploadReply, error) {
	sb, err := s.Store.LookupSandbox(ctx, sandboxID)
	if err != nil || sb.EnvironmentID != envID || sb.TeammateID != teammateID || sb.Kind != "" {
		return agentproto.UploadReply{}, store.ErrNotFound
	}
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return agentproto.UploadReply{}, err
	}
	st := s.state(envID, teammateID)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return agentproto.UploadReply{}, ctx.Err()
	}
	defer func() { <-st.run }()
	pg, err := s.downloadPage(st, session, nil)
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	return s.copyDownload(ctx, st, pg, sb, name, "user")
}

// sessions lists the sessions of st whose downloads a caller sees: the persistent profile and
// the ephemeral sessions owned by owner (every one when owner is nil).
func (s *Service) sessions(st *state, owner *store.Sandbox) []sessionSlot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []sessionSlot{{"", 0}}
	for _, pg := range st.eph {
		if owner == nil || pg.owner.ID == owner.ID {
			out = append(out, sessionSlot{pg.id, pg.slot})
		}
	}
	return out
}

// listDownloads lists the downloads of the given sessions of a browser VM, newest first.
func (s *Service) listDownloads(ctx context.Context, vm string, sessions []sessionSlot) ([]Download, error) {
	out := []Download{}
	for _, ss := range sessions {
		list, err := s.Runner.BrowserDownloads(ctx, vm, agentproto.BrowserDownloadsRequest{Ephemeral: ss.slot})
		if err != nil {
			return nil, err
		}
		for _, f := range list.Files {
			out = append(out, Download{Session: ss.session, Name: f.Name, Size: f.Size, Saved: time.Unix(f.Modified, 0).UTC()})
		}
	}
	slices.SortFunc(out, func(a, b Download) int {
		return cmp.Or(b.Saved.Compare(a.Saved), strings.Compare(a.Name, b.Name))
	})
	return out[:min(len(out), agentproto.MaxBrowserDownloads)], nil
}

// downloadPage is the session a download comes from: the persistent profile for "", else an
// ephemeral session of st that owner may drive (any when owner is nil).
func (s *Service) downloadPage(st *state, session string, owner *store.Sandbox) (*page, error) {
	if session == "" {
		return &st.page, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pg := st.pageFor(session)
	if pg == nil || (owner != nil && pg.owner.ID != owner.ID) {
		return nil, ErrNoSession
	}
	return pg, nil
}

// copyDownload copies download name of pg's session into downloads/ of sb's workspace, under
// its name or a free one, and logs the copy. The caller holds the teammate's command slot.
func (s *Service) copyDownload(ctx context.Context, st *state, pg *page, sb store.Sandbox, name, actor string) (agentproto.UploadReply, error) {
	s.mu.Lock()
	vm, slot := st.vm, pg.slot
	s.mu.Unlock()
	if vm == "" {
		return agentproto.UploadReply{}, ErrNotLive
	}
	var reply agentproto.UploadReply
	err := s.Runner.BrowserDownload(ctx, vm, agentproto.BrowserDownloadsRequest{Ephemeral: slot, Name: name},
		func(d agentproto.BrowserDownload, body io.Reader) error {
			var err error
			reply, err = s.Runner.Upload(ctx, sb.ID, agentproto.UploadRequest{Dir: downloadsFolder, Name: d.Name, Size: d.Size,
				Conflict: agentproto.ConflictKeep, CreateDir: true}, body)
			return err
		})
	e := entry{actor: actor, sandbox: sb.ID, action: "send download", target: name, outcome: "ok",
		detail: "sent to " + sb.Name + " as " + downloadsFolder + "/" + reply.Name}
	if err != nil {
		e.outcome, e.detail = "failed", err.Error()
	}
	s.record(ctx, st, pg, e)
	return reply, err
}

// callerState is the teammate's browser that a call from sb, as the channel named it, drives.
func (s *Service) callerState(sb store.Sandbox) (*state, error) {
	if sb.Kind != "" || sb.BuildJobID != "" {
		return nil, ErrNoCalls
	}
	if sb.TeammateID == "" {
		return nil, ErrNoTeammate
	}
	return s.state(sb.EnvironmentID, sb.TeammateID), nil
}

// vmOf is the browser VM of st, once it has been started.
func (s *Service) vmOf(st *state) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return st.vm
}

// toolDownloads answers browser_downloads for the calling sandbox: the persistent profile's
// downloads and those of the ephemeral sessions it opened.
func (s *Service) toolDownloads(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct{}
	if err := decodeTool(params, &in); err != nil {
		return nil, err
	}
	st, err := s.callerState(sb)
	if err != nil {
		return nil, toolError(err)
	}
	release, err := s.acquire(ctx, st, &st.page)
	if err != nil {
		return nil, toolError(err)
	}
	defer release()
	if err := s.boot(ctx, st); err != nil {
		return nil, integrations.ToolError("the browser could not be started; the user can look at the Browser page in Studio")
	}
	files, err := s.listDownloads(ctx, s.vmOf(st), s.sessions(st, &sb))
	if err != nil {
		return nil, err
	}
	return map[string]any{"downloads": files}, nil
}

// toolDownloadToWorkspace answers browser_download_to_workspace: it copies a download of the
// teammate's browser into downloads/ of the calling sandbox.
func (s *Service) toolDownloadToWorkspace(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Name    string `json:"name"`
		Session string `json:"session"`
	}
	if err := decodeTool(params, &in); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, integrations.ToolError("name the download, as browser_downloads lists it")
	}
	if in.Session != "" && !IsEphemeral(in.Session) {
		return nil, toolError(ErrNoSession)
	}
	st, err := s.callerState(sb)
	if err != nil {
		return nil, toolError(err)
	}
	pg, err := s.downloadPage(st, in.Session, &sb)
	if err != nil {
		return nil, toolError(err)
	}
	release, err := s.acquire(ctx, st, pg)
	if err != nil {
		return nil, toolError(err)
	}
	defer release()
	if err := s.boot(ctx, st); err != nil {
		return nil, integrations.ToolError("the browser could not be started; the user can look at the Browser page in Studio")
	}
	// The session may have ended while the call waited for its turn.
	if pg, err = s.downloadPage(st, in.Session, &sb); err != nil {
		return nil, toolError(err)
	}
	reply, err := s.copyDownload(ctx, st, pg, sb, in.Name, "agent")
	if err != nil {
		return nil, integrations.ToolError("could not copy %s: %v", in.Name, err)
	}
	return map[string]any{"name": reply.Name, "path": downloadsFolder + "/" + reply.Name, "size": reply.Size}, nil
}

// decodeTool reads a tool's arguments strictly: an argument the tool doesn't name is refused.
func decodeTool(params json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return integrations.ToolError("invalid arguments: %v", err)
	}
	return nil
}

// toolError shows a refusal to the agent as it is.
func toolError(err error) error { return integrations.ToolError("%v", err) }
