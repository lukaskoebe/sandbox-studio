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

// NeedsMove reports whether msb, with a VM's storage at status, won't start or copy the VM
// until its disks are moved: on macOS only an explicit recovery moves disks of the layout
// before 0.7.12 (maintenance_required), and anywhere one that msb found damaged or half moved.
func NeedsMove(status string) bool {
	return status == string(msb.StorageMaintenanceRequired) || status == string(msb.StorageRecoveryRequired)
}

// ErrMoveDisks means msb keeps a sandbox's disks in its layout before 0.7.12 and won't start or
// copy the sandbox until they are moved.
var ErrMoveDisks = errors.New("microsandbox must move this sandbox's disks to its new layout before it starts or copies it: " +
	"Move disks, on Home or Computers, does that and keeps the originals")

// moveError returns err as ErrMoveDisks if msb refused to start or copy a VM whose disks it has
// yet to move, else err.
func moveError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "legacy storage requires explicit maintenance recovery") ||
		strings.Contains(msg, "migration/recovery required") {
		return fmt.Errorf("%w (%v)", ErrMoveDisks, err)
	}
	return err
}

// StorageStates is the package's StorageStates, for the sandboxes' manager.
func (*Runtime) StorageStates(ctx context.Context, prefix string) ([]StorageState, error) {
	return StorageStates(ctx, prefix)
}

// MoveDisks has msb move the disks of the stopped VM name to the layout it keeps them in, keeping
// the originals. It never starts or stops a VM. On macOS it tells msb what only the person can
// vouch for: that no VM or msb from before the upgrade runs, here or in another msb home, and that
// none starts until the move is done. Studio holds the sandboxes meanwhile.
func (*Runtime) MoveDisks(ctx context.Context, name string) error {
	r, err := msb.RecoverStorage(ctx, name, msb.StorageRecoveryOptions{LegacyWritersStopped: true})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "cannot exclude a writer"):
			if writer := writerOf(msg); writer != "" && writer != name {
				msbCmd := "msb"
				if path, err := MSBPath(); err == nil {
					msbCmd = path
				}
				return fmt.Errorf("microsandbox counts the VM %s as still running, so it can't rule out that something writes to the disks of %s. "+
					"If no sandbox uses %s (`studio doctor` lists such leftover VMs), remove it with `%s remove --force %s`; "+
					"otherwise stop it, and any Studio or msb from before the update, then try again (%w)", writer, name, writer, msbCmd, writer, err)
			}
			return fmt.Errorf("microsandbox sees something still writing to the disks of %s: stop every sandbox, "+
				"and any Studio or msb from before the update, then try again (%w)", name, err)
		case strings.Contains(msg, "select one complete view"), strings.Contains(msg, "missing recovery selection"),
			strings.Contains(msg, "ambiguous"):
			return fmt.Errorf("microsandbox found more than one version of the disks of %s, so someone has to pick: "+
				"run `msb self repair --sandbox %s` in a terminal, which shows them (%w)", name, name, err)
		}
		return catalogError(err)
	}
	if !r.Ready {
		return fmt.Errorf("microsandbox did not finish moving the disks of %s; run `msb self repair --sandbox %s` in a terminal", name, name)
	}
	return nil
}

// writerOf is the VM msb's "cannot exclude a writer in sandbox 'NAME'" names, or "".
func writerOf(msg string) string {
	_, rest, ok := strings.Cut(msg, "cannot exclude a writer in sandbox '")
	if !ok {
		return ""
	}
	writer, _, ok := strings.Cut(rest, "'")
	if !ok {
		return ""
	}
	return writer
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
