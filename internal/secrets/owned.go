package secrets

import (
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Prepare validates and seals a new secret without storing it, for a caller that inserts
// it in one transaction with the record owning it (a provider's key). The caller must call
// Changed after the insert.
func (v *Vault) Prepare(envID, name, value string, hosts []string, note string) (store.Secret, error) {
	if err := checkName(name); err != nil {
		return store.Secret{}, err
	}
	value, err := normalizeValue(value)
	if err != nil {
		return store.Secret{}, err
	}
	hosts, err = normalizeHosts(hosts)
	if err != nil {
		return store.Secret{}, err
	}
	id := store.NewID()
	return store.Secret{
		ID: id, EnvironmentID: envID, Name: name, Sealed: v.Seal([]byte(value), []byte(id)),
		Hosts: hosts, Placeholder: newPlaceholder(), Note: note,
	}, nil
}

// SealValue validates and seals a new value for the existing secret id.
func (v *Vault) SealValue(id, value string) ([]byte, error) {
	value, err := normalizeValue(value)
	if err != nil {
		return nil, err
	}
	return v.Seal([]byte(value), []byte(id)), nil
}

// Hosts validates and normalizes the hosts a secret is bound to.
func Hosts(hosts []string) ([]string, error) { return normalizeHosts(hosts) }

// Changed drops the cached bindings of an environment after its secrets were written
// outside the vault's own methods.
func (v *Vault) Changed(envID string) {
	v.mu.Lock()
	delete(v.bindings, envID)
	v.mu.Unlock()
}
