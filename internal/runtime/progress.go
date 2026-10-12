package runtime

import msb "github.com/superradcompany/microsandbox/sdk/go"

// Creation phases, reported through Spec.Progress in this order. The SDK may skip pull
// events for an image it already has.
const (
	PhasePull = "pull" // pulling and unpacking the image
	PhaseVM   = "vm"   // starting the VM
	PhaseBoot = "boot" // running `studio-agent boot`; not reported in transfer mode
)

// Progress is one report of a VM's creation.
type Progress struct {
	Phase string
	Pull  PullProgress // the pull so far, in PhasePull
}

// PullProgress counts an image pull. Totals are zero while unknown.
type PullProgress struct {
	Layers     int    // layers in the image
	LayersDone int    // layers downloaded
	Bytes      uint64 // bytes downloaded
	TotalBytes uint64 // bytes to download
}

// pullCounter folds the SDK's per-layer creation events into totals.
type pullCounter struct {
	bytes map[uint64]uint64 // layer index → bytes downloaded
	done  map[uint64]bool
	pull  PullProgress
}

// observe returns the progress an SDK event amounts to, or false for an event that
// changes nothing Studio shows.
func (c *pullCounter) observe(e msb.CreationProgress) (Progress, bool) {
	switch e.Kind {
	case "startup":
		return Progress{Phase: PhaseVM}, true
	case "pull":
	default:
		return Progress{}, false
	}
	if c.bytes == nil {
		c.bytes, c.done = map[uint64]uint64{}, map[uint64]bool{}
	}
	d := e.Progress
	switch d.Kind {
	case "resolved":
		if d.LayerCount != nil {
			c.pull.Layers = int(*d.LayerCount)
		}
		if d.TotalDownloadBytes != nil {
			c.pull.TotalBytes = *d.TotalDownloadBytes
		}
	case "layer_download_progress", "layer_download_complete":
		if d.LayerIndex == nil {
			return Progress{}, false
		}
		c.bytes[*d.LayerIndex] = d.DownloadedBytes
		if d.Kind == "layer_download_complete" {
			c.done[*d.LayerIndex] = true
		}
	case "complete":
		return Progress{Phase: PhaseVM}, true
	default:
		return Progress{}, false
	}
	c.pull.Bytes = 0
	for _, n := range c.bytes {
		c.pull.Bytes += n
	}
	c.pull.LayersDone = len(c.done)
	return Progress{Phase: PhasePull, Pull: c.pull}, true
}
