package sandboxes

import (
	"context"
	"errors"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// BootBuildSandbox boots a worker-owned sandbox using the same network and agent setup as
// a public sandbox. The worker retains the catalog record on every error so it can clean up
// a VM or partially prepared host resources before releasing ownership in the store.
func (m *Manager) BootBuildSandbox(ctx context.Context, envID, jobID, sandboxID string, source runtime.ImageSource) error {
	unlock, err := m.tryMutation(sandboxID)
	if err != nil {
		return err
	}
	defer unlock()

	job, sb, err := m.buildSandbox(ctx, envID, jobID, sandboxID)
	if err != nil {
		return err
	}
	if job.Status != store.BuildSettingUp {
		return fmt.Errorf("build job %s is not setting up: %w", jobID, store.ErrConflict)
	}
	if source.Username != envID {
		return errors.New("build image source is not scoped to the environment")
	}

	res := buildResources(sb)
	if err := res.Validate(); err != nil {
		return err
	}
	egress, err := m.Egress.Attach(sb)
	if err != nil {
		return err
	}
	socket := m.Paths.AgentSocket(sb.ID)
	if err := m.Hub.Listen(sb.ID, socket); err != nil {
		return err
	}
	spec := runtime.Spec{
		Image:        &source,
		CPUs:         uint8(res.CPUs),
		MemoryMiB:    uint32(res.MemoryMiB),
		MaxMemoryMiB: uint32(res.MaxMemoryMiB),
		WorkspaceMiB: uint32(res.WorkspaceMiB),
		DockerMiB:    uint32(res.DockerMiB),
		Egress:       egress,
	}
	return m.Runtime.Create(ctx, VMName(sb), spec, socket, buildSandboxLabels(sb, envID, jobID))
}

// CleanupBuildSandbox removes only the VM whose full Studio ownership labels match this
// build job. The worker deletes the owned catalog row separately after cleanup succeeds.
func (m *Manager) CleanupBuildSandbox(ctx context.Context, envID, jobID, sandboxID string) error {
	unlock, err := m.tryMutation(sandboxID)
	if err != nil {
		return err
	}
	defer unlock()

	_, sb, err := m.buildSandbox(ctx, envID, jobID, sandboxID)
	if err != nil {
		return err
	}
	remover, ok := m.Runtime.(interface {
		RemoveOwned(context.Context, runtime.OwnedVM) error
	})
	if !ok {
		return errors.New("runtime does not support owned build sandbox cleanup")
	}
	owned := runtime.OwnedVM{Name: VMName(sb), Labels: buildSandboxLabels(sb, envID, jobID)}
	if err := remover.RemoveOwned(ctx, owned); err != nil {
		return err
	}
	m.Hub.Close(sb.ID)
	m.Egress.Detach(sb.ID)
	return nil
}

func (m *Manager) buildSandbox(ctx context.Context, envID, jobID, sandboxID string) (store.BuildJob, store.Sandbox, error) {
	job, err := m.Store.BuildJob(ctx, envID, jobID)
	if err != nil {
		return store.BuildJob{}, store.Sandbox{}, err
	}
	sb, err := m.Store.Sandbox(ctx, envID, sandboxID)
	if err != nil {
		return store.BuildJob{}, store.Sandbox{}, err
	}
	if job.ID != jobID || job.EnvironmentID != envID || job.SandboxID != sandboxID || sb.BuildJobID != jobID {
		return store.BuildJob{}, store.Sandbox{}, store.ErrNotFound
	}
	return job, sb, nil
}

func buildResources(sb store.Sandbox) resources.Resources {
	return resources.Resources{
		CPUs:         int64(sb.CPUs),
		MemoryMiB:    int64(sb.MemoryMiB),
		MaxMemoryMiB: int64(sb.MaxMemoryMiB),
		WorkspaceMiB: int64(sb.WorkspaceMiB),
		DockerMiB:    int64(sb.DockerMiB),
	}
}

func buildSandboxLabels(sb store.Sandbox, envID, jobID string) map[string]string {
	return map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": envID,
		"studio.sandbox-name":   sb.Name,
		"studio.build-job":      jobID,
	}
}
