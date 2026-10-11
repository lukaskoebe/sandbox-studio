package sandboxes

import (
	"context"
	"errors"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A teammate's browser VM (PLAN §6.8, internal/browser) is a sandbox row of kind browser.
// It runs the browser image, is created on the first browser call, stopped when idle and
// never shown or reachable as a sandbox: Public hides it from the sandbox API, terminals
// and previews.

// browserResources size a browser VM: Chromium wants memory, its profile little disk, and
// it runs no Docker.
var browserResources = resources.Resources{CPUs: 2, MemoryMiB: 2048, MaxMemoryMiB: 2048, WorkspaceMiB: 2048, DockerMiB: 256}

// browserReady bounds the wait for a booting browser VM's guest agent.
const browserReady = 90 * time.Second

// BrowserName is the name of a teammate's browser VM row.
func BrowserName(teammateID string) string { return "browser-" + teammateID }

// EnsureBrowser returns the teammate's browser VM with its guest agent connected: it creates
// the VM from image on first use and starts it when stopped.
func (m *Manager) EnsureBrowser(ctx context.Context, envID, teammateID, image string) (store.Sandbox, error) {
	rec, err := m.Store.BrowserSandbox(ctx, envID, teammateID)
	if errors.Is(err, store.ErrNotFound) {
		rec, err = m.createBrowser(ctx, envID, teammateID, image)
	}
	if err != nil {
		return store.Sandbox{}, err
	}
	if _, ok := m.Hub.Connected(rec.ID); ok {
		return rec, nil
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return store.Sandbox{}, err
	}
	defer unlock()
	status, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return store.Sandbox{}, err
	}
	if _, err := m.Egress.Attach(rec); err != nil {
		return store.Sandbox{}, err
	}
	if err := m.Hub.Listen(rec.ID, m.Paths.AgentSocket(rec.ID)); err != nil {
		return store.Sandbox{}, err
	}
	switch status {
	case runtime.StatusSuspended:
		err = m.Runtime.Resume(ctx, VMName(rec))
	case runtime.StatusRunning, runtime.StatusStarting:
	default:
		err = m.Runtime.Start(ctx, VMName(rec))
	}
	if err != nil {
		return store.Sandbox{}, err
	}
	return rec, m.WaitReady(ctx, rec.ID, browserReady)
}

func (m *Manager) createBrowser(ctx context.Context, envID, teammateID, image string) (store.Sandbox, error) {
	r := browserResources
	rec, err := m.Store.CreateBrowserSandbox(ctx, store.Sandbox{
		EnvironmentID: envID, TeammateID: teammateID, Name: BrowserName(teammateID),
		CPUs: int(r.CPUs), MemoryMiB: int(r.MemoryMiB), MaxMemoryMiB: int(r.MaxMemoryMiB),
		WorkspaceMiB: int(r.WorkspaceMiB), DockerMiB: int(r.DockerMiB),
	})
	if errors.Is(err, store.ErrExists) {
		// Another call created it first.
		return m.Store.BrowserSandbox(ctx, envID, teammateID)
	}
	if err != nil {
		return store.Sandbox{}, err
	}
	unlock, err := m.tryMutation(rec.ID)
	if err != nil {
		return store.Sandbox{}, err
	}
	defer unlock()
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), envID, rec.ID)
		return store.Sandbox{}, err
	}
	sock := m.Paths.AgentSocket(rec.ID)
	if err := m.Hub.Listen(rec.ID, sock); err != nil {
		m.Egress.Detach(rec.ID)
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), envID, rec.ID)
		return store.Sandbox{}, err
	}
	spec := runtime.Spec{
		CPUs: uint8(r.CPUs), MemoryMiB: uint32(r.MemoryMiB), MaxMemoryMiB: uint32(r.MaxMemoryMiB),
		WorkspaceMiB: uint32(r.WorkspaceMiB), DockerMiB: uint32(r.DockerMiB),
		BaseImage: image, Egress: egress,
	}
	if err := m.Runtime.Create(ctx, VMName(rec), spec, sock, sandboxLabels(rec)); err != nil {
		m.Hub.Close(rec.ID)
		m.Egress.Detach(rec.ID)
		m.Runtime.Remove(context.WithoutCancel(ctx), VMName(rec))
		m.Store.DeleteSandbox(context.WithoutCancel(ctx), envID, rec.ID)
		return store.Sandbox{}, err
	}
	return rec, nil
}

// BrowserStatus returns the teammate's browser VM and its state; the VM may not exist.
func (m *Manager) BrowserStatus(ctx context.Context, envID, teammateID string) (store.Sandbox, runtime.Status, error) {
	rec, err := m.Store.BrowserSandbox(ctx, envID, teammateID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Sandbox{}, runtime.StatusAbsent, nil
	}
	if err != nil {
		return store.Sandbox{}, "", err
	}
	st, err := m.Runtime.Status(ctx, VMName(rec))
	return rec, st, err
}

// StopBrowser stops the teammate's browser VM, keeping its profile disk.
func (m *Manager) StopBrowser(ctx context.Context, envID, teammateID string) error {
	rec, err := m.Store.BrowserSandbox(ctx, envID, teammateID)
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

// RemoveBrowser deletes the teammate's browser VM, its profile (cookies, logins) and its
// row. The teammate can then be deleted.
func (m *Manager) RemoveBrowser(ctx context.Context, envID, teammateID string) error {
	rec, err := m.Store.BrowserSandbox(ctx, envID, teammateID)
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
