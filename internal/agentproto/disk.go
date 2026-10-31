package agentproto

// KindDisk (host → guest) asks how full the sandbox's disks are: no request line, and the
// guest replies with a []Disk line, or an Error. A guest older than v0.10.1 replies with
// an Error.
const KindDisk = "disk"

// Disk names.
const (
	DiskHome      = "home"      // the terminal user's home: harness state, caches, snapshots
	DiskWorkspace = "workspace" // /workspace
	DiskDocker    = "docker"    // /var/lib/docker: images, containers, volumes, build cache
)

// DockerDir is where the Docker disk is mounted.
const DockerDir = "/var/lib/docker"

// With TransferURLEnv set, `studio-agent <tree>-export` POSTs its archive there instead of
// writing it to stdout, trusting the CA certificate in TransferCAEnv.
const (
	TransferURLEnv = "STUDIO_TRANSFER_URL"
	TransferCAEnv  = "STUDIO_TRANSFER_CA"
)

// Disk is one of the sandbox's disks, in bytes, as the terminal user sees it: Free leaves
// out what only root may use.
type Disk struct {
	Name  string `json:"name" enum:"home,workspace,docker"`
	Free  int64  `json:"free"`
	Total int64  `json:"total"`
}

// Low says the disk is nearly full: under a tenth of it, up to 512 MiB, is free. Agents
// write their sessions to the home disk, and stop when it fills.
func (d Disk) Low() bool { return d.Total > 0 && d.Free < min(d.Total/10, 512<<20) }

// Full says too little is free for an agent to write its session: under 64 MiB, or a
// fiftieth of a smaller disk.
func (d Disk) Full() bool { return d.Total > 0 && d.Free < min(d.Total/50, 64<<20) }
