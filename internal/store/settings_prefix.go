package store

import "context"

// SettingsWithPrefix returns every setting whose key starts with prefix.
func (s *Store) SettingsWithPrefix(ctx context.Context, prefix string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings WHERE substr(key, 1, ?) = ?", len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// DeleteSetting removes key; a missing key is fine.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	return err
}
