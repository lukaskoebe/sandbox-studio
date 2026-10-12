package runtime

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestPullCounterFoldsLayerEvents(t *testing.T) {
	u := func(n uint64) *uint64 { return &n }
	pull := func(d msb.CreationProgressDetail) msb.CreationProgress {
		return msb.CreationProgress{Kind: "pull", Progress: d}
	}
	var c pullCounter
	steps := []struct {
		event msb.CreationProgress
		ok    bool
		want  Progress
	}{
		{pull(msb.CreationProgressDetail{Kind: "resolving", Reference: "img"}), false, Progress{}},
		{pull(msb.CreationProgressDetail{Kind: "resolved", LayerCount: u(2), TotalDownloadBytes: u(300)}), true,
			Progress{Phase: PhasePull, Pull: PullProgress{Layers: 2, TotalBytes: 300}}},
		{pull(msb.CreationProgressDetail{Kind: "layer_download_progress", LayerIndex: u(0), DownloadedBytes: 50}), true,
			Progress{Phase: PhasePull, Pull: PullProgress{Layers: 2, Bytes: 50, TotalBytes: 300}}},
		{pull(msb.CreationProgressDetail{Kind: "layer_download_progress", LayerIndex: u(1), DownloadedBytes: 20}), true,
			Progress{Phase: PhasePull, Pull: PullProgress{Layers: 2, Bytes: 70, TotalBytes: 300}}},
		{pull(msb.CreationProgressDetail{Kind: "layer_download_complete", LayerIndex: u(0), DownloadedBytes: 100}), true,
			Progress{Phase: PhasePull, Pull: PullProgress{Layers: 2, LayersDone: 1, Bytes: 120, TotalBytes: 300}}},
		// Cumulative events repeat; a layer counts once.
		{pull(msb.CreationProgressDetail{Kind: "layer_download_complete", LayerIndex: u(0), DownloadedBytes: 100}), true,
			Progress{Phase: PhasePull, Pull: PullProgress{Layers: 2, LayersDone: 1, Bytes: 120, TotalBytes: 300}}},
		{pull(msb.CreationProgressDetail{Kind: "layer_download_progress"}), false, Progress{}},
		{pull(msb.CreationProgressDetail{Kind: "layer_materialize_started", LayerIndex: u(0)}), false, Progress{}},
		{pull(msb.CreationProgressDetail{Kind: "complete", LayerCount: u(2)}), true, Progress{Phase: PhaseVM}},
		{msb.CreationProgress{Kind: "startup", Progress: msb.CreationProgressDetail{Phase: "activating"}}, true, Progress{Phase: PhaseVM}},
		{msb.CreationProgress{Kind: "unknown"}, false, Progress{}},
	}
	for i, s := range steps {
		got, ok := c.observe(s.event)
		if ok != s.ok || got != s.want {
			t.Fatalf("event %d (%s/%s) = %+v, %v; want %+v, %v", i, s.event.Kind, s.event.Progress.Kind, got, ok, s.want, s.ok)
		}
	}
}
