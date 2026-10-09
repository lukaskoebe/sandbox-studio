package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"
)

// Template lifecycle states.
const (
	TemplateStateCreating = "creating"
	TemplateStateReady    = "ready"
	TemplateStateDeleting = "deleting"
)

// Template is a durable registry cache entry. Artifacts are catalog metadata
// and are loaded by the store, but are not part of the public JSON shape.
type Template struct {
	ID              string             `json:"id"`
	EnvironmentID   string             `json:"environmentId"`
	CacheKey        string             `json:"cacheKey"`
	Spec            string             `json:"spec"`
	BaseRef         string             `json:"baseRef"`
	BaseDigest      string             `json:"baseDigest"`
	Platform        string             `json:"platform"`
	ExporterVersion string             `json:"exporterVersion"`
	State           string             `json:"state" enum:"creating,ready,deleting"`
	CreatedAt       time.Time          `json:"createdAt"`
	Artifacts       []TemplateArtifact `json:"-"`
}

// TemplateArtifact describes one immutable OCI object in a ready template.
type TemplateArtifact struct {
	Role      string `json:"role"`
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

const templateCols = "id, environment_id, cache_key, spec, base_ref, base_digest, platform, exporter_version, state, created_at"

func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var t Template
	var created int64
	err := row.Scan(&t.ID, &t.EnvironmentID, &t.CacheKey, &t.Spec, &t.BaseRef, &t.BaseDigest, &t.Platform, &t.ExporterVersion, &t.State, &created)
	if err != nil {
		return t, err
	}
	t.CreatedAt = time.Unix(created, 0)
	t.Artifacts = []TemplateArtifact{}
	return t, nil
}

type templateQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readTemplateArtifacts(ctx context.Context, q templateQueryer, templateID string) ([]TemplateArtifact, error) {
	rows, err := q.QueryContext(ctx, "SELECT role, digest, media_type, size FROM template_artifacts WHERE template_id = ? ORDER BY CASE role WHEN 'manifest' THEN 0 WHEN 'config' THEN 1 ELSE 2 END", templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TemplateArtifact{}
	for rows.Next() {
		var a TemplateArtifact
		if err := rows.Scan(&a.Role, &a.Digest, &a.MediaType, &a.Size); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validOCISHA256(digest string) bool {
	const prefix = "sha256:"
	if len(digest) != len(prefix)+64 || !strings.HasPrefix(digest, prefix) {
		return false
	}
	_, err := hex.DecodeString(digest[len(prefix):])
	return err == nil && digest == strings.ToLower(digest)
}

func validateTemplate(t Template) error {
	if strings.TrimSpace(t.EnvironmentID) == "" {
		return fmt.Errorf("template environment ID is required")
	}
	if !validOCISHA256(t.CacheKey) {
		return fmt.Errorf("template cache key must be a lowercase sha256 digest")
	}
	if strings.TrimSpace(t.Spec) == "" {
		return fmt.Errorf("template spec is required")
	}
	if strings.TrimSpace(t.BaseRef) == "" {
		return fmt.Errorf("template base reference is required")
	}
	if !validOCISHA256(t.BaseDigest) {
		return fmt.Errorf("template base digest must be a lowercase sha256 digest")
	}
	if strings.TrimSpace(t.Platform) == "" {
		return fmt.Errorf("template platform is required")
	}
	if strings.TrimSpace(t.ExporterVersion) == "" {
		return fmt.Errorf("template exporter version is required")
	}
	return nil
}

// CreateTemplate inserts a creating template, assigning its ID and timestamp.
// The input ID, state, timestamp, and artifact fields are ignored.
func (s *Store) CreateTemplate(ctx context.Context, in Template) (Template, error) {
	if err := validateTemplate(in); err != nil {
		return Template{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM environments WHERE id = ?", in.EnvironmentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}

	out := Template{
		ID: NewID(), EnvironmentID: in.EnvironmentID, CacheKey: in.CacheKey,
		Spec: in.Spec, BaseRef: in.BaseRef, BaseDigest: in.BaseDigest,
		Platform: in.Platform, ExporterVersion: in.ExporterVersion,
		State: TemplateStateCreating, CreatedAt: time.Unix(now(), 0),
		Artifacts: []TemplateArtifact{},
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO templates ("+templateCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		out.ID, out.EnvironmentID, out.CacheKey, out.Spec, out.BaseRef, out.BaseDigest,
		out.Platform, out.ExporterVersion, out.State, out.CreatedAt.Unix())
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return Template{}, fmt.Errorf("a template with cache key %q: %w", out.CacheKey, ErrExists)
		}
		return Template{}, err
	}
	if err := tx.Commit(); err != nil {
		return Template{}, err
	}
	return out, nil
}

func loadTemplateWithArtifacts(ctx context.Context, tx *sql.Tx, row interface{ Scan(...any) error }) (Template, error) {
	t, err := scanTemplate(row)
	if err != nil {
		return t, err
	}
	t.Artifacts, err = readTemplateArtifacts(ctx, tx, t.ID)
	return t, err
}

// Template returns a template owned by envID, including its artifacts.
func (s *Store) Template(ctx context.Context, envID, id string) (Template, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()

	t, err := loadTemplateWithArtifacts(ctx, tx, tx.QueryRowContext(ctx, "SELECT "+templateCols+" FROM templates WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}
	if err := tx.Commit(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// TemplateByKey returns the template for an environment's cache key.
func (s *Store) TemplateByKey(ctx context.Context, envID, key string) (Template, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback()

	t, err := loadTemplateWithArtifacts(ctx, tx, tx.QueryRowContext(ctx, "SELECT "+templateCols+" FROM templates WHERE environment_id = ? AND cache_key = ?", envID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}
	if err := tx.Commit(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// Templates lists one environment's templates. An empty environment ID lists all
// templates so startup recovery can find work left in creating/deleting states.
func (s *Store) Templates(ctx context.Context, envID string) ([]Template, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	query := "SELECT " + templateCols + " FROM templates ORDER BY environment_id, created_at, id"
	var rows *sql.Rows
	if envID == "" {
		rows, err = tx.QueryContext(ctx, query)
	} else {
		rows, err = tx.QueryContext(ctx, "SELECT "+templateCols+" FROM templates WHERE environment_id = ? ORDER BY created_at, id", envID)
	}
	if err != nil {
		return nil, err
	}
	out := []Template{}
	for rows.Next() {
		t, scanErr := scanTemplate(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		out = append(out, t)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return nil, rowsErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range out {
		out[i].Artifacts, err = readTemplateArtifacts(ctx, tx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateTemplateArtifacts(artifacts []TemplateArtifact) error {
	if len(artifacts) != 3 {
		return fmt.Errorf("a ready template must have exactly manifest, config, and layer artifacts")
	}
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		switch artifact.Role {
		case "manifest", "config", "layer":
		default:
			return fmt.Errorf("invalid template artifact role %q", artifact.Role)
		}
		if seen[artifact.Role] {
			return fmt.Errorf("duplicate template artifact role %q", artifact.Role)
		}
		seen[artifact.Role] = true
		if !validOCISHA256(artifact.Digest) {
			return fmt.Errorf("template artifact %q digest must be a lowercase sha256 digest", artifact.Role)
		}
		if artifact.Size <= 0 {
			return fmt.Errorf("template artifact %q size must be positive", artifact.Role)
		}
		mediaType, _, err := mime.ParseMediaType(artifact.MediaType)
		if err != nil || mediaType == "" {
			return fmt.Errorf("template artifact %q has an invalid media type", artifact.Role)
		}
	}
	if !seen["manifest"] || !seen["config"] || !seen["layer"] {
		return fmt.Errorf("a ready template must have exactly manifest, config, and layer artifacts")
	}
	return nil
}

// ReadyTemplate atomically records the immutable artifact set and marks a
// creating template ready.
func (s *Store) ReadyTemplate(ctx context.Context, envID, id string, artifacts []TemplateArtifact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM templates WHERE environment_id = ? AND id = ?", envID, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != TemplateStateCreating {
		return ErrConflict
	}
	if err := validateTemplateArtifacts(artifacts); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, "INSERT INTO template_artifacts (template_id, role, digest, media_type, size) VALUES (?, ?, ?, ?, ?)", id, artifact.Role, artifact.Digest, artifact.MediaType, artifact.Size); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "UPDATE templates SET state = ? WHERE environment_id = ? AND id = ? AND state = ?", TemplateStateReady, envID, id, TemplateStateCreating)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// SetTemplateDeleting marks a template for cleanup. Repeating the transition is
// safe so recovery and normal cleanup can share the same path.
func (s *Store) SetTemplateDeleting(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM templates WHERE environment_id = ? AND id = ?", envID, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == TemplateStateDeleting {
		return tx.Commit()
	}
	if state != TemplateStateCreating && state != TemplateStateReady {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE templates SET state = ? WHERE environment_id = ? AND id = ? AND state = ?", TemplateStateDeleting, envID, id, state)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// DeleteTemplate removes a template after its registry artifacts have been
// cleaned up. Only a deleting record can be removed.
func (s *Store) DeleteTemplate(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM templates WHERE environment_id = ? AND id = ?", envID, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != TemplateStateDeleting {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM templates WHERE environment_id = ? AND id = ? AND state = ?", envID, id, TemplateStateDeleting)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
