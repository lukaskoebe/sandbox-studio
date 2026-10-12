package api

import (
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestSandboxLineageRoute(t *testing.T) {
	h, s, env, sb, _ := checkpointAPI(t)
	base := testOrigin + "/api/environments/" + env.ID + "/sandboxes/" + sb.ID
	if rec := do(h, http.MethodPost, base+"/checkpoints", `{"name":"before"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: %d %s", rec.Code, rec.Body)
	}
	checkpoint := decode[[]store.Checkpoint](t, do(h, http.MethodGet, base+"/checkpoints", ""))[0]

	rec := do(h, http.MethodGet, base+"/lineage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("lineage: %d %s", rec.Code, rec.Body)
	}
	lineage := decode[sandboxes.Lineage](t, rec)
	if lineage.SandboxID != sb.ID || lineage.Generation != sb.Generation || len(lineage.Events) != 1 ||
		lineage.Events[0].Kind != store.EventCheckpoint || lineage.Events[0].CheckpointID != checkpoint.ID {
		t.Fatalf("lineage = %+v", lineage)
	}
	if !lineage.CheckpointRestoreSupported || len(lineage.Checkpoints) != 1 || !lineage.Checkpoints[0].RestoreAllowed || lineage.Parent != nil || len(lineage.Children) != 0 {
		t.Fatalf("lineage checkpoints or links = %+v", lineage)
	}

	// Renaming changes the name only, and keeps the names of a sandbox unique.
	if rec := do(h, http.MethodPut, base+"/checkpoints/"+checkpoint.ID, `{"name":"before deploy"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename checkpoint: %d %s", rec.Code, rec.Body)
	} else if renamed := decode[store.Checkpoint](t, rec); renamed.Name != "before deploy" || renamed.ID != checkpoint.ID {
		t.Fatalf("renamed checkpoint = %+v", renamed)
	}
	if rec := do(h, http.MethodPut, base+"/checkpoints/"+checkpoint.ID, `{"name":"   "}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank rename: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, base+"/checkpoints", `{"name":"second"}`); rec.Code != http.StatusCreated {
		t.Fatalf("second checkpoint: %d %s", rec.Code, rec.Body)
	}
	var second store.Checkpoint
	for _, cp := range decode[[]store.Checkpoint](t, do(h, http.MethodGet, base+"/checkpoints", "")) {
		if cp.ID != checkpoint.ID {
			second = cp
		}
	}
	if rec := do(h, http.MethodPut, base+"/checkpoints/"+second.ID, `{"name":"before deploy"}`); rec.Code != http.StatusConflict {
		t.Fatalf("rename onto a taken name: %d %s", rec.Code, rec.Body)
	}

	otherEnv := newEnvironment(t, s, "other")
	otherSandbox := newSandbox(t, s, otherEnv, "foreign")
	if rec := do(h, http.MethodGet, testOrigin+"/api/environments/"+env.ID+"/sandboxes/"+otherSandbox.ID+"/lineage", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("lineage of a sandbox in another environment: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, testOrigin+"/api/environments/"+env.ID+"/sandboxes/missing/lineage", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("lineage of a missing sandbox: %d %s", rec.Code, rec.Body)
	}
}

func TestLineageTreeRoute(t *testing.T) {
	h, _, env, sb, _ := checkpointAPI(t)
	rec := do(h, http.MethodGet, testOrigin+"/api/environments/"+env.ID+"/lineage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("tree: %d %s", rec.Code, rec.Body)
	}
	tree := decode[sandboxes.LineageTree](t, rec)
	if len(tree.Templates) != 1 || tree.Templates[0].ID != "" || len(tree.Templates[0].Sandboxes) != 1 {
		t.Fatalf("tree = %+v", tree)
	}
	if node := tree.Templates[0].Sandboxes[0]; node.ID != sb.ID || node.Name != sb.Name || node.Status != "stopped" || node.Deleted || len(node.Forks) != 0 {
		t.Fatalf("sandbox in the tree = %+v", node)
	}
	if rec := do(h, http.MethodGet, testOrigin+"/api/environments/missing/lineage", ""); rec.Code != http.StatusOK || decode[sandboxes.LineageTree](t, rec).Templates == nil {
		t.Fatalf("tree of an unknown environment: %d %s", rec.Code, rec.Body)
	}
}
