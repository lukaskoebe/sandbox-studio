// Package resources defines the shared resource limits for Studio sandboxes.
package resources

import (
	"errors"
	"fmt"
)

// ErrInvalid identifies a resource configuration outside Studio's supported range.
var ErrInvalid = errors.New("invalid resources")

// Default resource values for new sandboxes.
const (
	DefaultCPUs         = 2
	DefaultMemoryMiB    = 4096
	DefaultWorkspaceMiB = 20480
	DefaultDockerMiB    = 20480
)

const maxMiBValue = int64(1<<32 - 1)

// Resources describes a sandbox's initial and maximum resource allocation.
type Resources struct {
	CPUs         int64 `json:"cpus"`
	MemoryMiB    int64 `json:"memoryMiB"`
	MaxMemoryMiB int64 `json:"maxMemoryMiB"`
	WorkspaceMiB int64 `json:"workspaceMiB"`
	DockerMiB    int64 `json:"dockerMiB"`
}

// Defaults returns Studio's default resources. The memory ceiling initially
// matches memory; callers may raise it explicitly for hotplug growth.
func Defaults() Resources {
	return Resources{
		CPUs: DefaultCPUs, MemoryMiB: DefaultMemoryMiB,
		MaxMemoryMiB: DefaultMemoryMiB,
		WorkspaceMiB: DefaultWorkspaceMiB, DockerMiB: DefaultDockerMiB,
	}
}

// Validate checks resolved values. It deliberately does not apply defaults.
func (r Resources) Validate() error {
	if r.CPUs < 1 || r.CPUs > 64 {
		return fmt.Errorf("%w: cpus must be between 1 and 64", ErrInvalid)
	}
	if r.MemoryMiB < 512 {
		return fmt.Errorf("%w: memoryMiB must be at least 512", ErrInvalid)
	}
	if r.MaxMemoryMiB < r.MemoryMiB {
		return fmt.Errorf("%w: maxMemoryMiB must be at least memoryMiB", ErrInvalid)
	}
	if r.WorkspaceMiB < 1024 {
		return fmt.Errorf("%w: workspaceMiB must be at least 1024", ErrInvalid)
	}
	if r.DockerMiB < 1024 {
		return fmt.Errorf("%w: dockerMiB must be at least 1024", ErrInvalid)
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"memoryMiB", r.MemoryMiB},
		{"maxMemoryMiB", r.MaxMemoryMiB},
		{"workspaceMiB", r.WorkspaceMiB},
		{"dockerMiB", r.DockerMiB},
	} {
		if field.value > maxMiBValue {
			return fmt.Errorf("%w: %s must not exceed %d", ErrInvalid, field.name, maxMiBValue)
		}
	}
	return nil
}
