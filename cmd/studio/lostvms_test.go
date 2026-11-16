package main

import (
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestFindLostVMs(t *testing.T) {
	started := time.Now()
	sbs := []store.Sandbox{
		{ID: "mjbm", Name: "katara", Generation: 2, StartedAt: &started}, // rebased; msb lost -g2
		{ID: "ok", Name: "fine", Generation: 1, StartedAt: &started},
		{ID: "new", Name: "being-made", Generation: 1},                 // never started, no disks
		{ID: "gone", Name: "gone", Generation: 3, StartedAt: &started}, // no record, no disks
	}
	statuses := map[string]runtime.Status{"ss-mjbm-g1": runtime.StatusStopped, "ss-ok-g1": runtime.StatusRunning}
	disks := func(vm string) (string, error) {
		if vm == "ss-mjbm-g2" {
			return "/msb/sandboxes/" + vm, nil
		}
		return "", runtime.ErrNoDisks
	}
	lost := findLostVMs(sbs, statuses, disks)
	if len(lost) != 2 {
		t.Fatalf("lost %+v, want katara and gone", lost)
	}
	if l := lost[0]; l.Sandbox != "katara" || l.VM != "ss-mjbm-g2" || !l.Disks || len(l.Known) != 1 || l.Known[0] != "ss-mjbm-g1" {
		t.Errorf("katara: %+v", l)
	}
	if l := lost[1]; l.Sandbox != "gone" || l.Disks || len(l.Known) != 0 {
		t.Errorf("gone: %+v", l)
	}
}

func TestFindLeftoverVMs(t *testing.T) {
	sbs := []store.Sandbox{
		{ID: "mjbm", Name: "katara", Generation: 2},
		{ID: "ok", Name: "fine", Generation: 1},
		{ID: "reb", Name: "rebasing", Generation: 1},
	}
	statuses := map[string]runtime.Status{
		"ss-mjbm-g1": runtime.StatusStopped, // katara's VM before its rebase
		"ss-mjbm-g2": runtime.StatusRunning,
		"ss-ok-g1":   runtime.StatusRunning,
		"ss-reb-g2":  runtime.StatusStarting, // a rebase's target, still being made
		"ss-xmeh-g1": runtime.StatusRunning,  // a failed fork's
		"ss-bad-gx":  runtime.StatusStopped,  // no name of Studio's
	}
	left := findLeftoverVMs(sbs, statuses)
	if len(left) != 2 {
		t.Fatalf("left %+v, want ss-mjbm-g1 and ss-xmeh-g1", left)
	}
	if l := left[0]; l.VM != "ss-mjbm-g1" || l.Sandbox != "katara" || l.Status != "stopped" {
		t.Errorf("katara's: %+v", l)
	}
	if l := left[1]; l.VM != "ss-xmeh-g1" || l.Sandbox != "" || l.Status != "running" {
		t.Errorf("the fork's: %+v", l)
	}
}
