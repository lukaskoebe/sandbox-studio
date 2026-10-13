package runtime

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestReadResizePlan(t *testing.T) {
	change := func(field, disposition string) msb.PlannedChange {
		return msb.PlannedChange{Kind: "config", Field: field, Change: "updated", Disposition: disposition}
	}
	tests := []struct {
		name         string
		changes      []msb.PlannedChange
		cpus, memory resizeChange
	}{
		{name: "no change"},
		{
			name:    "running VM takes both changes live",
			changes: []msb.PlannedChange{change("cpus", "live"), change("memory", "live")},
			cpus:    resizeChange{changed: true, live: true}, memory: resizeChange{changed: true, live: true},
		},
		{
			name:    "memory above the boot-time ceiling waits for the next start",
			changes: []msb.PlannedChange{change("cpus", "live"), change("memory", "requires restart")},
			cpus:    resizeChange{changed: true, live: true}, memory: resizeChange{changed: true},
		},
		{
			name:    "a stopped sandbox saves both for its next start",
			changes: []msb.PlannedChange{change("cpus", "next start"), change("memory", "next start")},
			cpus:    resizeChange{changed: true}, memory: resizeChange{changed: true},
		},
		{
			name:    "a suspended sandbox cannot change live",
			changes: []msb.PlannedChange{change("cpus", "unsupported"), change("memory", "unsupported")},
			cpus:    resizeChange{changed: true}, memory: resizeChange{changed: true},
		},
		{
			name:    "fields other than CPUs and memory are ignored",
			changes: []msb.PlannedChange{change("max_memory", "requires restart"), change("root_disk_size", "live")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpus, memory := readResizePlan(tt.changes)
			if cpus != tt.cpus || memory != tt.memory {
				t.Fatalf("readResizePlan = cpus %+v memory %+v, want cpus %+v memory %+v", cpus, memory, tt.cpus, tt.memory)
			}
		})
	}
}
