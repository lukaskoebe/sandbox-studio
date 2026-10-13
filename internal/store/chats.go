package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Chat states.
const (
	ChatIdle     = "idle"
	ChatRunning  = "running"
	ChatTerminal = "terminal" // handed over to the harness's TUI in a tmux session
	ChatOrphaned = "orphaned" // the agent's session is gone; read-only
)

// Chat turn origins.
const (
	OriginChat     = "chat"
	OriginTerminal = "terminal"
)

// Chat is a conversation with the owner teammate's harness in one sandbox, over ACP.
type Chat struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	SandboxID     string `json:"sandboxId"`
	TeammateID    string `json:"teammateId"`
	Harness       string `json:"harness"`
	// ACPSessionID is the harness's own session ID, also what its TUI resumes.
	ACPSessionID string    `json:"acpSessionId,omitempty"`
	Title        string    `json:"title"`
	State        string    `json:"state" enum:"idle,running,terminal,orphaned"`
	Rev          int64     `json:"rev" doc:"The chat's change counter: every change to the chat, its turns or its items raises it"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	// Heartbeat marks a chat a heartbeat opened (heartbeats.go).
	Heartbeat bool `json:"heartbeat"`
}

// ChatTurn is one prompt and everything the agent did for it.
type ChatTurn struct {
	ID         string          `json:"id"`
	ChatID     string          `json:"chatId"`
	Seq        int             `json:"seq"`
	Snapshot   string          `json:"-"`
	StopReason string          `json:"stopReason,omitempty"`
	Usage      json.RawMessage `json:"-"`
	Changes    json.RawMessage `json:"-"`
	Origin     string          `json:"origin" enum:"chat,terminal"`
	UndoneAt   *time.Time      `json:"undoneAt,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	EndedAt    *time.Time      `json:"endedAt,omitempty"`
	Rev        int64           `json:"rev"`
}

// ChatItem is one thing shown in a chat: a message, a tool call, a plan, a question.
type ChatItem struct {
	ID         string          `json:"id"`
	ChatID     string          `json:"chatId"`
	TurnID     string          `json:"turnId"`
	Seq        int             `json:"seq"`
	Kind       string          `json:"kind"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Status     string          `json:"status,omitempty"`
	ApprovalID string          `json:"approvalId,omitempty"`
	Body       json.RawMessage `json:"-"`
	UpdatedAt  time.Time       `json:"updatedAt"`
	Rev        int64           `json:"rev"`
}

const chatCols = "id, environment_id, sandbox_id, teammate_id, harness, acp_session_id, title, state, rev, created_at, updated_at, heartbeat"

func scanChat(row interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	var created, updated int64
	err := row.Scan(&c.ID, &c.EnvironmentID, &c.SandboxID, &c.TeammateID, &c.Harness, &c.ACPSessionID, &c.Title, &c.State, &c.Rev, &created, &updated, &c.Heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return c, err
}

const chatTurnCols = "id, chat_id, seq, snapshot, stop_reason, usage, changes, origin, undone_at, started_at, ended_at, rev"

func scanChatTurn(row interface{ Scan(...any) error }) (ChatTurn, error) {
	var t ChatTurn
	var usage, changes []byte
	var undone, ended sql.NullInt64
	var started int64
	err := row.Scan(&t.ID, &t.ChatID, &t.Seq, &t.Snapshot, &t.StopReason, &usage, &changes, &t.Origin, &undone, &started, &ended, &t.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.Usage, t.Changes = usage, changes
	t.StartedAt = time.Unix(started, 0)
	if undone.Valid {
		u := time.Unix(undone.Int64, 0)
		t.UndoneAt = &u
	}
	if ended.Valid {
		e := time.Unix(ended.Int64, 0)
		t.EndedAt = &e
	}
	return t, err
}

const chatItemCols = "id, chat_id, turn_id, seq, kind, tool_call_id, status, approval_id, body, updated_at, rev"

func scanChatItem(row interface{ Scan(...any) error }) (ChatItem, error) {
	var it ChatItem
	var body []byte
	var updated int64
	err := row.Scan(&it.ID, &it.ChatID, &it.TurnID, &it.Seq, &it.Kind, &it.ToolCallID, &it.Status, &it.ApprovalID, &body, &updated, &it.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	it.Body = body
	it.UpdatedAt = time.Unix(updated, 0)
	return it, err
}

type execQuerier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// nextChatRev takes the chat's next rev.
func nextChatRev(ctx context.Context, q execQuerier, chatID string) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, "UPDATE chats SET rev = rev + 1, updated_at = ? WHERE id = ? RETURNING rev", now(), chatID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rev, err
}

// inChatTx runs fn in a transaction with the chat's next rev.
func (s *Store) inChatTx(ctx context.Context, chatID string, fn func(tx *sql.Tx, rev int64) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rev, err := nextChatRev(ctx, tx, chatID)
	if err != nil {
		return err
	}
	if err := fn(tx, rev); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateChat inserts c, an idle chat; the store picks its ID unless c has one.
func (s *Store) CreateChat(ctx context.Context, c Chat) (Chat, error) {
	if c.ID == "" {
		c.ID = NewID()
	}
	t := now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO chats (id, environment_id, sandbox_id, teammate_id, harness, title, state, rev, created_at, updated_at, heartbeat) VALUES (?, ?, ?, ?, ?, ?, 'idle', 1, ?, ?, ?)",
		c.ID, c.EnvironmentID, c.SandboxID, c.TeammateID, c.Harness, c.Title, t, t, c.Heartbeat)
	if err != nil {
		return c, err
	}
	return s.Chat(ctx, c.EnvironmentID, c.ID)
}

// Chat returns one chat of an environment.
func (s *Store) Chat(ctx context.Context, envID, id string) (Chat, error) {
	return scanChat(s.db.QueryRowContext(ctx, "SELECT "+chatCols+" FROM chats WHERE environment_id = ? AND id = ?", envID, id))
}

// Chats lists an environment's chats, or one sandbox's if sandboxID is set, most
// recently active first.
func (s *Store) Chats(ctx context.Context, envID, sandboxID string) ([]Chat, error) {
	return s.chats(ctx, "SELECT "+chatCols+" FROM chats WHERE environment_id = ? AND (? = '' OR sandbox_id = ?) ORDER BY updated_at DESC, rev DESC",
		envID, sandboxID, sandboxID)
}

// ChatsInState lists the chats of every environment in a state.
func (s *Store) ChatsInState(ctx context.Context, state string) ([]Chat, error) {
	return s.chats(ctx, "SELECT "+chatCols+" FROM chats WHERE state = ?", state)
}

func (s *Store) chats(ctx context.Context, query string, args ...any) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetChatState moves a chat to state if it is in one of from (any state if from is
// empty), or fails with ErrConflict.
func (s *Store) SetChatState(ctx context.Context, envID, id, state string, from ...string) (Chat, error) {
	c, err := s.Chat(ctx, envID, id)
	if err != nil {
		return c, err
	}
	err = s.inChatTx(ctx, id, func(tx *sql.Tx, _ int64) error {
		var cur string
		if err := tx.QueryRowContext(ctx, "SELECT state FROM chats WHERE id = ?", id).Scan(&cur); err != nil {
			return err
		}
		if len(from) > 0 && !slices.Contains(from, cur) {
			return ErrConflict
		}
		_, err := tx.ExecContext(ctx, "UPDATE chats SET state = ? WHERE id = ?", state, id)
		return err
	})
	if err != nil {
		return c, err
	}
	return s.Chat(ctx, envID, id)
}

// SetChatSession records the harness's session ID.
func (s *Store) SetChatSession(ctx context.Context, chatID, sessionID string) error {
	return s.inChatTx(ctx, chatID, func(tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, "UPDATE chats SET acp_session_id = ? WHERE id = ?", sessionID, chatID)
		return err
	})
}

// SetChatTitle renames a chat.
func (s *Store) SetChatTitle(ctx context.Context, chatID, title string) error {
	return s.inChatTx(ctx, chatID, func(tx *sql.Tx, _ int64) error {
		_, err := tx.ExecContext(ctx, "UPDATE chats SET title = ? WHERE id = ?", title, chatID)
		return err
	})
}

// DeleteChat removes a chat with its turns and items.
func (s *Store) DeleteChat(ctx context.Context, envID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM chats WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- turns ----------------------------------------------------------------------------

// StartChatTurn opens the chat's next turn.
func (s *Store) StartChatTurn(ctx context.Context, chatID, origin, snapshot string) (ChatTurn, error) {
	var t ChatTurn
	err := s.inChatTx(ctx, chatID, func(tx *sql.Tx, rev int64) error {
		var err error
		t, err = scanChatTurn(tx.QueryRowContext(ctx, `INSERT INTO chat_turns (id, chat_id, seq, snapshot, origin, started_at, rev)
			VALUES (?, ?, (SELECT IFNULL(MAX(seq), 0) + 1 FROM chat_turns WHERE chat_id = ?), ?, ?, ?, ?) RETURNING `+chatTurnCols,
			NewID(), chatID, chatID, snapshot, origin, now(), rev))
		return err
	})
	return t, err
}

// EndChatTurn closes an open turn; ErrNotFound if it is not open.
func (s *Store) EndChatTurn(ctx context.Context, chatID, turnID, stopReason string, usage []byte) (ChatTurn, error) {
	return s.updateChatTurn(ctx, chatID, turnID, "stop_reason = ?, usage = ?, ended_at = ? WHERE id = ? AND chat_id = ? AND ended_at IS NULL",
		stopReason, nullBytes(usage), now())
}

// SetChatTurnSnapshot records the snapshot taken before a turn.
func (s *Store) SetChatTurnSnapshot(ctx context.Context, chatID, turnID, snapshot string) (ChatTurn, error) {
	return s.updateChatTurn(ctx, chatID, turnID, "snapshot = ? WHERE id = ? AND chat_id = ?", snapshot)
}

// SetChatTurnChanges records the files a turn changed.
func (s *Store) SetChatTurnChanges(ctx context.Context, chatID, turnID string, changes []byte) (ChatTurn, error) {
	return s.updateChatTurn(ctx, chatID, turnID, "changes = ? WHERE id = ? AND chat_id = ?", nullBytes(changes))
}

func (s *Store) updateChatTurn(ctx context.Context, chatID, turnID, set string, args ...any) (ChatTurn, error) {
	var t ChatTurn
	err := s.inChatTx(ctx, chatID, func(tx *sql.Tx, rev int64) error {
		var err error
		t, err = scanChatTurn(tx.QueryRowContext(ctx, "UPDATE chat_turns SET rev = ?, "+set+" RETURNING "+chatTurnCols,
			append(append([]any{rev}, args...), turnID, chatID)...))
		return err
	})
	return t, err
}

// UndoChatTurns marks the turns from seq on as undone and drops their snapshots, which
// a later restore must not use: the workspace is back before turn seq.
func (s *Store) UndoChatTurns(ctx context.Context, chatID string, seq int) ([]ChatTurn, error) {
	var out []ChatTurn
	err := s.inChatTx(ctx, chatID, func(tx *sql.Tx, rev int64) error {
		rows, err := tx.QueryContext(ctx, "UPDATE chat_turns SET undone_at = ?, rev = ? WHERE chat_id = ? AND seq >= ? AND undone_at IS NULL RETURNING "+chatTurnCols,
			now(), rev, chatID, seq)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanChatTurn(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

// ChatTurn returns one turn.
func (s *Store) ChatTurn(ctx context.Context, chatID, turnID string) (ChatTurn, error) {
	return scanChatTurn(s.db.QueryRowContext(ctx, "SELECT "+chatTurnCols+" FROM chat_turns WHERE chat_id = ? AND id = ?", chatID, turnID))
}

// ChatTurns lists a chat's turns in order.
func (s *Store) ChatTurns(ctx context.Context, chatID string) ([]ChatTurn, error) {
	return s.chatTurns(ctx, "SELECT "+chatTurnCols+" FROM chat_turns WHERE chat_id = ? ORDER BY seq", chatID)
}

// OpenChatTurns lists the turns of every chat that never ended.
func (s *Store) OpenChatTurns(ctx context.Context) ([]ChatTurn, error) {
	return s.chatTurns(ctx, "SELECT "+chatTurnCols+" FROM chat_turns WHERE ended_at IS NULL ORDER BY chat_id, seq")
}

func (s *Store) chatTurns(ctx context.Context, query string, args ...any) ([]ChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatTurn{}
	for rows.Next() {
		t, err := scanChatTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- items ----------------------------------------------------------------------------

// AddChatItem appends it to its chat; the store picks the ID unless it has one, the
// seq and the rev.
func (s *Store) AddChatItem(ctx context.Context, it ChatItem) (ChatItem, error) {
	if it.ID == "" {
		it.ID = NewID()
	}
	err := s.inChatTx(ctx, it.ChatID, func(tx *sql.Tx, rev int64) error {
		var err error
		it, err = scanChatItem(tx.QueryRowContext(ctx, `INSERT INTO chat_items (id, chat_id, turn_id, seq, kind, tool_call_id, status, approval_id, body, updated_at, rev)
			VALUES (?, ?, ?, (SELECT IFNULL(MAX(seq), 0) + 1 FROM chat_items WHERE chat_id = ?), ?, ?, ?, ?, ?, ?, ?) RETURNING `+chatItemCols,
			it.ID, it.ChatID, it.TurnID, it.ChatID, it.Kind, it.ToolCallID, it.Status, it.ApprovalID, []byte(it.Body), now(), rev))
		return err
	})
	return it, err
}

// UpdateChatItem stores an item's status, approval and body.
func (s *Store) UpdateChatItem(ctx context.Context, it ChatItem) (ChatItem, error) {
	err := s.inChatTx(ctx, it.ChatID, func(tx *sql.Tx, rev int64) error {
		var err error
		it, err = updateChatItemTx(ctx, tx, rev, it)
		return err
	})
	return it, err
}

func updateChatItemTx(ctx context.Context, tx *sql.Tx, rev int64, it ChatItem) (ChatItem, error) {
	return scanChatItem(tx.QueryRowContext(ctx, `UPDATE chat_items SET tool_call_id = ?, status = ?, approval_id = ?, body = ?, updated_at = ?, rev = ?
		WHERE chat_id = ? AND id = ? RETURNING `+chatItemCols,
		it.ToolCallID, it.Status, it.ApprovalID, []byte(it.Body), now(), rev, it.ChatID, it.ID))
}

// ChatItem returns one item.
func (s *Store) ChatItem(ctx context.Context, chatID, id string) (ChatItem, error) {
	return scanChatItem(s.db.QueryRowContext(ctx, "SELECT "+chatItemCols+" FROM chat_items WHERE chat_id = ? AND id = ?", chatID, id))
}

// ChatItemByApproval returns the item an approval belongs to.
func (s *Store) ChatItemByApproval(ctx context.Context, approvalID string) (ChatItem, error) {
	return scanChatItem(s.db.QueryRowContext(ctx, "SELECT "+chatItemCols+" FROM chat_items WHERE approval_id = ?", approvalID))
}

// ChatItems lists a chat's items in order; turnID narrows them to one turn.
func (s *Store) ChatItems(ctx context.Context, chatID, turnID string) ([]ChatItem, error) {
	return s.chatItems(ctx, "SELECT "+chatItemCols+" FROM chat_items WHERE chat_id = ? AND (? = '' OR turn_id = ?) ORDER BY seq", chatID, turnID, turnID)
}

func (s *Store) chatItems(ctx context.Context, query string, args ...any) ([]ChatItem, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatItem{}
	for rows.Next() {
		it, err := scanChatItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ChatChanges returns the chat and the turns and items that changed after rev, in the
// order they changed, read together so nothing between them is missed.
func (s *Store) ChatChanges(ctx context.Context, envID, chatID string, after int64) (Chat, []ChatTurn, []ChatItem, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Chat{}, nil, nil, err
	}
	defer tx.Rollback()
	c, err := scanChat(tx.QueryRowContext(ctx, "SELECT "+chatCols+" FROM chats WHERE environment_id = ? AND id = ?", envID, chatID))
	if err != nil {
		return c, nil, nil, err
	}
	turns := []ChatTurn{}
	rows, err := tx.QueryContext(ctx, "SELECT "+chatTurnCols+" FROM chat_turns WHERE chat_id = ? AND rev > ? ORDER BY rev, seq", chatID, after)
	if err != nil {
		return c, nil, nil, err
	}
	for rows.Next() {
		t, err := scanChatTurn(rows)
		if err != nil {
			rows.Close()
			return c, nil, nil, err
		}
		turns = append(turns, t)
	}
	rows.Close()
	items := []ChatItem{}
	rows, err = tx.QueryContext(ctx, "SELECT "+chatItemCols+" FROM chat_items WHERE chat_id = ? AND rev > ? ORDER BY rev, seq", chatID, after)
	if err != nil {
		return c, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanChatItem(rows)
		if err != nil {
			return c, nil, nil, err
		}
		items = append(items, it)
	}
	return c, turns, items, rows.Err()
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
