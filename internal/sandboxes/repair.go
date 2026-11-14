package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// vmAdopter is the runtime surface for repairing a sandbox whose VM msb's catalog lost: when its
// database was damaged and rebuilt from an older copy, or from what could still be read.
type vmAdopter interface {
	LostDisks(name string) (string, error)
	AdoptDisks(ctx context.Context, name string, spec runtime.Spec, agentSocket string, labels map[string]string) error
}

var _ vmAdopter = (*runtime.Runtime)(nil)

// repairTimeout bounds a repair: msb may have to pull the image again before it makes the record.
const repairTimeout = 15 * time.Minute

// repairable reports whether msb lost the record of v's VM while its disks are still on hand,
// so RepairVM can give it a new one.
func (m *Manager) repairable(v View) bool {
	if v.Status != runtime.StatusAbsent || v.Progress != nil {
		return false
	}
	ad, ok := m.Runtime.(vmAdopter)
	if !ok {
		return false
	}
	_, err := ad.LostDisks(VMName(v.Sandbox))
	return err == nil
}

// RepairVM gives a sandbox whose VM msb's catalog lost a new record around the disks msb kept,
// the way it made the VM, and starts it. Nothing on the disks is copied or changed.
func (m *Manager) RepairVM(ctx context.Context, envID, id string) (View, error) {
	defer m.publish(envID, id)
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return View{}, err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return View{}, err
	}
	defer unlock()
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return View{}, err
	}
	rec, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return View{}, err
	}
	if m.failedCreation(id) {
		return View{}, lifecycleError("the sandbox could not be created; delete it")
	}
	ad, ok := m.Runtime.(vmAdopter)
	if !ok {
		return View{}, lifecycleError("this runtime can't repair a sandbox's VM")
	}
	status, err := m.Runtime.Status(ctx, VMName(rec))
	if err != nil {
		return View{}, err
	}
	if status != runtime.StatusAbsent {
		return View{}, lifecycleError("microsandbox has this sandbox's VM, so there is nothing to repair")
	}
	if _, err := ad.LostDisks(VMName(rec)); errors.Is(err, runtime.ErrNoDisks) {
		return View{}, lifecycleError("this sandbox's disks are gone too, so it can't be repaired; delete it")
	} else if err != nil {
		return View{}, err
	}
	spec, sock, err := m.repairSpec(ctx, rec)
	if err != nil {
		return View{}, err
	}
	// The request may go away; a repair stopped half way leaves the disks aside for another try.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repairTimeout)
	defer cancel()
	m.Log.Warn("repairing a sandbox whose VM microsandbox lost", "sandbox", rec.ID, "vm", VMName(rec))
	if err := ad.AdoptDisks(ctx, VMName(rec), spec, sock, sandboxLabels(rec)); err != nil {
		m.Log.Error("repair a sandbox's VM", "sandbox", rec.ID, "vm", VMName(rec), "err", err)
		return View{}, fmt.Errorf("repair the VM: %w", err)
	}
	m.Log.Info("repaired a sandbox's VM; starting it", "sandbox", rec.ID, "vm", VMName(rec))
	m.vmsChanged()
	if rec, err = m.startVM(ctx, rec); err != nil {
		return View{}, fmt.Errorf("the VM has its disks back but did not start: %w", err)
	}
	return m.view(ctx, rec)
}

// repairSpec is the spec rec's VM was made with, and its agent's socket: the same disks, and
// the same image, so its root disk's changes still lie over the files they were made on.
func (m *Manager) repairSpec(ctx context.Context, rec store.Sandbox) (runtime.Spec, string, error) {
	var image *runtime.ImageSource
	if rec.TemplateID != "" {
		var err error
		if image, err = m.templateImage(ctx, rec.EnvironmentID, rec.TemplateID); err != nil {
			return runtime.Spec{}, "", err
		}
	}
	egress, err := m.Egress.Attach(rec)
	if err != nil {
		return runtime.Spec{}, "", err
	}
	spec := transferSpec(rec, image, egress)
	if image == nil {
		spec.BaseImage = rec.BaseImage
	}
	return spec, m.Paths.AgentSocket(rec.ID), nil
}

func (m *Manager) vmsChanged() {
	if m.VMsChanged != nil {
		m.VMsChanged()
	}
}
