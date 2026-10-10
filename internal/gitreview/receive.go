package gitreview

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/revlist"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// receivePack stages a push. The advertisement is the forge's current branches and tags,
// fetched into the staging repository first, so the sandbox sends only what the forge
// lacks and the staged commits can be checked and diffed on the host.
func (s *Service) receivePack(w http.ResponseWriter, r *http.Request, t target) {
	ctx := r.Context()
	token, err := s.token(ctx, t.forge)
	if err != nil {
		s.Log.Error("git remote: forge token", "forge", t.forge.Name, "err", err)
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not read the forge's token.")
		return
	}
	rm, err := s.remote(t, token)
	if err != nil {
		refuse(w, http.StatusNotImplemented, err.Error())
		return
	}
	path := s.stagingPath(t)
	unlock := s.lock(path)
	defer unlock()
	repo, err := openStaging(path)
	if err != nil {
		s.Log.Error("git remote: staging", "path", path, "err", err)
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not open its staging repository.")
		return
	}
	if t.op == "info/refs" {
		if err := rm.fetchMirror(ctx, repo); err != nil {
			refuse(w, http.StatusBadGateway, "Sandbox Studio could not fetch the repository from the forge: "+mask(err.Error(), token))
			return
		}
		s.advertise(w, repo)
		return
	}
	s.stage(w, r, t, repo)
}

// noThin asks git not to send thin packs; go-git has no constant for it.
const noThin = capability.Capability("no-thin")

func receiveCaps() *capability.List {
	c := capability.NewList()
	c.Set(capability.Agent, "sandbox-studio")
	c.Set(capability.ReportStatus)
	c.Set(capability.Sideband64k)
	c.Set(capability.OFSDelta)
	c.Set(noThin) // the pack must be complete on its own, so the connectivity check is meaningful
	return c
}

func (s *Service) advertise(w http.ResponseWriter, repo *git.Repository) {
	refs, err := mirrorRefs(repo)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not read its staging repository.")
		return
	}
	ar := packp.NewAdvRefs()
	ar.Prefix = [][]byte{[]byte("# service=git-receive-pack"), pktline.Flush}
	ar.Capabilities = receiveCaps()
	for name, h := range refs {
		ar.References[name.String()] = h
	}
	var buf bytes.Buffer
	if err := ar.Encode(&buf); err != nil {
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not encode its advertisement.")
		return
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(buf.Bytes())
}

// errTooLarge reports a push over the size limit.
var errTooLarge = errors.New("too large")

// limitedBody reads at most n bytes of the request body (after gzip, if any).
type limitedBody struct {
	r io.Reader
	n int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errTooLarge
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// stage receives a push into the staging repository and raises a git.push approval for
// each branch update that passes the checks.
func (s *Service) stage(w http.ResponseWriter, r *http.Request, t target, repo *git.Repository) {
	ctx := r.Context()
	if r.Header.Get("Content-Type") != "application/x-git-receive-pack-request" {
		refuse(w, http.StatusUnsupportedMediaType, "Expected a git receive-pack request.")
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, s.Limits.MaxPack)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(body)
		if err != nil {
			refuse(w, http.StatusBadRequest, "The request body is not valid gzip.")
			return
		}
		body = gz
	}
	body = &limitedBody{r: body, n: s.Limits.MaxPack}
	br := bufio.NewReader(body)
	// git probes with a lone flush-pkt before sending a large request.
	if head, err := br.Peek(4); err == nil && string(head) == "0000" {
		if _, err := br.Peek(5); errors.Is(err, io.EOF) {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	req := packp.NewReferenceUpdateRequest()
	if err := req.Decode(br); err != nil {
		if tooLarge(err) {
			s.refuseLarge(w)
			return
		}
		refuse(w, http.StatusBadRequest, "Malformed push request.")
		return
	}
	if len(req.Commands) > s.Limits.MaxCommands {
		refuse(w, http.StatusBadRequest, fmt.Sprintf("A push may update at most %d branches.", s.Limits.MaxCommands))
		return
	}
	if req.Packfile != nil {
		if err := packfile.UpdateObjectStorage(repo.Storer, req.Packfile); err != nil && !errors.Is(err, packfile.ErrEmptyPackfile) {
			if tooLarge(err) {
				s.refuseLarge(w)
				return
			}
			s.Log.Info("git remote: bad pack", "sandbox", t.sb.ID, "err", err)
			s.reply(w, req, "unpack failed", nil, nil)
			return
		}
		io.Copy(io.Discard, io.LimitReader(req.Packfile, 1<<20))
	}
	upstream, err := mirrorRefs(repo)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "Sandbox Studio could not read its staging repository.")
		return
	}
	statuses := make([]string, len(req.Commands))
	var messages []string
	for i, cmd := range req.Commands {
		approvalID, err := s.stageCommand(ctx, t, repo, upstream, cmd)
		if err != nil {
			statuses[i] = err.Error()
			continue
		}
		messages = append(messages, fmt.Sprintf("Sandbox Studio: %s queued for review (approval %s). It reaches the forge once the user approves it.", cmd.Name.Short(), approvalID))
	}
	s.reply(w, req, "ok", statuses, messages)
}

func tooLarge(err error) bool {
	var mb *http.MaxBytesError
	return errors.Is(err, errTooLarge) || errors.As(err, &mb)
}

func (s *Service) refuseLarge(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	refuse(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("The push is larger than Sandbox Studio accepts (%d MiB). Push fewer or smaller commits.", s.Limits.MaxPack>>20))
}

// reply sends the report-status, with messages on the progress band when the client
// asked for side-band, which git shows as "remote: ..." lines.
func (s *Service) reply(w http.ResponseWriter, req *packp.ReferenceUpdateRequest, unpack string, statuses, messages []string) {
	rs := packp.NewReportStatus()
	rs.UnpackStatus = unpack
	for i, cmd := range req.Commands {
		st := "ok"
		if unpack != "ok" {
			st = "unpacker error"
		} else if statuses[i] != "" {
			st = statuses[i]
		}
		rs.CommandStatuses = append(rs.CommandStatuses, &packp.CommandStatus{ReferenceName: cmd.Name, Status: st})
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	var report bytes.Buffer
	if req.Capabilities.Supports(capability.ReportStatus) {
		rs.Encode(&report)
	}
	typ := sideband.Type(-1)
	switch {
	case req.Capabilities.Supports(capability.Sideband64k):
		typ = sideband.Sideband64k
	case req.Capabilities.Supports(capability.Sideband):
		typ = sideband.Sideband
	}
	if typ < 0 {
		w.Write(report.Bytes())
		return
	}
	m := sideband.NewMuxer(typ, w)
	for _, msg := range messages {
		m.WriteChannel(sideband.ProgressMessage, []byte(msg+"\n"))
	}
	if report.Len() > 0 {
		m.WriteChannel(sideband.PackData, report.Bytes())
	}
	pktline.NewEncoder(w).Flush()
}

// refusal is a reason a ref update is refused, as git prints it after "! [remote rejected]".
type refusal string

func (r refusal) Error() string { return string(r) }

// stageCommand checks one ref update and stages it. Anything the sandbox sent is untrusted:
// names, hashes and objects are all checked against what the host has.
func (s *Service) stageCommand(ctx context.Context, t target, repo *git.Repository, upstream map[plumbing.ReferenceName]plumbing.Hash, cmd *packp.Command) (string, error) {
	if !cmd.Name.IsBranch() || cmd.Name.Validate() != nil {
		return "", refusal("only branches (refs/heads/...) can be pushed through Sandbox Studio")
	}
	if cmd.New.IsZero() {
		return "", refusal("deleting branches is not supported through Sandbox Studio")
	}
	current := upstream[cmd.Name]
	if cmd.Old != current {
		return "", refusal("stale info: the branch changed on the forge; fetch first")
	}
	newCommit, err := repo.CommitObject(cmd.New)
	if err != nil {
		return "", refusal("missing objects: the pushed commit was not received")
	}
	objs, err := revlist.Objects(repo.Storer, []plumbing.Hash{cmd.New}, hashes(upstream))
	if err != nil {
		return "", refusal("missing objects: the push is incomplete")
	}
	if !current.IsZero() {
		oldCommit, err := repo.CommitObject(current)
		if err != nil {
			return "", refusal("internal error: the forge's commit is missing")
		}
		if ok, err := oldCommit.IsAncestor(newCommit); err != nil || !ok {
			return "", refusal("non-fast-forward: Sandbox Studio only stages fast-forward pushes; fetch, rebase and push again")
		}
	}
	rv, err := s.review(ctx, repo, current, cmd.New, objs)
	if err != nil {
		s.Log.Error("git remote: review", "sandbox", t.sb.ID, "err", err)
		return "", refusal("internal error: Sandbox Studio could not describe the push")
	}
	rv.Sandbox, rv.Forge, rv.Owner, rv.Repo, rv.Branch = t.sb.Name, t.forge.Name, t.owner, t.repo, cmd.Name.Short()
	rv.New = cmd.New.String()
	if !current.IsZero() {
		rv.Old = current.String()
	}
	if t.sb.PersonaID != "" {
		if p, err := s.Store.Persona(ctx, t.sb.EnvironmentID, t.sb.PersonaID); err == nil {
			rv.Persona = p.Name
		}
	}

	// A newer push to the same branch replaces any still waiting.
	pending, err := s.Store.PendingGitPushes(ctx, t.sb.EnvironmentID, t.sb.ID, t.forge.ID, t.owner, t.repo, cmd.Name.String())
	if err != nil {
		return "", refusal("internal error")
	}
	for _, p := range pending {
		if err := s.Store.SettleGitPush(ctx, p.EnvironmentID, p.ID, store.PushPending, store.PushSuperseded, "replaced by a later push", ""); err == nil {
			s.Store.DecideApproval(ctx, p.EnvironmentID, p.ApprovalID, store.StatusDismissed, "")
		}
	}

	payload, err := json.Marshal(rv)
	if err != nil {
		return "", refusal("internal error")
	}
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: t.sb.EnvironmentID, SandboxID: t.sb.ID, Kind: KindPush,
		Subject: fmt.Sprintf("%s/%s/%s:%s", t.forge.Name, t.owner, t.repo, cmd.Name.Short()), Payload: payload,
	})
	if err != nil {
		s.Log.Error("git remote: approval", "err", err)
		return "", refusal("internal error: Sandbox Studio could not queue the push")
	}
	id := store.NewID()
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/studio/push/"+id), cmd.New)); err != nil {
		return "", refusal("internal error: Sandbox Studio could not stage the push")
	}
	_, err = s.Store.CreateGitPush(ctx, store.GitPush{
		ID: id, EnvironmentID: t.sb.EnvironmentID, SandboxID: t.sb.ID, PersonaName: rv.Persona,
		ForgeID: t.forge.ID, ForgeName: t.forge.Name, Owner: t.owner, Repo: t.repo, Ref: cmd.Name.String(),
		OldSHA: current.String(), NewSHA: cmd.New.String(), ApprovalID: a.ID,
	})
	if err != nil {
		s.Log.Error("git remote: record push", "err", err)
		s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
		return "", refusal("internal error: Sandbox Studio could not record the push")
	}
	s.publish(t.sb.EnvironmentID, a.ID)
	return a.ID, nil
}
