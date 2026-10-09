package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type checkpointFakeRuntime struct {
	mu                         sync.Mutex
	statuses                   map[string]runtime.Status
	checkpoints                map[string]bool
	checkpointRestoreSupported bool
	createCheckpointErr        error
	removeCheckpointErr        error
}

var _ sandboxes.SandboxRuntime = (*checkpointFakeRuntime)(nil)

func newCheckpointFakeRuntime() *checkpointFakeRuntime {
	return &checkpointFakeRuntime{
		statuses:                   make(map[string]runtime.Status),
		checkpoints:                make(map[string]bool),
		checkpointRestoreSupported: true,
	}
}

func (r *checkpointFakeRuntime) CheckpointRestoreSupported() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkpointRestoreSupported
}

func (r *checkpointFakeRuntime) setCheckpointRestoreSupported(supported bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkpointRestoreSupported = supported
}

func (r *checkpointFakeRuntime) Create(_ context.Context, name string, _ runtime.Spec, _ string, _ map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[name] = runtime.StatusRunning
	return nil
}

func (r *checkpointFakeRuntime) Start(_ context.Context, name string) error {
	r.setStatus(name, runtime.StatusRunning)
	return nil
}

func (r *checkpointFakeRuntime) Stop(_ context.Context, name string) error {
	r.setStatus(name, runtime.StatusStopped)
	return nil
}

func (r *checkpointFakeRuntime) Remove(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.statuses, name)
	return nil
}

func (r *checkpointFakeRuntime) Status(_ context.Context, name string) (runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.statuses[name]
	if !ok {
		return runtime.StatusAbsent, nil
	}
	return status, nil
}

func (r *checkpointFakeRuntime) Statuses(_ context.Context, prefix string) (map[string]runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	statuses := make(map[string]runtime.Status)
	for name, status := range r.statuses {
		if strings.HasPrefix(name, prefix) {
			statuses[name] = status
		}
	}
	return statuses, nil
}

func (r *checkpointFakeRuntime) CreateCheckpoint(_ context.Context, _ string, sandboxID, checkpointID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createCheckpointErr != nil {
		return r.createCheckpointErr
	}
	r.checkpoints[sandboxID+"/"+checkpointID] = true
	return nil
}

func (r *checkpointFakeRuntime) setCreateCheckpointError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCheckpointErr = err
}

func (r *checkpointFakeRuntime) RemoveCheckpoint(_ context.Context, sandboxID, checkpointID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.removeCheckpointErr != nil {
		return r.removeCheckpointErr
	}
	delete(r.checkpoints, sandboxID+"/"+checkpointID)
	return nil
}

func (r *checkpointFakeRuntime) setRemoveCheckpointError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeCheckpointErr = err
}

func (r *checkpointFakeRuntime) RestoreCheckpoint(_ context.Context, sandboxID, checkpointID, newVMName string, _ runtime.Egress) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.checkpoints[sandboxID+"/"+checkpointID] {
		return store.ErrNotFound
	}
	r.statuses[newVMName] = runtime.StatusStopped
	return nil
}

func (r *checkpointFakeRuntime) setStatus(name string, status runtime.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[name] = status
}

type checkpointFakeEgress struct{}

func (checkpointFakeEgress) Attach(store.Sandbox) (runtime.Egress, error) {
	return runtime.Egress{}, nil
}
func (checkpointFakeEgress) Detach(string) {}

func checkpointAPI(t *testing.T) (http.Handler, *Server, store.Environment, store.Sandbox, *checkpointFakeRuntime) {
	t.Helper()
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	sb := newSandbox(t, s, env, "dev")

	rt := newCheckpointFakeRuntime()
	rt.setStatus(sandboxes.VMName(sb), runtime.StatusStopped)
	hub := agentchan.NewHub(s.Log)
	dataDir, err := os.MkdirTemp("", "ss-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	dataPaths := paths.Paths{Data: dataDir}
	if err := dataPaths.Ensure(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hub.Close(sb.ID) })
	s.Sandboxes = &sandboxes.Manager{
		Store: s.Store, Runtime: rt, Hub: hub, Egress: checkpointFakeEgress{}, Paths: dataPaths, Log: s.Log,
	}
	return h, s, env, sb, rt
}

func TestCheckpointRoutes(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/checkpoints"
	vmName := sandboxes.VMName(sb)

	if rec := do(h, http.MethodPost, base, `{"name":"   "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank checkpoint name: %d %s", rec.Code, rec.Body)
	}

	rt.setStatus(vmName, runtime.StatusPaused)
	if rec := do(h, http.MethodPost, base, `{"name":"before-edit"}`); rec.Code != http.StatusConflict {
		t.Fatalf("create from paused state: %d %s", rec.Code, rec.Body)
	}
	rt.setStatus(vmName, runtime.StatusStopped)

	rec := do(h, http.MethodPost, base, `{"name":"before edit"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: %d %s", rec.Code, rec.Body)
	}
	checkpoint := decode[store.Checkpoint](t, rec)
	if checkpoint.ID == "" || checkpoint.EnvironmentID != env.ID || checkpoint.SandboxID != sb.ID ||
		checkpoint.Name != "before edit" || checkpoint.State != store.CheckpointStateReady || checkpoint.Generation != sb.Generation {
		t.Fatalf("created checkpoint: %+v", checkpoint)
	}
	if strings.Contains(rec.Body.String(), "checkpointRef") || strings.Contains(rec.Body.String(), "path") {
		t.Fatalf("checkpoint response leaked runtime details: %s", rec.Body)
	}

	if rec := do(h, http.MethodPost, base, `{"name":"before edit"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate checkpoint name: %d %s", rec.Code, rec.Body)
	}
	listRec := do(h, http.MethodGet, base, "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list checkpoints: %d %s", listRec.Code, listRec.Body)
	}
	listed := decode[[]store.Checkpoint](t, listRec)
	if len(listed) != 1 || listed[0].ID != checkpoint.ID {
		t.Fatalf("listed checkpoints: %+v", listed)
	}

	otherEnv := newEnvironment(t, s, "other")
	otherSandbox := newSandbox(t, s, otherEnv, "foreign")
	foreignCheckpoint, err := s.Store.CreateCheckpoint(context.Background(), otherEnv.ID, otherSandbox.ID, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	foreignURL := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + otherSandbox.ID + "/checkpoints"
	if rec := do(h, http.MethodGet, foreignURL, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("list foreign sandbox checkpoints: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodDelete, base+"/"+foreignCheckpoint.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete foreign checkpoint: %d %s", rec.Code, rec.Body)
	}

	pendingRestore, err := s.Store.BeginRestore(context.Background(), env.ID, sb.ID, checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, http.MethodDelete, base+"/"+checkpoint.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete checkpoint used by a restore: %d %s", rec.Code, rec.Body)
	}
	if err := s.Store.EndRestore(context.Background(), pendingRestore); err != nil {
		t.Fatal(err)
	}

	rt.setStatus(vmName, runtime.StatusStopped)
	restoreURL := base + "/" + checkpoint.ID + "/restore"
	rec = do(h, http.MethodPost, restoreURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore checkpoint: %d %s", rec.Code, rec.Body)
	}
	view := decode[sandboxes.View](t, rec)
	if view.ID != sb.ID || view.Generation != sb.Generation+1 || view.Status != runtime.StatusStopped {
		t.Fatalf("restored sandbox view: %+v", view)
	}

	if rec := do(h, http.MethodDelete, base+"/"+checkpoint.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete checkpoint: %d %s", rec.Code, rec.Body)
	}
	listRec = do(h, http.MethodGet, base, "")
	if listRec.Code != http.StatusOK || len(decode[[]store.Checkpoint](t, listRec)) != 0 {
		t.Fatalf("list after delete: %d %s", listRec.Code, listRec.Body)
	}
}

func TestCheckpointRestoreRequiresStoppedSource(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/checkpoints"
	checkpoint, err := s.Store.CreateCheckpoint(context.Background(), env.ID, sb.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetCheckpointState(context.Background(), env.ID, sb.ID, checkpoint.ID, store.CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.checkpoints[sb.ID+"/"+checkpoint.ID] = true
	rt.mu.Unlock()
	rt.setStatus(sandboxes.VMName(sb), runtime.StatusPaused)

	if rec := do(h, http.MethodPost, base+"/"+checkpoint.ID+"/restore", ""); rec.Code != http.StatusConflict {
		t.Fatalf("restore from paused source: %d %s", rec.Code, rec.Body)
	}
}

func TestCheckpointRestoreUnavailable(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	checkpoint, err := s.Store.CreateCheckpoint(context.Background(), env.ID, sb.ID, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetCheckpointState(context.Background(), env.ID, sb.ID, checkpoint.ID, store.CheckpointStateReady); err != nil {
		t.Fatal(err)
	}
	if err := rt.CreateCheckpoint(context.Background(), sandboxes.VMName(sb), sb.ID, checkpoint.ID); err != nil {
		t.Fatal(err)
	}
	rt.setCheckpointRestoreSupported(false)

	url := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/checkpoints/" + checkpoint.ID + "/restore"
	if rec := do(h, http.MethodPost, url, ""); rec.Code != http.StatusConflict {
		t.Fatalf("restore when runtime does not support it: %d %s", rec.Code, rec.Body)
	}

	source, err := s.Store.Sandbox(context.Background(), env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Generation != sb.Generation {
		t.Fatalf("source generation changed: got %d, want %d", source.Generation, sb.Generation)
	}
	got, err := s.Store.Checkpoint(context.Background(), env.ID, sb.ID, checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.CheckpointStateReady {
		t.Fatalf("checkpoint state changed: got %q, want %q", got.State, store.CheckpointStateReady)
	}
}

func TestLiveCheckpointOfSystemdSandboxConflicts(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	rt.setCreateCheckpointError(runtime.ErrLiveCheckpointUnsupported)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/checkpoints"
	if rec := do(h, http.MethodPost, base, `{"name":"live"}`); rec.Code != http.StatusConflict {
		t.Fatalf("live checkpoint of a systemd sandbox: %d %s", rec.Code, rec.Body)
	}
	if list, err := s.Store.Checkpoints(context.Background(), env.ID, sb.ID); err != nil || len(list) != 0 {
		t.Fatalf("checkpoints after refused capture = %+v, %v; want none", list, err)
	}
}

func TestDeleteCheckpointInUseConflictRetainsSourceAndCheckpoint(t *testing.T) {
	h, s, env, sb, rt := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID + "/checkpoints"
	createRec := do(h, http.MethodPost, base, `{"name":"in-use"}`)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: %d %s", createRec.Code, createRec.Body)
	}
	checkpoint := decode[store.Checkpoint](t, createRec)
	rt.setRemoveCheckpointError(runtime.ErrCheckpointInUse)

	if rec := do(h, http.MethodDelete, base+"/"+checkpoint.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete checkpoint in use: %d %s", rec.Code, rec.Body)
	}

	source, err := s.Store.Sandbox(context.Background(), env.ID, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Generation != sb.Generation {
		t.Fatalf("source generation changed: got %d, want %d", source.Generation, sb.Generation)
	}
	got, err := s.Store.Checkpoint(context.Background(), env.ID, sb.ID, checkpoint.ID)
	if err != nil {
		t.Fatalf("checkpoint was not retained: %v", err)
	}
	if got.ID != checkpoint.ID {
		t.Fatalf("retained checkpoint ID: got %q, want %q", got.ID, checkpoint.ID)
	}

	rt.mu.Lock()
	sourceStatus, sourceExists := rt.statuses[sandboxes.VMName(sb)]
	snapshotExists := rt.checkpoints[sb.ID+"/"+checkpoint.ID]
	rt.mu.Unlock()
	if !sourceExists || sourceStatus != runtime.StatusStopped {
		t.Fatalf("source runtime was not retained: exists=%t status=%q", sourceExists, sourceStatus)
	}
	if !snapshotExists {
		t.Fatal("checkpoint runtime snapshot was not retained")
	}
}
