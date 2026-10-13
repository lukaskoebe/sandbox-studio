package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PrivateHost is a host on an environment's private hosts list. Its connections may reach
// private addresses, such as a company host behind a VPN; see docs/network.md.
type PrivateHost struct {
	Host  string `json:"host" doc:"A host name or a wildcard, as in rules: git.company.local or *.company.local"`
	Ports []int  `json:"ports" doc:"Ports the host may be reached on. Empty means 80 and 443"`
}

// PrivateHosts returns the private hosts of an environment.
func (s *Store) PrivateHosts(ctx context.Context, envID string) ([]PrivateHost, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT private_hosts FROM environments WHERE id = ?", envID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var hosts []PrivateHost
	if err := json.Unmarshal([]byte(raw), &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

// SetPrivateHosts replaces the private hosts of an environment. The caller checks them.
func (s *Store) SetPrivateHosts(ctx context.Context, envID string, hosts []PrivateHost) error {
	if hosts == nil {
		hosts = []PrivateHost{}
	}
	raw, err := json.Marshal(hosts)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE environments SET private_hosts = ? WHERE id = ?", string(raw), envID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
