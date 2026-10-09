package resources

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	want := Resources{CPUs: 2, MemoryMiB: 4096, MaxMemoryMiB: 4096, WorkspaceMiB: 20480, DockerMiB: 20480}
	if got := Defaults(); got != want {
		t.Fatalf("Defaults() = %+v, want %+v", got, want)
	}
}

func TestValidateBoundaries(t *testing.T) {
	min := Resources{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024}
	if err := min.Validate(); err != nil {
		t.Fatalf("minimum resources rejected: %v", err)
	}
	maxMiB := int64(1<<32 - 1)
	max := Resources{CPUs: 64, MemoryMiB: maxMiB, MaxMemoryMiB: maxMiB, WorkspaceMiB: maxMiB, DockerMiB: maxMiB}
	if err := max.Validate(); err != nil {
		t.Fatalf("maximum resources rejected: %v", err)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	base := Resources{CPUs: 2, MemoryMiB: 2048, MaxMemoryMiB: 4096, WorkspaceMiB: 1024, DockerMiB: 1024}
	tests := []struct {
		name, field string
		change      func(*Resources)
	}{
		{"zero CPUs", "cpus", func(r *Resources) { r.CPUs = 0 }},
		{"negative CPUs", "cpus", func(r *Resources) { r.CPUs = -1 }},
		{"too many CPUs", "cpus", func(r *Resources) { r.CPUs = 65 }},
		{"zero memory", "memoryMiB", func(r *Resources) { r.MemoryMiB = 0 }},
		{"negative memory", "memoryMiB", func(r *Resources) { r.MemoryMiB = -1 }},
		{"zero max memory", "maxMemoryMiB", func(r *Resources) { r.MaxMemoryMiB = 0 }},
		{"negative max memory", "maxMemoryMiB", func(r *Resources) { r.MaxMemoryMiB = -1 }},
		{"max below initial", "maxMemoryMiB", func(r *Resources) { r.MaxMemoryMiB = 1024 }},
		{"zero workspace", "workspaceMiB", func(r *Resources) { r.WorkspaceMiB = 0 }},
		{"negative workspace", "workspaceMiB", func(r *Resources) { r.WorkspaceMiB = -1 }},
		{"zero docker disk", "dockerMiB", func(r *Resources) { r.DockerMiB = 0 }},
		{"negative docker disk", "dockerMiB", func(r *Resources) { r.DockerMiB = -1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := base
			tc.change(&got)
			err := got.Validate()
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %v; want ErrInvalid mentioning %s", err, tc.field)
			}
		})
	}

	maxMiB := int64(1<<32 - 1)
	for _, tc := range []struct {
		field string
		set   func(*Resources)
	}{
		{"memoryMiB", func(r *Resources) { r.MemoryMiB = maxMiB + 1; r.MaxMemoryMiB = maxMiB + 1 }},
		{"maxMemoryMiB", func(r *Resources) { r.MaxMemoryMiB = maxMiB + 1 }},
		{"workspaceMiB", func(r *Resources) { r.WorkspaceMiB = maxMiB + 1 }},
		{"dockerMiB", func(r *Resources) { r.DockerMiB = maxMiB + 1 }},
	} {
		t.Run("overflow "+tc.field, func(t *testing.T) {
			got := base
			tc.set(&got)
			err := got.Validate()
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %v; want ErrInvalid mentioning %s", err, tc.field)
			}
		})
	}
}
