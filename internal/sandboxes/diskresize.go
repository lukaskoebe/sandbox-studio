package sandboxes

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

var (
	// ErrDockerTooLarge means Docker's data does not fit the new Docker disk.
	ErrDockerTooLarge = errors.New("Docker's images, containers and volumes do not fit the new Docker disk; remove what you no longer need (docker system prune), or pick a bigger size, and try again")
	// ErrNoHomeDisk means a resize was asked of a sandbox whose home is on its root disk, as
	// with a template built before the home directory existed. A resize replaces the root
	// disk and copies only what is on the three disks, so that home would be lost.
	ErrNoHomeDisk = fmt.Errorf("%w: this sandbox keeps its home on its root disk, which a resize replaces, so its home would be lost; rebase it onto a rebuilt template first, which gives it a home disk", resources.ErrInvalid)
)

// DiskSizes are new sizes for a sandbox's disks. A size left at 0 stays as it is.
type DiskSizes struct {
	WorkspaceMiB int `json:"workspaceMiB,omitempty" doc:"The size of /workspace in MiB, at least 1024"`
	DockerMiB    int `json:"dockerMiB,omitempty" doc:"The size of Docker's disk in MiB, at least 1024"`
	HomeMiB      int `json:"homeMiB,omitempty" doc:"The size of /home/agent in MiB, at least 1024"`
}

// ResizeDisks gives a sandbox's workspace, Docker and home disks new sizes. A disk can't
// change its size in place, so this is a rebase onto the sandbox's own template, or onto
// the default image for a sandbox without one, that keeps its CPUs and memory and copies
// Docker's images, containers and volumes as well as the workspace and the home. A disk can
// shrink as long as what it holds fits. As with a rebase, what was installed outside the
// three disks is lost and tmux sessions end, the sandbox keeps its identity, and the history
// records a disks event. It answers at once with the sandbox in state creating; Wait returns
// the outcome, and a failed resize leaves the sandbox as it was. A sandbox with no home disk
// is refused: its home is on the root disk, which the resize would replace.
func (m *Manager) ResizeDisks(ctx context.Context, envID, id string, sizes DiskSizes) (View, error) {
	// The same template's VM again, so everything on the old disks comes along, the tools too.
	return m.startRebase(ctx, envID, id, OperationDisks, RebaseOptions{KeepTools: true, KeepDocker: true}, func(sb store.Sandbox) (store.RebaseOperation, error) {
		if sb.HomeMiB == 0 {
			return store.RebaseOperation{}, ErrNoHomeDisk
		}
		workspace, docker := cmp.Or(sizes.WorkspaceMiB, sb.WorkspaceMiB), cmp.Or(sizes.DockerMiB, sb.DockerMiB)
		home := cmp.Or(sizes.HomeMiB, sb.HomeMiB)
		disks := resources.Resources{WorkspaceMiB: int64(workspace), DockerMiB: int64(docker), HomeMiB: int64(home)}
		if err := disks.ValidateDisks(); err != nil {
			return store.RebaseOperation{}, err
		}
		if workspace == sb.WorkspaceMiB && docker == sb.DockerMiB && home == sb.HomeMiB {
			return store.RebaseOperation{}, fmt.Errorf("%w: the disks already have these sizes", resources.ErrInvalid)
		}
		return store.RebaseOperation{
			Kind: store.RebaseDisks, SandboxID: sb.ID, EnvironmentID: envID, TemplateID: sb.TemplateID,
			CPUs: sb.CPUs, MemoryMiB: sb.MemoryMiB, MaxMemoryMiB: sb.MaxMemoryMiB,
			WorkspaceMiB: workspace, DockerMiB: docker, HomeMiB: home,
			BaseImage: m.BaseImage, BaseVersion: m.BaseVersion,
		}, nil
	})
}
