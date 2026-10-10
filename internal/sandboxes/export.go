package sandboxes

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

// A .studio-sandbox file is the magic line, a big-endian uint32 manifest length, the
// manifest JSON and then the workspace archive exactly as workspace-export wrote it, to
// EOF. See docs/export-import.md.
const (
	ExportMagic        = "sandbox-studio-export v1\n"
	ExportFormat       = 1
	ExportExtension    = ".studio-sandbox"
	maxManifestBytes   = 1 << 20
	exportHeaderPrefix = len(ExportMagic) + 4
)

// ErrInvalidExport means an export file is malformed or unsupported.
var ErrInvalidExport = errors.New("invalid sandbox export")

// ExportManifest describes the sandbox in an export file. It carries no secrets, egress
// identity, approvals, checkpoints or Docker state.
type ExportManifest struct {
	Format           int                 `json:"format"`
	ExportedAt       time.Time           `json:"exportedAt"`
	Name             string              `json:"name"`
	TemplateSpecYAML *string             `json:"templateSpecYAML"`
	Resources        resources.Resources `json:"resources"`
	// WorkspaceBytes is the archive length, or 0 when unknown. Studio streams exports, so
	// it writes 0 and the archive runs to the end of the file.
	WorkspaceBytes int64 `json:"workspaceBytes"`
}

func invalidExport(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExport, fmt.Sprintf(format, args...))
}

// WriteExportHeader writes the magic line, the manifest length and the manifest.
func WriteExportHeader(w io.Writer, manifest ExportManifest) error {
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(body) > maxManifestBytes {
		return invalidExport("manifest exceeds 1 MiB")
	}
	header := make([]byte, 0, exportHeaderPrefix+len(body))
	header = append(header, ExportMagic...)
	header = binary.BigEndian.AppendUint32(header, uint32(len(body)))
	header = append(header, body...)
	_, err = w.Write(header)
	return err
}

// ReadExportHeader reads and strictly validates the header of an untrusted export file.
// It reads at most the magic line, 4 bytes and 1 MiB, leaving r at the archive.
func ReadExportHeader(r io.Reader) (ExportManifest, error) {
	prefix := make([]byte, exportHeaderPrefix)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return ExportManifest{}, invalidExport("file is too short")
	}
	if string(prefix[:len(ExportMagic)]) != ExportMagic {
		return ExportManifest{}, invalidExport("not a Sandbox Studio export, or an unsupported version")
	}
	n := binary.BigEndian.Uint32(prefix[len(ExportMagic):])
	if n == 0 || n > maxManifestBytes {
		return ExportManifest{}, invalidExport("manifest length must be between 1 byte and 1 MiB")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return ExportManifest{}, invalidExport("manifest is truncated")
	}
	return ParseExportManifest(body)
}

// ParseExportManifest decodes a manifest, rejecting unknown fields and trailing data,
// and validates it as an import would.
func ParseExportManifest(body []byte) (ExportManifest, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var raw struct {
		Format           *int                 `json:"format"`
		ExportedAt       time.Time            `json:"exportedAt"`
		Name             string               `json:"name"`
		TemplateSpecYAML *string              `json:"templateSpecYAML"`
		Resources        *resources.Resources `json:"resources"`
		WorkspaceBytes   int64                `json:"workspaceBytes"`
	}
	if err := dec.Decode(&raw); err != nil {
		return ExportManifest{}, invalidExport("manifest is not valid: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ExportManifest{}, invalidExport("manifest has trailing data")
	}
	if raw.Format == nil || *raw.Format != ExportFormat {
		return ExportManifest{}, invalidExport("unsupported manifest format")
	}
	if raw.Resources == nil {
		return ExportManifest{}, invalidExport("manifest has no resources")
	}
	m := ExportManifest{Format: *raw.Format, ExportedAt: raw.ExportedAt, Name: raw.Name,
		TemplateSpecYAML: raw.TemplateSpecYAML, Resources: *raw.Resources, WorkspaceBytes: raw.WorkspaceBytes}
	if err := runtime.ValidName(m.Name); err != nil {
		return ExportManifest{}, invalidExport("sandbox name is invalid")
	}
	if err := m.Resources.Validate(); err != nil {
		return ExportManifest{}, invalidExport("%v", err)
	}
	if m.WorkspaceBytes < 0 || m.WorkspaceBytes > m.WorkspaceLimit() {
		return ExportManifest{}, invalidExport("workspaceBytes is out of range")
	}
	if m.TemplateSpecYAML != nil {
		spec, err := templatespec.ParseYAML([]byte(*m.TemplateSpecYAML))
		if err != nil {
			return ExportManifest{}, invalidExport("%v", err)
		}
		if spec.Resources != m.Resources {
			return ExportManifest{}, invalidExport("resources do not match the template spec")
		}
	}
	return m, nil
}

// WorkspaceLimit is the largest archive an import of m accepts: the workspace size, the
// same limit as a workspace copy between VMs.
func (m ExportManifest) WorkspaceLimit() int64 { return m.Resources.WorkspaceMiB << 20 }

// Export writes a sandbox as an export file. open is called once the sandbox is ready to
// stream, with the manifest; errors before that leave nothing written. A running sandbox
// is exported live, so files written meanwhile may be inconsistent. A stopped one is
// started without services for the export and stopped again.
func (m *Manager) Export(ctx context.Context, envID, id string, open func(ExportManifest) (io.Writer, error)) error {
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	unlock, err := m.tryMutation(id)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := m.PublicSandbox(ctx, envID, id); err != nil {
		return err
	}
	if err := m.ensureSettled(ctx, envID, id, ""); err != nil {
		return err
	}
	sb, err := m.PublicSandbox(ctx, envID, id)
	if err != nil {
		return err
	}
	rt, err := m.transfer()
	if err != nil {
		return err
	}
	status, err := m.Runtime.Status(ctx, VMName(sb))
	if err != nil {
		return err
	}
	if status != runtime.StatusRunning && status != runtime.StatusStopped {
		return ErrLifecycleState
	}
	manifest := ExportManifest{
		Format: ExportFormat, ExportedAt: time.Now().UTC().Truncate(time.Second), Name: sb.Name,
		Resources: resources.Resources{
			CPUs: int64(sb.CPUs), MemoryMiB: int64(sb.MemoryMiB), MaxMemoryMiB: int64(sb.MaxMemoryMiB),
			WorkspaceMiB: int64(sb.WorkspaceMiB), DockerMiB: int64(sb.DockerMiB),
		},
	}
	if sb.TemplateID != "" {
		source, err := m.templateSourceYAML(ctx, envID, sb.TemplateID)
		if err != nil {
			return err
		}
		manifest.TemplateSpecYAML = &source
	}
	if status == runtime.StatusStopped {
		if _, err := m.Egress.Attach(sb); err != nil {
			return err
		}
		if err := rt.StartForTransfer(ctx, VMName(sb)); err != nil {
			return errors.Join(err, m.stopQuietly(ctx, VMName(sb)))
		}
		defer func() {
			if stopErr := m.stopQuietly(ctx, VMName(sb)); stopErr != nil {
				m.Log.Warn("stopping exported sandbox", "sandbox", sb.ID, "err", stopErr)
			}
		}()
	}
	w, err := open(manifest)
	if err != nil {
		return err
	}
	if err := WriteExportHeader(w, manifest); err != nil {
		return err
	}
	return rt.ExportWorkspace(ctx, ownedVM(sb), &limitWriter{w: w, left: manifest.WorkspaceLimit()})
}

// templateSourceYAML returns YAML for a template's spec. Templates keep only the
// canonical spec, so it is rendered from that; it parses back to the same spec.
func (m *Manager) templateSourceYAML(ctx context.Context, envID, templateID string) (string, error) {
	template, err := m.Store.Template(ctx, envID, templateID)
	if err != nil {
		return "", err
	}
	if template.State != store.TemplateStateReady {
		return "", store.ErrConflict
	}
	spec, err := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	if err != nil {
		return "", errors.New("template specification is invalid")
	}
	out, err := templatespec.YAML(spec)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
