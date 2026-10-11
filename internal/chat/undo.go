package chat

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func (s *Service) undoNote(context.Context, store.Chat) (string, error) { return "", nil }

func (s *Service) snapshot(_ context.Context, _ store.Chat, t store.ChatTurn) store.ChatTurn {
	return t
}

func (s *Service) changes(context.Context, store.Chat, store.ChatTurn) {}
