package sandboxes

import (
	"context"
	"slices"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// disksEvery is how often WatchDisks asks the guests how full their disks are, unless
// Manager.DisksEvery says.
const disksEvery = time.Minute

// WatchDisks asks the guests of running sandboxes how full their disks are every
// disksEvery while a UI tab is open, and publishes events.TopicSandboxes when one becomes
// nearly full or has room again: the view's LowDisks.
func (m *Manager) WatchDisks(ctx context.Context) {
	if m.Bus == nil {
		return
	}
	every := m.DisksEvery
	if every <= 0 {
		every = disksEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if !m.Bus.Watched() {
			continue
		}
		connected := map[string]bool{}
		for _, id := range m.Hub.ConnectedIDs() {
			connected[id] = true
		}
		recs, err := m.Store.AllSandboxes(ctx)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if Public(rec) && connected[rec.ID] {
				m.checkDisks(ctx, rec.EnvironmentID, rec.ID)
			} else {
				m.lowDisks.Delete(rec.ID)
			}
		}
	}
}

// checkDisks asks a sandbox's guest how full its disks are and keeps the low ones for
// its view. A guest that can't say, such as one older than KindDisk, has none.
func (m *Manager) checkDisks(ctx context.Context, envID, id string) {
	ctx, cancel := context.WithTimeout(ctx, terminalsAsk)
	defer cancel()
	disks, err := m.Hub.Disks(ctx, id)
	if err != nil {
		disks = nil
	}
	low := slices.DeleteFunc(disks, func(d agentproto.Disk) bool { return !d.Low() })
	var before []agentproto.Disk
	if v, ok := m.lowDisks.Load(id); ok {
		before = v.([]agentproto.Disk)
	}
	if len(low) == 0 {
		m.lowDisks.Delete(id)
	} else {
		m.lowDisks.Store(id, low)
	}
	// Free space changes all the time; a change of which disks are low, or of how full a
	// low one is to the nearest 16 MiB, is news.
	if !slices.EqualFunc(before, low, func(a, b agentproto.Disk) bool {
		return a.Name == b.Name && a.Free>>24 == b.Free>>24
	}) {
		m.publish(envID, id)
	}
}

// LowDisks are the sandbox's disks that were nearly full when Studio last asked.
func (m *Manager) LowDisks(id string) []agentproto.Disk {
	if v, ok := m.lowDisks.Load(id); ok {
		return v.([]agentproto.Disk)
	}
	return nil
}
