package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PrivateHost is an entry of an environment's local network list (the private hosts): a name
// whose connections may reach private addresses, such as a company host behind a VPN, or private
// addresses themselves, such as a NAS on the LAN; see docs/network.md.
type PrivateHost struct {
	Host  string `json:"host" doc:"A host name or a wildcard, as in rules (git.company.local or *.company.local), a private IPv4 address (192.168.178.20) or a range of them (192.168.178.0/24)"`
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

// HostPorts returns the ports of this computer that an environment's sandboxes may connect to,
// as host.studio.internal; see docs/network.md.
func (s *Store) HostPorts(ctx context.Context, envID string) ([]int, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT host_ports FROM environments WHERE id = ?", envID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var ports []int
	if err := json.Unmarshal([]byte(raw), &ports); err != nil {
		return nil, err
	}
	return ports, nil
}

// SetHostPorts replaces the host ports of an environment. The caller checks them.
func (s *Store) SetHostPorts(ctx context.Context, envID string, ports []int) error {
	if ports == nil {
		ports = []int{}
	}
	raw, err := json.Marshal(ports)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE environments SET host_ports = ? WHERE id = ?", string(raw), envID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
