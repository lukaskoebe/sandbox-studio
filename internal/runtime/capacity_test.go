package runtime

import "testing"

func TestBootCapacity(t *testing.T) {
	old := hostCapacity
	t.Cleanup(func() { hostCapacity = old })
	for _, tc := range []struct {
		name            string
		hostCPUs        int
		hostMemory      uint64
		cpus            uint8
		memory, ceiling uint32
		wantCPUs        uint8
		wantCeiling     uint32
	}{
		{"room to grow", 8, 32768, 2, 4096, 4096, 4, 8192},
		{"capped by the host's memory", 8, 6144, 2, 4096, 4096, 4, 6144},
		{"never below the memory", 8, 2048, 2, 4096, 4096, 4, 4096},
		{"never below the ceiling asked for", 8, 32768, 2, 4096, 16384, 4, 16384},
		{"at most four spare CPUs", 64, 0, 12, 4096, 0, 16, 8192},
		{"capped by the host's cores", 6, 0, 4, 4096, 0, 6, 8192},
		{"never below the CPUs", 4, 32768, 8, 1024, 0, 8, 2048},
	} {
		hostCapacity = func() (int, uint64) { return tc.hostCPUs, tc.hostMemory }
		cpus, ceiling := bootCapacity(tc.cpus, tc.memory, tc.ceiling)
		if cpus != tc.wantCPUs || ceiling != tc.wantCeiling {
			t.Errorf("%s: got %d CPUs, %d MiB; want %d, %d", tc.name, cpus, ceiling, tc.wantCPUs, tc.wantCeiling)
		}
	}
}
