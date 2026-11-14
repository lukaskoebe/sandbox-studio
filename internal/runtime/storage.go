package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
)

// StorageState is how far msb 0.7.12 and later have moved a VM's disks to the layout they
// keep them in, which checkpoints, forks and disk resizes need.
type StorageState struct {
	VM string
	// Status is msb's: "ready"; "eligible", moved when the VM next starts; "in_progress";
	// "maintenance_required", on macOS moved only by msb self repair; "recovery_required";
	// or "unavailable".
	Status  string
	Running bool
	Reasons []string
}

// StorageStates asks msb how far it has moved the disks of each VM whose name starts with
// prefix, and returns those that aren't ready. It only reads.
func StorageStates(ctx context.Context, prefix string) ([]StorageState, error) {
	home, err := msbdb.Home()
	if err != nil {
		return nil, err
	}
	d, err := msb.DiagnoseStorage(ctx, msb.RuntimeConfig{Home: home}, msb.StorageSelection{All: true})
	if err != nil {
		return nil, catalogError(err)
	}
	var out []StorageState
	for _, s := range d.Sandboxes {
		if !strings.HasPrefix(s.Name, prefix) || s.Status == msb.StorageReady {
			continue
		}
		st := StorageState{VM: s.Name, Status: string(s.Status), Running: s.Running}
		if s.Reason != nil && *s.Reason != "" {
			st.Reasons = append(st.Reasons, *s.Reason)
		}
		if s.Inspection != nil {
			st.Reasons = append(st.Reasons, s.Inspection.Reasons...)
		}
		out = append(out, st)
	}
	return out, nil
}

// layoutFile is where msb records how far it has moved a VM's disks to its protected layout.
const layoutFile = "state-layout.json"

// unfinishedLayout returns why the VM directory dir holds disks msb was part way through
// moving to its protected layout, or "": a new record around them would boot a half-moved
// layout.
func unfinishedLayout(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, layoutFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var layout struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(b, &layout); err != nil {
		return fmt.Sprintf("its %s can't be read", layoutFile), nil
	}
	if layout.Phase != "ready" {
		return fmt.Sprintf("microsandbox was moving its disks to its new layout (phase %q)", layout.Phase), nil
	}
	return "", nil
}
