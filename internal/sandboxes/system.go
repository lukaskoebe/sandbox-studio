package sandboxes

import (
	"context"
	"errors"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A system VM (store/system.go) is a sandbox row Studio runs for a subscription provider: the
// VM of one login attempt, or the utility VM of a provider's one-shot CLI calls. Like a browser
// VM it is never shown or reachable as a sandbox, and it has no project: its /workspace is a
// small empty disk the runtime always mounts.

// systemResources size a system VM: a CLI needs memory and no Docker.
var systemResources = resources.Resources{CPUs: 1, MemoryMiB: 1024, MaxMemoryMiB: 1024, WorkspaceMiB: 1024, DockerMiB: 256}

// systemReady bounds the wait for a booting system VM's guest agent.
const systemReady = 90 * time.Second

// CreateSystem records a system VM of kind named name in environment envID and boots it from the
// default image. A VM of that name exists already if the store says ErrExists.
func (m *Manager) CreateSystem(ctx context.Context, envID, kind, name string) (store.Sandbox, error) {
	r := systemResources
	rec, err := m.Store.CreateSystemSandbox(ctx, store.Sandbox{
		EnvironmentID: envID, Name: name,
		CPUs: int(r.CPUs), MemoryMiB: int(r.MemoryMiB), MaxMemoryMiB: int(r.MaxMemoryMiB),
		WorkspaceMiB: int(r.WorkspaceMiB), DockerMiB: int(r.DockerMiB),
	}, kind)
	if err != nil {
		return store.Sandbox{}, err
	}
	if err := m.provision(ctx, rec, r, ""); err != nil {
		return store.Sandbox{}, err
	}
	return rec, nil
}

// EnsureSystem returns the system VM of kind and name with its guest agent connected and
// configured: it creates the VM on first use and starts it when it is stopped.
func (m *Manager) EnsureSystem(ctx context.Context, envID, kind, name string) (store.Sandbox, error) {
	rec, err := m.Store.SystemSandbox(ctx, envID, kind, name)
	if errors.Is(err, store.ErrNotFound) {
		rec, err = m.CreateSystem(ctx, envID, kind, name)
	}
	if err != nil {
		return store.Sandbox{}, err
	}
	return rec, m.ready(ctx, rec, systemReady)
}

// SystemStatus returns the system VM of kind and name and its state; the VM may not exist.
func (m *Manager) SystemStatus(ctx context.Context, envID, kind, name string) (store.Sandbox, runtime.Status, error) {
	rec, err := m.Store.SystemSandbox(ctx, envID, kind, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Sandbox{}, runtime.StatusAbsent, nil
	}
	if err != nil {
		return store.Sandbox{}, "", err
	}
	st, err := m.Runtime.Status(ctx, VMName(rec))
	return rec, st, err
}

// StopSystem stops the system VM of kind and name. A missing VM is not an error.
func (m *Manager) StopSystem(ctx context.Context, envID, kind, name string) error {
	rec, err := m.Store.SystemSandbox(ctx, envID, kind, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return err
	}
	defer unlock()
	return m.Runtime.Stop(ctx, VMName(rec))
}

// RemoveSystem deletes the system VM of kind and name and its row. A missing VM is not an error.
func (m *Manager) RemoveSystem(ctx context.Context, envID, kind, name string) error {
	rec, err := m.Store.SystemSandbox(ctx, envID, kind, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.Runtime.Remove(ctx, VMName(rec)); err != nil {
		return err
	}
	m.Hub.Close(rec.ID)
	m.Egress.Detach(rec.ID)
	if err := m.Store.DeleteSandbox(ctx, envID, rec.ID); err != nil {
		return err
	}
	m.forget(envID, rec.ID)
	return nil
}
