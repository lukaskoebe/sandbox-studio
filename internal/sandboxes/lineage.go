package sandboxes

import (
	"context"
	"sort"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A sandbox's history is recorded when each operation succeeds (docs/sandboxes.md#lineage).
// A rebase and a restore record in the store, with the generation switch; the rest here.

// StatusDeleted is the status of a sandbox that was deleted: its tombstone in a lineage.
const StatusDeleted = "deleted"

// eventFor is an event of kind for sb as it is now: its generation, name and template.
func eventFor(sb store.Sandbox, kind string) store.SandboxEvent {
	return store.SandboxEvent{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: kind,
		Generation: sb.Generation, Name: sb.Name, TemplateID: sb.TemplateID}
}

// recordEvent appends ev to the history. It runs after its operation succeeded, so a failure is
// logged rather than returned: the operation stands either way.
func (m *Manager) recordEvent(ctx context.Context, ev store.SandboxEvent) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	if err := m.Store.RecordSandboxEvent(ctx, ev); err != nil {
		m.Log.Warn("sandbox history", "sandbox", ev.SandboxID, "event", ev.Kind, "err", err)
	}
}

// recordFork records a fork that succeeded on both sides: the fork's history starts with it,
// and the source's shows where it branched off.
func (m *Manager) recordFork(ctx context.Context, source, fork store.Sandbox) {
	child := eventFor(fork, store.EventFork)
	child.OtherSandboxID = source.ID
	m.recordEvent(ctx, child)
	branch := eventFor(source, store.EventFork)
	branch.OtherSandboxID = fork.ID
	m.recordEvent(ctx, branch)
}

// LineageNode is a sandbox in a lineage: a live one, or the tombstone of a deleted one.
type LineageNode struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TemplateID   string `json:"templateId,omitempty" doc:"The template the sandbox runs; empty for none"`
	TemplateName string `json:"templateName,omitempty" doc:"The display name of that template; empty for none, or once the template is gone"`
	Status       string `json:"status" doc:"running, waiting (still starting, or a create or fork that runs), failed, deleted, or another runtime status"`
	Deleted      bool   `json:"deleted"`
	// ForkGeneration is the generation of the source the fork was made from.
	ForkGeneration int `json:"forkGeneration,omitempty"`
}

// LineageCheckpoint is a checkpoint as the lineage shows it.
type LineageCheckpoint struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	State      string    `json:"state" enum:"creating,ready,deleting"`
	Generation int       `json:"generation"`
	CreatedAt  time.Time `json:"createdAt"`
	// RestoreAllowed is true when the runtime can restore checkpoints (its gate is open) and
	// this one is ready. The sandbox must also be stopped.
	RestoreAllowed bool `json:"restoreAllowed"`
}

// LineageEvent is a point in a sandbox's history, with the display names of the templates it
// refers to.
type LineageEvent struct {
	store.SandboxEvent
	TemplateName     string `json:"templateName,omitempty" doc:"The display name of the template of the generation; empty for none, or once the template is gone"`
	FromTemplateName string `json:"fromTemplateName,omitempty" doc:"For a rebase, the display name of the template it moved from"`
}

// Lineage is the history of one sandbox: its events in order, the sandbox it was forked from,
// the forks made from it and its checkpoints.
type Lineage struct {
	SandboxID                  string              `json:"sandboxId"`
	Generation                 int                 `json:"generation"`
	TemplateID                 string              `json:"templateId,omitempty" doc:"The template of the current generation; empty for none"`
	TemplateName               string              `json:"templateName,omitempty" doc:"The display name of that template; empty for none, or once the template is gone"`
	Events                     []LineageEvent      `json:"events"`
	Parent                     *LineageNode        `json:"parent,omitempty" doc:"The sandbox this one was forked from"`
	Children                   []LineageNode       `json:"children"`
	Checkpoints                []LineageCheckpoint `json:"checkpoints"`
	CheckpointRestoreSupported bool                `json:"checkpointRestoreSupported"`
}

// LineageTree is an environment's family tree: its sandboxes grouped by template, with the
// forks of each sandbox under it.
type LineageTree struct {
	Templates []LineageTemplate `json:"templates"`
}

// LineageTemplate groups the sandboxes of one template. ID is empty for sandboxes without one.
type LineageTemplate struct {
	ID        string            `json:"id"`
	Name      string            `json:"name,omitempty" doc:"The template's display name; empty for sandboxes without one, or for a template that is gone"`
	CreatedAt time.Time         `json:"createdAt" doc:"When the template was built; zero if it no longer exists"`
	Sandboxes []LineageTreeNode `json:"sandboxes"`
}

// LineageTreeNode is a sandbox in the family tree and the forks made from it.
type LineageTreeNode struct {
	LineageNode
	Forks []LineageTreeNode `json:"forks"`
}

// lineageStatus folds a sandbox's state into the statuses the tree colours.
func lineageStatus(v View) string {
	switch v.Status {
	case StatusFailed, runtime.StatusCrashed:
		return "failed"
	case StatusCreating, runtime.StatusStarting, runtime.StatusDraining:
		return "waiting"
	default:
		return string(v.Status)
	}
}

func liveNode(v View) LineageNode {
	return LineageNode{ID: v.ID, Name: v.Name, TemplateID: v.TemplateID, TemplateName: v.TemplateName, Status: lineageStatus(v)}
}

// deletedNode is the tombstone of a deleted sandbox. names are the display names of the
// environment's templates.
func deletedNode(ev store.SandboxEvent, names map[string]string) LineageNode {
	return LineageNode{ID: ev.SandboxID, Name: ev.Name, TemplateID: ev.TemplateID, TemplateName: names[ev.TemplateID], Status: StatusDeleted, Deleted: true}
}

// lineageEvents gives a sandbox's events the display names of the templates they refer to.
func lineageEvents(events []store.SandboxEvent, names map[string]string) []LineageEvent {
	out := make([]LineageEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, LineageEvent{SandboxEvent: ev, TemplateName: names[ev.TemplateID], FromTemplateName: names[ev.FromTemplateID]})
	}
	return out
}

// Lineage returns the history of a sandbox. Its parent and forks stay visible after they are
// deleted, through their tombstones.
func (m *Manager) Lineage(ctx context.Context, envID, id string) (Lineage, error) {
	views, err := m.List(ctx, envID)
	if err != nil {
		return Lineage{}, err
	}
	var self *View
	for i := range views {
		if views[i].ID == id {
			self = &views[i]
		}
	}
	if self == nil {
		return Lineage{}, store.ErrNotFound
	}
	events, err := m.Store.SandboxEvents(ctx, envID, id)
	if err != nil {
		return Lineage{}, err
	}
	tombstones, err := m.Store.DeleteEvents(ctx, envID)
	if err != nil {
		return Lineage{}, err
	}
	checkpoints, err := m.Store.Checkpoints(ctx, envID, id)
	if err != nil {
		return Lineage{}, err
	}
	names, err := m.Store.TemplateNames(ctx, envID)
	if err != nil {
		return Lineage{}, err
	}

	out := Lineage{
		SandboxID: id, Generation: self.Generation, TemplateID: self.TemplateID, TemplateName: names[self.TemplateID],
		Events: lineageEvents(events, names), Children: []LineageNode{}, Checkpoints: []LineageCheckpoint{},
		CheckpointRestoreSupported: m.Runtime.CheckpointRestoreSupported(),
	}
	if self.ForkedFromID != "" {
		parent := LineageNode{ID: self.ForkedFromID, Name: "deleted sandbox", Status: StatusDeleted, Deleted: true}
		for _, v := range views {
			if v.ID == self.ForkedFromID {
				parent = liveNode(v)
			}
		}
		for _, ev := range tombstones {
			if ev.SandboxID == self.ForkedFromID {
				parent = deletedNode(ev, names)
			}
		}
		parent.ForkGeneration = self.ForkedFromGeneration
		out.Parent = &parent
	}
	for _, v := range views {
		if v.ForkedFromID == id {
			child := liveNode(v)
			child.ForkGeneration = v.ForkedFromGeneration
			out.Children = append(out.Children, child)
		}
	}
	for _, ev := range tombstones {
		if ev.OtherSandboxID != id {
			continue
		}
		child := deletedNode(ev, names)
		for _, branch := range events {
			if branch.Kind == store.EventFork && branch.OtherSandboxID == ev.SandboxID {
				child.ForkGeneration = branch.Generation
			}
		}
		out.Children = append(out.Children, child)
	}
	restorable := out.CheckpointRestoreSupported
	for _, cp := range checkpoints {
		out.Checkpoints = append(out.Checkpoints, LineageCheckpoint{
			ID: cp.ID, Name: cp.Name, State: cp.State, Generation: cp.Generation, CreatedAt: cp.CreatedAt,
			RestoreAllowed: restorable && cp.State == store.CheckpointStateReady,
		})
	}
	return out, nil
}

// LineageTree returns the family tree of an environment. A fork sits under its source, even
// when the source is deleted; a fork whose source is unknown sits under its template.
func (m *Manager) LineageTree(ctx context.Context, envID string) (LineageTree, error) {
	views, err := m.List(ctx, envID)
	if err != nil {
		return LineageTree{}, err
	}
	tombstones, err := m.Store.DeleteEvents(ctx, envID)
	if err != nil {
		return LineageTree{}, err
	}
	templates, err := m.Store.Templates(ctx, envID)
	if err != nil {
		return LineageTree{}, err
	}
	names, err := m.Store.TemplateNames(ctx, envID)
	if err != nil {
		return LineageTree{}, err
	}

	type entry struct {
		node     LineageNode
		source   string // the sandbox it was forked from, if any
		template string
	}
	var entries []entry
	known := map[string]bool{}
	for _, v := range views {
		entries = append(entries, entry{node: liveNode(v), source: v.ForkedFromID, template: v.TemplateID})
		known[v.ID] = true
	}
	for _, ev := range tombstones {
		entries = append(entries, entry{node: deletedNode(ev, names), source: ev.OtherSandboxID, template: ev.TemplateID})
		known[ev.SandboxID] = true
	}
	forks := map[string][]int{} // source ID → indexes of its forks
	roots := map[string][]int{} // template ID → indexes of sandboxes not forked from a known one
	for i, e := range entries {
		if e.source != "" && known[e.source] {
			forks[e.source] = append(forks[e.source], i)
			continue
		}
		roots[e.template] = append(roots[e.template], i)
	}

	var build func(i int) LineageTreeNode
	build = func(i int) LineageTreeNode {
		node := LineageTreeNode{LineageNode: entries[i].node, Forks: []LineageTreeNode{}}
		for _, child := range forks[entries[i].node.ID] {
			node.Forks = append(node.Forks, build(child))
		}
		return node
	}
	createdAt := map[string]time.Time{}
	for _, t := range templates {
		createdAt[t.ID] = t.CreatedAt
	}
	ids := make([]string, 0, len(roots))
	for id := range roots {
		ids = append(ids, id)
	}
	// Known templates oldest first, then templates that are gone, and sandboxes without one last.
	sort.SliceStable(ids, func(a, b int) bool {
		ca, oka := createdAt[ids[a]]
		cb, okb := createdAt[ids[b]]
		switch {
		case ids[a] == "" || ids[b] == "":
			return ids[b] == ""
		case oka != okb:
			return oka
		case oka && !ca.Equal(cb):
			return ca.Before(cb)
		default:
			return ids[a] < ids[b]
		}
	})
	out := LineageTree{Templates: []LineageTemplate{}}
	for _, id := range ids {
		group := LineageTemplate{ID: id, Name: names[id], CreatedAt: createdAt[id], Sandboxes: []LineageTreeNode{}}
		for _, i := range roots[id] {
			group.Sandboxes = append(group.Sandboxes, build(i))
		}
		out.Templates = append(out.Templates, group)
	}
	return out, nil
}

// RenameCheckpoint gives a ready checkpoint another name. Its disks are not touched.
func (m *Manager) RenameCheckpoint(ctx context.Context, envID, id, checkpointID, name string) (store.Checkpoint, error) {
	defer m.publish(envID, id)
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return store.Checkpoint{}, err
	}
	name, err := validateCheckpointName(name)
	if err != nil {
		return store.Checkpoint{}, err
	}
	return m.Store.RenameCheckpoint(ctx, envID, id, checkpointID, name)
}
