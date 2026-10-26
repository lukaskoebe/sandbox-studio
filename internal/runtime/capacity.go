package runtime

import goruntime "runtime"

// Boot-time capacity. msb 0.7.8 resizes a running VM's CPUs and memory live up to the CPU
// maximum and the memory ceiling it booted with, and not past them, so Studio boots each VM
// with room to grow. Measured with spikes/live-resize on an idle 1-CPU, 512 MiB VM: a memory
// ceiling of 8 GiB cost the host nothing, in memory or CPU, but every spare vCPU about 1.1%
// of a host core, since its thread keeps polling while the vCPU is offline. So the memory
// ceiling is twice the memory, at most the host's, and the CPU maximum the CPUs plus as many
// again, at most maxSpareCPUs more and at most the host's cores.
const maxSpareCPUs = 4

// hostCapacity is the host's CPU count and memory in MiB; the memory is 0 when unknown.
var hostCapacity = func() (int, uint64) { return goruntime.NumCPU(), hostMemoryMiB() }

// bootCapacity is the CPU maximum and memory ceiling a VM of cpus and memoryMiB boots with,
// never below maxMemoryMiB, the ceiling asked for.
func bootCapacity(cpus uint8, memoryMiB, maxMemoryMiB uint32) (uint8, uint32) {
	hostCPUs, hostMemory := hostCapacity()
	maxCPUs := max(cpus, uint8(min(int(cpus)+min(int(cpus), maxSpareCPUs), hostCPUs, 255)))
	ceiling := 2 * uint64(memoryMiB)
	if hostMemory > 0 {
		ceiling = min(ceiling, hostMemory)
	}
	ceiling = min(ceiling, 1<<32-1)
	return maxCPUs, max(memoryMiB, maxMemoryMiB, uint32(ceiling))
}
