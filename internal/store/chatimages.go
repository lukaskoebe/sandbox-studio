package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ChatImage is a picture an agent's message shows, copied out of its sandbox: File names it in
// Studio's chat-images folder, or Error says why it wasn't copied.
type ChatImage struct {
	ItemID   string
	Src      string
	ChatID   string
	File     string
	MimeType string
	Size     int64
	Error    string
	Created  time.Time
}

// ChatImage returns the picture item shows as src.
func (s *Store) ChatImage(ctx context.Context, itemID, src string) (ChatImage, error) {
	var img ChatImage
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT item_id, src, chat_id, file, mime_type, size, error, created_at
		FROM chat_images WHERE item_id = ? AND src = ?`, itemID, src).
		Scan(&img.ItemID, &img.Src, &img.ChatID, &img.File, &img.MimeType, &img.Size, &img.Error, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return img, ErrNotFound
	}
	img.Created = time.Unix(created, 0)
	return img, err
}

// PutChatImage records a picture, or a failed try at copying one, in place of what was
// recorded before. An item that is gone is ErrNotFound.
func (s *Store) PutChatImage(ctx context.Context, img ChatImage) error {
	if img.Created.IsZero() {
		img.Created = time.Unix(now(), 0)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_images (item_id, src, chat_id, file, mime_type, size, error, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM chat_items WHERE id = ?)
		ON CONFLICT (item_id, src) DO UPDATE SET file = excluded.file, mime_type = excluded.mime_type,
			size = excluded.size, error = excluded.error, created_at = excluded.created_at`,
		img.ItemID, img.Src, img.ChatID, img.File, img.MimeType, img.Size, img.Error, img.Created.Unix(), img.ItemID)
	if err != nil {
		return err
	}
	if _, err := s.ChatImage(ctx, img.ItemID, img.Src); err != nil {
		return err
	}
	return nil
}

// ChatImageFiles lists the files the recorded pictures are kept in, to remove those of
// deleted chats.
func (s *Store) ChatImageFiles(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT file FROM chat_images WHERE file != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = true
	}
	return out, rows.Err()
}
