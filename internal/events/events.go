// Package events fans out change notifications to open UI tabs (served as SSE at
// /api/events). Events say what changed, not the new state: clients refetch.
package events

import "sync"

// Topics.
const (
	TopicApprovals = "approvals"
	TopicRules     = "rules"
	TopicSecrets   = "secrets"
	TopicBuilds    = "builds"
	TopicTeammates = "teammates"
	TopicChats     = "chats" // a chat was created, deleted, renamed or changed state
	// TopicSandboxes: a sandbox changed (its create or fork, lifecycle, checkpoints, rebases,
	// history or deletion), so its lineage and the lineage tree are stale.
	TopicSandboxes = "sandboxes"
	// TopicTasks: a teammate's task list or heartbeat log changed, by the user or the teammate.
	TopicTasks = "tasks"
	// TopicBrowser: a teammate's browser action log has a new entry; TeammateID names it.
	TopicBrowser = "browser"
	// TopicTerminals: what runs in a sandbox's terminals changed; SandboxID names it.
	TopicTerminals = "terminals"
)

// Event is one change notification.
type Event struct {
	Topic         string `json:"topic"`
	EnvironmentID string `json:"environmentId,omitempty"`
	// ID names the changed object, e.g. a new approval, so clients can notify about it.
	ID string `json:"id,omitempty"`
	// TeammateID and SandboxID narrow a secrets event to the sandboxes of one teammate or
	// to one sandbox; without them it covers the environment.
	TeammateID string `json:"teammateId,omitempty"`
	SandboxID  string `json:"sandboxId,omitempty"`
}

// Bus is an in-memory publish/subscribe hub. The zero value is ready to use.
type Bus struct {
	mu       sync.Mutex
	subs     map[chan Event]struct{}
	watchers int
}

// Watch counts an open UI tab until the returned function is called. Work done only for
// someone looking, such as asking guests what their terminals run, checks Watched first.
func (b *Bus) Watch() func() {
	b.mu.Lock()
	b.watchers++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.watchers--
			b.mu.Unlock()
		})
	}
}

// Watched reports whether a UI tab is open.
func (b *Bus) Watched() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.watchers > 0
}

// Publish sends e to every subscriber. A subscriber that falls too far behind is
// disconnected; its client reconnects and refetches everything.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// Subscribe returns a channel of events, closed when the subscriber is dropped, and a
// function that unsubscribes.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[chan Event]struct{}{}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
	}
}
