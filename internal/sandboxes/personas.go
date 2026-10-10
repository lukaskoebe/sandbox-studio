package sandboxes

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// CreateFromTemplate starts a fresh, unowned sandbox from a template; see
// CreateFromTemplateFor.
func (m *Manager) CreateFromTemplate(ctx context.Context, envID, templateID, name string) (View, error) {
	return m.CreateFromTemplateFor(ctx, envID, templateID, name, "")
}

// createOwnedRecord inserts the catalog record of a new sandbox. personaID, if set, must be
// a persona of the environment; ownership never changes afterwards.
func (m *Manager) createOwnedRecord(ctx context.Context, envID, name string, resolved resources.Resources, templateID, personaID string) (store.Sandbox, error) {
	return m.Store.CreateSandbox(ctx, store.Sandbox{
		EnvironmentID: envID,
		TemplateID:    templateID,
		PersonaID:     personaID,
		Name:          name,
		CPUs:          int(resolved.CPUs),
		MemoryMiB:     int(resolved.MemoryMiB),
		MaxMemoryMiB:  int(resolved.MaxMemoryMiB),
		WorkspaceMiB:  int(resolved.WorkspaceMiB),
		DockerMiB:     int(resolved.DockerMiB),
	})
}
