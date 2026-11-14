package sandboxes

import (
	"log/slog"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// phaseTimer logs how long each phase of a rebase or disk resize took, and each tree's copy
// with its bytes and rate, so a slow one says where its time went. It logs at info level: a
// rebase is rare, and its few lines are what a report of a slow one needs.
type phaseTimer struct {
	log         *slog.Logger
	id, op      string
	start, last time.Time

	mu   sync.Mutex
	tree runtime.Tree // the tree being copied, "" for none
	at   time.Time    // when its copy started
	n    int64        // the bytes of its files copied so far
}

// timePhases starts timing the operation on sandbox id.
func (m *Manager) timePhases(id string) *phaseTimer {
	op := OperationRebase
	m.opsMu.Lock()
	if o := m.ops[id]; o != nil {
		op = o.progress.Operation
	}
	m.opsMu.Unlock()
	now := time.Now()
	return &phaseTimer{log: m.Log, id: id, op: op, start: now, last: now}
}

// done logs the phase that ended now, since the last one ended. A copy phase's trees are
// logged on their own too.
func (t *phaseTimer) done(phase string) {
	t.endTree()
	now := time.Now()
	t.log.Info("sandbox copy phase", "sandbox", t.id, "operation", t.op, "phase", phase, "took", now.Sub(t.last).Round(time.Millisecond))
	t.last = now
}

// finished logs the whole operation's time and how it ended.
func (t *phaseTimer) finished(err error) {
	t.endTree()
	attrs := []any{"sandbox", t.id, "operation", t.op, "took", time.Since(t.start).Round(time.Millisecond)}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	t.log.Info("sandbox copy finished", attrs...)
}

// copies wraps report so that each tree's copy is logged when the next starts or a phase ends.
func (t *phaseTimer) copies(report copyReport) copyReport {
	return func(tree runtime.Tree, copied, total int64) {
		t.mu.Lock()
		if tree != t.tree {
			t.mu.Unlock()
			t.endTree()
			t.mu.Lock()
			t.tree, t.at = tree, time.Now()
		}
		t.n = copied
		t.mu.Unlock()
		if report != nil {
			report(tree, copied, total)
		}
	}
}

// endTree logs the tree being copied, if any, as done.
func (t *phaseTimer) endTree() {
	t.mu.Lock()
	tree, at, n := t.tree, t.at, t.n
	t.tree, t.n = "", 0
	t.mu.Unlock()
	if tree == "" {
		return
	}
	took := time.Since(at)
	rate := 0.0
	if took > 0 {
		rate = float64(n) / (1 << 20) / took.Seconds()
	}
	t.log.Info("sandbox copy tree", "sandbox", t.id, "operation", t.op, "tree", string(tree),
		"took", took.Round(time.Millisecond), "bytes", n, "mib_per_s", float64(int64(rate*10))/10)
}
