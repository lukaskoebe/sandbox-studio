package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Undo and the Changes panel (docs/design/chat-mode.md section 5): the guest snapshots
// /workspace before each prompt (as the turn's ID) and after it (as ID-after). The
// snapshots stay in the guest; the host only keeps their IDs and each turn's list of
// changed files.

const (
	// keepTurns is how many of a chat's newest turns keep their snapshots.
	keepTurns = 50
	// snapshotTimeout bounds a snapshot, which holds up its prompt.
	snapshotTimeout = 2 * time.Minute
)

func afterID(turnID string) string { return turnID + "-after" }

// TurnChanges are the files a turn changed.
type TurnChanges struct {
	Files []agentproto.FileChange `json:"files"`
	More  int                     `json:"more,omitempty" doc:"Changed files not listed"`
}

// snapshot snapshots the workspace before turn t. Without a snapshot the turn works as
// usual; it just can't be undone.
func (s *Service) snapshot(ctx context.Context, c store.Chat, t store.ChatTurn) store.ChatTurn {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	base := ""
	if turns, err := s.Store.ChatTurns(ctx, c.ID); err == nil {
		for _, x := range slices.Backward(turns) {
			if x.ID != t.ID && x.Snapshot != "" {
				base = afterID(x.ID)
				break
			}
		}
	}
	reply, err := s.Guest.Snapshot(ctx, c.SandboxID, agentproto.SnapshotRequest{Op: agentproto.SnapshotTake, ID: t.ID, Base: base})
	if err != nil {
		s.log().Warn("chat: snapshot before the turn", "chat", c.ID, "err", err)
		return t
	}
	s.forget(ctx, c, reply.Dropped)
	next, err := s.Store.SetChatTurnSnapshot(ctx, c.ID, t.ID, t.ID)
	if err != nil {
		return t
	}
	return next
}

// changes snapshots the workspace after turn t and stores what the turn changed, then
// drops the snapshots of turns too old to undo. It says whether the turn changed files, or
// may have: true when there was no snapshot to compare.
func (s *Service) changes(ctx context.Context, c store.Chat, t store.ChatTurn) bool {
	if t.Snapshot == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	reply, err := s.Guest.Snapshot(ctx, c.SandboxID, agentproto.SnapshotRequest{Op: agentproto.SnapshotTake, ID: afterID(t.ID), Base: t.Snapshot})
	if err != nil {
		s.log().Warn("chat: snapshot after the turn", "chat", c.ID, "err", err)
		return true
	}
	s.forget(ctx, c, reply.Dropped)
	raw, _ := json.Marshal(TurnChanges{Files: orEmpty(reply.Changes), More: reply.More})
	if _, err := s.Store.SetChatTurnChanges(ctx, c.ID, t.ID, raw); err == nil {
		s.changed(c.EnvironmentID, c.ID, false)
	}
	if err := s.collect(ctx, c.EnvironmentID, c.SandboxID); err != nil {
		s.log().Warn("chat: collect snapshots", "sandbox", c.SandboxID, "err", err)
	}
	return len(reply.Changes) > 0 || reply.More > 0
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// forget clears the snapshots the guest dropped to stay under its cap.
func (s *Service) forget(ctx context.Context, c store.Chat, dropped []string) {
	for _, id := range dropped {
		if strings.HasSuffix(id, "-after") {
			continue // only the diffs go
		}
		if _, err := s.Store.SetChatTurnSnapshot(ctx, c.ID, id, ""); err == nil {
			s.changed(c.EnvironmentID, c.ID, false)
			continue
		}
		// A turn of another chat in the sandbox.
		chats, err := s.Store.Chats(ctx, c.EnvironmentID, c.SandboxID)
		if err != nil {
			continue
		}
		for _, o := range chats {
			if _, err := s.Store.SetChatTurnSnapshot(ctx, o.ID, id, ""); err == nil {
				s.changed(o.EnvironmentID, o.ID, false)
				break
			}
		}
	}
}

// collect keeps the snapshots of every chat's newest turns in the sandbox and has the
// guest remove the rest.
func (s *Service) collect(ctx context.Context, envID, sandboxID string) error {
	chats, err := s.Store.Chats(ctx, envID, sandboxID)
	if err != nil {
		return err
	}
	var keep []store.ChatTurn
	for _, c := range chats {
		turns, err := s.Store.ChatTurns(ctx, c.ID)
		if err != nil {
			return err
		}
		n := 0
		for _, t := range slices.Backward(turns) {
			if t.Snapshot == "" {
				continue
			}
			if n++; n <= keepTurns {
				keep = append(keep, t)
			} else if _, err := s.Store.SetChatTurnSnapshot(ctx, c.ID, t.ID, ""); err == nil {
				s.changed(c.EnvironmentID, c.ID, false)
			}
		}
	}
	// The guest takes a bounded list: past it, the oldest turns of all chats go.
	slices.SortFunc(keep, func(a, b store.ChatTurn) int { return b.StartedAt.Compare(a.StartedAt) })
	if limit := agentproto.MaxSnapshotKeep / 2; len(keep) > limit {
		for _, t := range keep[limit:] {
			if _, err := s.Store.SetChatTurnSnapshot(ctx, t.ChatID, t.ID, ""); err == nil {
				s.changed(envID, t.ChatID, false)
			}
		}
		keep = keep[:limit]
	}
	ids := make([]string, 0, 2*len(keep))
	for _, t := range keep {
		ids = append(ids, t.Snapshot, afterID(t.ID))
	}
	_, err = s.Guest.Snapshot(ctx, sandboxID, agentproto.SnapshotRequest{Op: agentproto.SnapshotGC, Keep: ids})
	return err
}

// undoNote is the text the next prompt starts with after an undo, so the agent knows its
// edits are gone. It is due while the chat's newest turn is undone, so it goes out once.
func (s *Service) undoNote(ctx context.Context, c store.Chat) (string, error) {
	turns, err := s.Store.ChatTurns(ctx, c.ID)
	if err != nil {
		return "", err
	}
	n := 0
	for n < len(turns) && turns[len(turns)-1-n].UndoneAt != nil {
		n++
	}
	if n == 0 {
		return "", nil
	}
	first := turns[len(turns)-n]
	prompt := ""
	if items, err := s.Store.ChatItems(ctx, c.ID, first.ID); err == nil {
		for _, it := range items {
			if v := ItemView(it); it.Kind == KindUser && v.User != nil {
				prompt = titleOf(v.User.Text, nil)
				break
			}
		}
	}
	requests := "request"
	if n > 1 {
		requests = fmt.Sprintf("%d requests", n)
	}
	if prompt == "" {
		return fmt.Sprintf("[Studio] The user undid your changes from the last %s; the files are back as they were before.", requests), nil
	}
	return fmt.Sprintf("[Studio] The user undid your changes from the last %s; the files are back as they were before '%s'.", requests, prompt), nil
}

// Undo puts the workspace back as it was before turn turnID, which undoes that turn and
// every later one.
func (s *Service) Undo(ctx context.Context, envID, chatID, turnID string) error {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return err
	}
	t, err := s.Store.ChatTurn(ctx, chatID, turnID)
	if err != nil {
		return err
	}
	if t.UndoneAt != nil {
		return Conflict("this change is already undone")
	}
	if t.Snapshot == "" {
		return Conflict("this change can no longer be undone")
	}
	if c, err = s.lock(ctx, c, store.ChatRunning); err != nil {
		return err
	}
	defer s.unlock(c, store.ChatRunning)
	reply, err := s.Guest.Snapshot(ctx, c.SandboxID, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: t.Snapshot})
	if err != nil {
		return fmt.Errorf("restoring the files: %w", err)
	}
	undone, err := s.Store.UndoChatTurns(ctx, chatID, t.Seq)
	if err != nil {
		return err
	}
	text := "Undid this change"
	if n := len(undone) - 1; n == 1 {
		text = "Undid this and 1 later change"
	} else if n > 1 {
		text = fmt.Sprintf("Undid this and %d later changes", n)
	}
	if len(reply.Failed) > 0 {
		text += fmt.Sprintf("; %d files couldn't be put back: %s", len(reply.Failed), strings.Join(reply.Failed, ", "))
	}
	if _, err := s.Store.AddChatItem(ctx, markerItem(t, MarkerUndo, text)); err != nil {
		return err
	}
	s.changed(envID, chatID, false)
	return nil
}

// Changes returns what turn turnID changed, with diffs. Once the turn's snapshots are
// gone only the list remains.
func (s *Service) Changes(ctx context.Context, envID, chatID, turnID string) (TurnChanges, error) {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return TurnChanges{}, err
	}
	t, err := s.Store.ChatTurn(ctx, chatID, turnID)
	if err != nil {
		return TurnChanges{}, err
	}
	stored := TurnChanges{Files: []agentproto.FileChange{}}
	if len(t.Changes) > 0 {
		if err := json.Unmarshal(t.Changes, &stored); err != nil {
			return TurnChanges{}, fmt.Errorf("the turn's changes are unreadable: %w", err)
		}
	}
	if t.Snapshot == "" || t.EndedAt == nil || len(t.Changes) == 0 {
		return stored, nil
	}
	reply, err := s.Guest.Snapshot(ctx, c.SandboxID, agentproto.SnapshotRequest{Op: agentproto.SnapshotChanges, ID: afterID(t.ID), Base: t.Snapshot})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return TurnChanges{}, err
		}
		s.log().Debug("chat: turn diffs", "chat", chatID, "err", err)
		return stored, nil
	}
	return TurnChanges{Files: orEmpty(reply.Changes), More: reply.More}, nil
}

// Files lists a directory or reads a file of a sandbox's workspace.
func (s *Service) Files(ctx context.Context, sandboxID string, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	return s.Guest.Files(ctx, sandboxID, req)
}

// Upload streams a file into a folder of a sandbox's workspace.
func (s *Service) Upload(ctx context.Context, sandboxID string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	return s.Guest.Upload(ctx, sandboxID, req, body)
}
