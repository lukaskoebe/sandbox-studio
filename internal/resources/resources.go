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
	// DefaultHomeMiB is the home disk's size where HomeMiB is 0.
	DefaultHomeMiB = 4096
)

const maxMiBValue = int64(1<<32 - 1)

// Resources describes a sandbox's initial and maximum resource allocation.
type Resources struct {
	CPUs         int64 `json:"cpus"`
	MemoryMiB    int64 `json:"memoryMiB"`
	MaxMemoryMiB int64 `json:"maxMemoryMiB"`
	WorkspaceMiB int64 `json:"workspaceMiB"`
	DockerMiB    int64 `json:"dockerMiB"`
	// HomeMiB is the home disk's size, for a sandbox that has one; 0 is DefaultHomeMiB. It
	// stays 0 unless set, so a template spec that doesn't name it keeps its cache key.
	HomeMiB int64 `json:"homeMiB,omitempty"`
}

// Home is the home disk's size: HomeMiB, or DefaultHomeMiB where it is 0.
func (r Resources) Home() int64 {
	if r.HomeMiB == 0 {
		return DefaultHomeMiB
	}
	return r.HomeMiB
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
	if err := r.ValidateSize(); err != nil {
		return err
	}
	return r.ValidateDisks()
}

// ValidateDisks checks the disk sizes: the part of the resources a disk resize changes.
func (r Resources) ValidateDisks() error {
	if r.WorkspaceMiB < 1024 {
		return fmt.Errorf("%w: workspaceMiB must be at least 1024", ErrInvalid)
	}
	if r.DockerMiB < 1024 {
		return fmt.Errorf("%w: dockerMiB must be at least 1024", ErrInvalid)
	}
	if r.HomeMiB != 0 && r.HomeMiB < 1024 {
		return fmt.Errorf("%w: homeMiB must be at least 1024", ErrInvalid)
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"workspaceMiB", r.WorkspaceMiB},
		{"dockerMiB", r.DockerMiB},
		{"homeMiB", r.HomeMiB},
	} {
		if field.value > maxMiBValue {
			return fmt.Errorf("%w: %s must not exceed %d", ErrInvalid, field.name, maxMiBValue)
		}
	}
	return nil
}

// ValidateSize checks the CPU count and the memory size and ceiling: the part of the resources
// a resize changes. Validate checks the rest as well.
func (r Resources) ValidateSize() error {
	if r.CPUs < 1 || r.CPUs > 64 {
		return fmt.Errorf("%w: cpus must be between 1 and 64", ErrInvalid)
	}
	if r.MemoryMiB < 512 {
		return fmt.Errorf("%w: memoryMiB must be at least 512", ErrInvalid)
	}
	if r.MaxMemoryMiB < r.MemoryMiB {
		return fmt.Errorf("%w: maxMemoryMiB must be at least memoryMiB", ErrInvalid)
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"memoryMiB", r.MemoryMiB},
		{"maxMemoryMiB", r.MaxMemoryMiB},
	} {
		if field.value > maxMiBValue {
			return fmt.Errorf("%w: %s must not exceed %d", ErrInvalid, field.name, maxMiBValue)
		}
	}
	return nil
}
