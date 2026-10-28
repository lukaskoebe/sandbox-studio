package sshconfig

import (
	"context"
	"log/slog"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// settle is how long Follow waits after a change for more before it writes the file.
const settle = 500 * time.Millisecond

// Follow writes the keys and the config file now, and the file again whenever sandboxes
// change, until ctx ends. list returns the sandboxes the file lists.
func (s *Service) Follow(ctx context.Context, bus *events.Bus, list func(context.Context) ([]store.Sandbox, error), log *slog.Logger) {
	if err := s.Ensure(); err != nil {
		log.Warn("SSH access: could not create Studio's SSH keys", "dir", s.Dir, "err", err)
		return
	}
	write := func() {
		sbs, err := list(ctx)
		if err == nil {
			err = s.Write(Hosts(sbs))
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("SSH access: could not write Studio's SSH config", "path", s.Config(), "err", err)
		}
	}
	for ctx.Err() == nil {
		ch, unsubscribe := bus.Subscribe()
		write() // first, and after the bus dropped us for falling behind
		s.follow(ctx, ch, write)
		unsubscribe()
	}
}

func (s *Service) follow(ctx context.Context, ch <-chan events.Event, write func()) {
	var due <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Topic == events.TopicSandboxes && due == nil {
				due = time.After(settle)
			}
		case <-due:
			due = nil
			write()
		}
	}
}
