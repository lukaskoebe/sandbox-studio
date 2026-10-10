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
	TopicPersonas  = "personas"
)

// Event is one change notification.
type Event struct {
	Topic         string `json:"topic"`
	EnvironmentID string `json:"environmentId,omitempty"`
	// ID names the changed object, e.g. a new approval, so clients can notify about it.
	ID string `json:"id,omitempty"`
}

// Bus is an in-memory publish/subscribe hub. The zero value is ready to use.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
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
