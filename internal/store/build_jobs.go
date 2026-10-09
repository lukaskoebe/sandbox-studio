package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
)

const (
	BuildQueued    = "queued"
	BuildPreparing = "preparing"
	BuildSettingUp = "setting_up"
	BuildExporting = "exporting"
	BuildReady     = "ready"
	BuildFailed    = "failed"
	BuildCancelled = "cancelled"

	maxBuildSourceBytes = 64 << 10
	maxBuildSpecBytes   = 512 << 10
	maxBuildLogBytes    = 1 << 20
	maxBuildErrorBytes  = 1000
	defaultBuildList    = 50
	maxBuildList        = 500
)

// BuildJob is a durable request and lifecycle record for one template build.
// Output is kept in build_job_logs so job rows stay bounded.
type BuildJob struct {
	ID              string    `json:"id"`
	EnvironmentID   string    `json:"environmentId"`
	RequestKey      string    `json:"requestKey"`
	Source          string    `json:"source"`
	Spec            string    `json:"spec"`
	BaseRef         string    `json:"baseRef"`
	TargetPlatform  string    `json:"targetPlatform"`
	ExporterVersion string    `json:"exporterVersion"`
	CacheKey        string    `json:"cacheKey,omitempty"`
	BaseDigest      string    `json:"baseDigest,omitempty"`
	Platform        string    `json:"platform,omitempty"`
	TemplateID      string    `json:"templateId,omitempty"`
	Status          string    `json:"status" enum:"queued,preparing,setting_up,exporting,ready,failed,cancelled"`
	Error           string    `json:"error,omitempty"`
	CleanupError    string    `json:"cleanupError,omitempty"`
	CleanupPending  bool      `json:"cleanupPending"`
	PrewarmName     string    `json:"-"`
	PrewarmToken    string    `json:"-"`
	SandboxID       string    `json:"sandboxId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

const buildJobCols = "id, environment_id, request_key, source, spec, base_ref, target_platform, exporter_version, cache_key, base_digest, platform, IFNULL(template_id, ''), status, error, cleanup_error, cleanup_pending, prewarm_name, prewarm_token, IFNULL(sandbox_id, ''), created_at, updated_at"

func scanBuildJob(row interface{ Scan(...any) error }) (BuildJob, error) {
	var job BuildJob
	var pending int
	var created, updated int64
	err := row.Scan(
		&job.ID, &job.EnvironmentID, &job.RequestKey, &job.Source, &job.Spec,
		&job.BaseRef, &job.TargetPlatform, &job.ExporterVersion, &job.CacheKey,
		&job.BaseDigest, &job.Platform, &job.TemplateID, &job.Status, &job.Error,
		&job.CleanupError, &pending, &job.PrewarmName, &job.PrewarmToken,
		&job.SandboxID, &created, &updated,
	)
	if err != nil {
		return job, err
	}
	job.CleanupPending = pending != 0
	job.CreatedAt = time.Unix(created, 0)
	job.UpdatedAt = time.Unix(updated, 0)
	return job, nil
}

type buildJobQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func buildJobFrom(ctx context.Context, q buildJobQueryer, envID, jobID string) (BuildJob, error) {
	job, err := scanBuildJob(q.QueryRowContext(ctx, "SELECT "+buildJobCols+" FROM build_jobs WHERE environment_id = ? AND id = ?", envID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return BuildJob{}, ErrNotFound
	}
	return job, err
}

func exactlyOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func validateBuildJobInput(in BuildJob) error {
	if strings.TrimSpace(in.EnvironmentID) == "" {
		return fmt.Errorf("build job environment ID is required")
	}
	if len(in.RequestKey) == 0 || len(in.RequestKey) > 255 {
		return fmt.Errorf("build job request key must be between 1 and 255 bytes")
	}
	if len(in.Source) == 0 || len(in.Source) > maxBuildSourceBytes {
		return fmt.Errorf("build job source must be between 1 and 64 KiB")
	}
	if len(in.Spec) == 0 || len(in.Spec) > maxBuildSpecBytes || !json.Valid([]byte(in.Spec)) {
		return fmt.Errorf("build job spec must be valid JSON no larger than 512 KiB")
	}
	if strings.TrimSpace(in.BaseRef) == "" {
		return fmt.Errorf("build job base reference is required")
	}
	if strings.TrimSpace(in.TargetPlatform) == "" {
		return fmt.Errorf("build job target platform is required")
	}
	if strings.TrimSpace(in.ExporterVersion) == "" {
		return fmt.Errorf("build job exporter version is required")
	}
	return nil
}

func sameBuildRequest(existing, in BuildJob) bool {
	// The request key is derived from the canonical spec, so equivalent YAML
	// formatting may carry a different raw Source while remaining idempotent.
	return existing.Spec == in.Spec &&
		existing.BaseRef == in.BaseRef && existing.TargetPlatform == in.TargetPlatform &&
		existing.ExporterVersion == in.ExporterVersion
}

// CreateBuildJob inserts a queued build. A repeated active environment/request
// key returns the original row if the canonical request payload is identical.
func (s *Store) CreateBuildJob(ctx context.Context, in BuildJob) (BuildJob, bool, error) {
	if err := validateBuildJobInput(in); err != nil {
		return BuildJob{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildJob{}, false, err
	}
	defer tx.Rollback()

	t := now()
	job := BuildJob{
		ID: NewID(), EnvironmentID: in.EnvironmentID, RequestKey: in.RequestKey,
		Source: in.Source, Spec: in.Spec, BaseRef: in.BaseRef,
		TargetPlatform: in.TargetPlatform, ExporterVersion: in.ExporterVersion,
		Status: BuildQueued, CreatedAt: time.Unix(t, 0), UpdatedAt: time.Unix(t, 0),
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO build_jobs (id, environment_id, request_key, source, spec, base_ref, target_platform, exporter_version, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
		job.ID, job.EnvironmentID, job.RequestKey, job.Source, job.Spec, job.BaseRef,
		job.TargetPlatform, job.ExporterVersion, job.Status, t, t)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY") {
			return BuildJob{}, false, ErrNotFound
		}
		return BuildJob{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return BuildJob{}, false, err
	}
	if n == 0 {
		// The active-request unique index is the cross-process deduplication
		// authority. Resolve the committed winner only after the insert conflict.
		previous, lookupErr := scanBuildJob(tx.QueryRowContext(ctx, "SELECT "+buildJobCols+" FROM build_jobs WHERE environment_id = ? AND request_key = ? AND status IN (?, ?, ?, ?) ORDER BY created_at DESC LIMIT 1",
			in.EnvironmentID, in.RequestKey, BuildQueued, BuildPreparing, BuildSettingUp, BuildExporting))
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return BuildJob{}, false, fmt.Errorf("build job request key %q: %w", in.RequestKey, ErrExists)
		}
		if lookupErr != nil {
			return BuildJob{}, false, lookupErr
		}
		if !sameBuildRequest(previous, in) {
			return BuildJob{}, false, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return BuildJob{}, false, err
		}
		return previous, false, nil
	}
	if n != 1 {
		return BuildJob{}, false, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return BuildJob{}, false, err
	}
	return job, true, nil
}

// BuildJob returns one job owned by envID.
func (s *Store) BuildJob(ctx context.Context, envID, id string) (BuildJob, error) {
	return buildJobFrom(ctx, s.db, envID, id)
}

// BuildJobs lists an environment's recent build jobs. Limits outside the
// supported range are clamped to keep this read bounded.
func (s *Store) BuildJobs(ctx context.Context, envID string, limit int) ([]BuildJob, error) {
	if limit <= 0 {
		limit = defaultBuildList
	}
	if limit > maxBuildList {
		limit = maxBuildList
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+buildJobCols+" FROM build_jobs WHERE environment_id = ? ORDER BY created_at DESC, id DESC LIMIT ?", envID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BuildJob{}
	for rows.Next() {
		job, err := scanBuildJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// RecoverableBuildJobs returns queued and active work, plus terminal jobs that
// still have a VM or prewarm owner requiring cleanup.
func (s *Store) RecoverableBuildJobs(ctx context.Context) ([]BuildJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+buildJobCols+" FROM build_jobs WHERE status IN (?, ?, ?, ?) OR cleanup_pending = 1 ORDER BY created_at, id",
		BuildQueued, BuildPreparing, BuildSettingUp, BuildExporting)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BuildJob{}
	for rows.Next() {
		job, err := scanBuildJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// ClaimBuildJob atomically claims the oldest queued job, enforcing one active
// worker across all Store instances that share the catalog.
func (s *Store) ClaimBuildJob(ctx context.Context) (BuildJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildJob{}, err
	}
	defer tx.Rollback()

	job, err := scanBuildJob(tx.QueryRowContext(ctx, "UPDATE build_jobs SET status = ?, updated_at = ? WHERE id = (SELECT id FROM build_jobs WHERE status = ? ORDER BY created_at, id LIMIT 1) AND status = ? AND NOT EXISTS (SELECT 1 FROM build_jobs WHERE status IN (?, ?, ?) OR cleanup_pending = 1) RETURNING "+buildJobCols,
		BuildPreparing, now(), BuildQueued, BuildQueued, BuildPreparing, BuildSettingUp, BuildExporting))
	if errors.Is(err, sql.ErrNoRows) {
		var active int
		checkErr := tx.QueryRowContext(ctx, "SELECT 1 FROM build_jobs WHERE status IN (?, ?, ?) OR cleanup_pending = 1 LIMIT 1", BuildPreparing, BuildSettingUp, BuildExporting).Scan(&active)
		if checkErr == nil {
			return BuildJob{}, ErrConflict
		}
		if !errors.Is(checkErr, sql.ErrNoRows) {
			return BuildJob{}, checkErr
		}
		return BuildJob{}, ErrNotFound
	}
	if err != nil {
		return BuildJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return BuildJob{}, err
	}
	return job, nil
}

// ResolveBuildJob binds the actual base and platform identity used for the
// cache key. The partial unique index prevents concurrent duplicate builds.
func (s *Store) ResolveBuildJob(ctx context.Context, envID, id, key, baseDigest, platform string) (BuildJob, error) {
	if !validOCISHA256(key) {
		return BuildJob{}, fmt.Errorf("build cache key must be a lowercase sha256 digest")
	}
	if !validOCISHA256(baseDigest) {
		return BuildJob{}, fmt.Errorf("build base digest must be a lowercase sha256 digest")
	}
	if strings.TrimSpace(platform) == "" {
		return BuildJob{}, fmt.Errorf("build platform is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildJob{}, err
	}
	defer tx.Rollback()

	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return BuildJob{}, err
	}
	if job.Status != BuildPreparing {
		return BuildJob{}, ErrConflict
	}
	if job.CacheKey != "" || job.BaseDigest != "" || job.Platform != "" {
		if job.CacheKey != key || job.BaseDigest != baseDigest || job.Platform != platform {
			return BuildJob{}, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return BuildJob{}, err
		}
		return job, nil
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET cache_key = ?, base_digest = ?, platform = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ? AND cache_key = '' AND base_digest = '' AND platform = ''",
		key, baseDigest, platform, now(), envID, id, BuildPreparing)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return BuildJob{}, fmt.Errorf("a build for cache key %q: %w", key, ErrExists)
		}
		return BuildJob{}, err
	}
	if err := exactlyOneRow(res); err != nil {
		return BuildJob{}, err
	}
	job, err = buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return BuildJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return BuildJob{}, err
	}
	return job, nil
}

// BeginBuildPrewarm records the exact prewarm identity before a VM can exist.
func (s *Store) BeginBuildPrewarm(ctx context.Context, envID, id, name, token string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(token) == "" {
		return fmt.Errorf("prewarm name and token are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return err
	}
	if job.Status != BuildPreparing || job.CleanupPending || job.PrewarmName != "" || job.PrewarmToken != "" || job.SandboxID != "" {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET prewarm_name = ?, prewarm_token = ?, cleanup_pending = 1, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ? AND cleanup_pending = 0 AND prewarm_name = '' AND prewarm_token = '' AND sandbox_id IS NULL",
		name, token, now(), envID, id, BuildPreparing)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishBuildPrewarm clears prewarm ownership only for the exact recorded
// identity. It remains valid after cancellation so cleanup can finish.
func (s *Store) FinishBuildPrewarm(ctx context.Context, envID, id, name, token string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return err
	}
	if !job.CleanupPending || job.PrewarmName != name || job.PrewarmToken != token || name == "" || token == "" || job.SandboxID != "" {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET prewarm_name = '', prewarm_token = '', cleanup_pending = 0, cleanup_error = '', updated_at = ? WHERE environment_id = ? AND id = ? AND cleanup_pending = 1 AND prewarm_name = ? AND prewarm_token = ? AND sandbox_id IS NULL",
		now(), envID, id, name, token)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// ReserveBuildSandbox atomically reserves a builder row and DNS port before
// the manager is allowed to create its VM.
func (s *Store) ReserveBuildSandbox(ctx context.Context, envID, id string, r resources.Resources) (Sandbox, error) {
	if err := r.Validate(); err != nil {
		return Sandbox{}, err
	}
	maxInt := int64(^uint(0) >> 1)
	if r.MemoryMiB > maxInt || r.MaxMemoryMiB > maxInt || r.WorkspaceMiB > maxInt || r.DockerMiB > maxInt || r.CPUs > maxInt {
		return Sandbox{}, fmt.Errorf("build sandbox resources exceed the platform integer range")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sandbox{}, err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return Sandbox{}, err
	}
	if job.Status != BuildPreparing || job.CacheKey == "" || job.BaseDigest == "" || job.Platform == "" || job.CleanupPending || job.SandboxID != "" || job.PrewarmName != "" || job.PrewarmToken != "" {
		return Sandbox{}, ErrConflict
	}
	port, err := freeDNSPort(ctx, tx)
	if err != nil {
		return Sandbox{}, err
	}
	t := now()
	sb := Sandbox{
		ID: NewID(), EnvironmentID: envID, BuildJobID: id, Name: "build-" + id,
		Generation: 1, CPUs: int(r.CPUs), MemoryMiB: int(r.MemoryMiB),
		MaxMemoryMiB: int(r.MaxMemoryMiB), WorkspaceMiB: int(r.WorkspaceMiB),
		DockerMiB: int(r.DockerMiB), CreatedAt: time.Unix(t, 0), DNSPort: port,
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO sandboxes (id, environment_id, build_job_id, name, generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib, created_at, dns_port) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sb.ID, sb.EnvironmentID, sb.BuildJobID, sb.Name, sb.Generation, sb.CPUs,
		sb.MemoryMiB, sb.MaxMemoryMiB, sb.WorkspaceMiB, sb.DockerMiB, t, sb.DNSPort)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
			return Sandbox{}, fmt.Errorf("a sandbox named %q: %w", sb.Name, ErrExists)
		}
		return Sandbox{}, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET sandbox_id = ?, cleanup_pending = 1, status = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ? AND cache_key <> '' AND base_digest <> '' AND platform <> '' AND cleanup_pending = 0 AND sandbox_id IS NULL AND prewarm_name = '' AND prewarm_token = ''",
		sb.ID, BuildSettingUp, t, envID, id, BuildPreparing)
	if err != nil {
		return Sandbox{}, err
	}
	if err := exactlyOneRow(res); err != nil {
		return Sandbox{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sandbox{}, err
	}
	return sb, nil
}

// FinishBuildSandbox releases a builder row only when both sides still name
// the same job. Callers must have verified that the runtime VM is absent.
func (s *Store) FinishBuildSandbox(ctx context.Context, envID, id, sandboxID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return err
	}
	if job.SandboxID != sandboxID || !job.CleanupPending || sandboxID == "" || job.PrewarmName != "" || job.PrewarmToken != "" {
		return ErrConflict
	}
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT IFNULL(build_job_id, '') FROM sandboxes WHERE environment_id = ? AND id = ?", envID, sandboxID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if owner != id {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET sandbox_id = NULL, cleanup_pending = 0, cleanup_error = '', updated_at = ? WHERE environment_id = ? AND id = ? AND sandbox_id = ? AND cleanup_pending = 1",
		now(), envID, id, sandboxID)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, "UPDATE sandboxes SET build_job_id = NULL WHERE environment_id = ? AND id = ? AND build_job_id = ?", envID, sandboxID, id)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, "DELETE FROM sandboxes WHERE environment_id = ? AND id = ? AND build_job_id IS NULL", envID, sandboxID)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// AdvanceBuildJob permits the single setup-to-export transition.
func (s *Store) AdvanceBuildJob(ctx context.Context, envID, id, from, to string) error {
	if from != BuildSettingUp || to != BuildExporting {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET status = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ?", to, now(), envID, id, from)
	if err != nil {
		return err
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return rowsErr
	}
	if n == 0 {
		var exists int
		checkErr := tx.QueryRowContext(ctx, "SELECT 1 FROM build_jobs WHERE environment_id = ? AND id = ?", envID, id).Scan(&exists)
		if errors.Is(checkErr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if checkErr != nil {
			return checkErr
		}
		return ErrConflict
	} else if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// CompleteBuildJob marks a job ready only after its already-ready template is
// found in the same environment with the resolved cache key.
func (s *Store) CompleteBuildJob(ctx context.Context, envID, id, from, templateID string) error {
	if from != BuildPreparing && from != BuildExporting {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return err
	}
	if job.Status != from || job.CacheKey == "" || job.BaseDigest == "" || job.Platform == "" {
		return ErrConflict
	}
	if from == BuildPreparing && (job.CleanupPending || job.SandboxID != "" || job.PrewarmName != "" || job.PrewarmToken != "") {
		return ErrConflict
	}
	var cacheKey, state string
	err = tx.QueryRowContext(ctx, "SELECT cache_key, state FROM templates WHERE environment_id = ? AND id = ?", envID, templateID).Scan(&cacheKey, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if cacheKey != job.CacheKey || state != TemplateStateReady {
		return ErrConflict
	}
	query := "UPDATE build_jobs SET status = ?, template_id = ?, error = '', updated_at = ? WHERE environment_id = ? AND id = ? AND status = ? AND cache_key = ?"
	args := []any{BuildReady, templateID, now(), envID, id, from, job.CacheKey}
	if from == BuildPreparing {
		query += " AND cleanup_pending = 0 AND sandbox_id IS NULL AND prewarm_name = '' AND prewarm_token = ''"
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelBuildJob atomically cancels nonterminal work. Existing ownership stays
// attached so the worker can clean it up after cancellation.
func (s *Store) CancelBuildJob(ctx context.Context, envID, id string) (BuildJob, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BuildJob{}, false, err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return BuildJob{}, false, err
	}
	if terminalBuildStatus(job.Status) {
		if err := tx.Commit(); err != nil {
			return BuildJob{}, false, err
		}
		return job, false, nil
	}
	t := now()
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET status = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ?", BuildCancelled, t, envID, id, job.Status)
	if err != nil {
		return BuildJob{}, false, err
	}
	if err := exactlyOneRow(res); err != nil {
		return BuildJob{}, false, err
	}
	job, err = buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return BuildJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BuildJob{}, false, err
	}
	return job, true, nil
}

func terminalBuildStatus(status string) bool {
	return status == BuildReady || status == BuildFailed || status == BuildCancelled
}

func boundBuildText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	return truncateValidUTF8(value, limit)
}

func truncateValidUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	// value was valid UTF-8 before slicing, so only a partial final rune can
	// make the prefix invalid. At most three bytes need to be removed.
	for removed := 0; removed < utf8.UTFMax-1 && !utf8.ValidString(value); removed++ {
		value = value[:len(value)-1]
	}
	return value
}

// FailBuildJob marks nonterminal work failed without replacing ready or
// cancelled outcomes. The short error field is byte-bounded.
func (s *Store) FailBuildJob(ctx context.Context, envID, id, message string) error {
	message = boundBuildText(message, maxBuildErrorBytes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := buildJobFrom(ctx, tx, envID, id)
	if err != nil {
		return err
	}
	if terminalBuildStatus(job.Status) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET status = ?, error = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND status = ?",
		BuildFailed, message, now(), envID, id, job.Status)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}

// SetBuildCleanupError stores a bounded cleanup error while preserving status
// and ownership for later retry.
func (s *Store) SetBuildCleanupError(ctx context.Context, envID, id, message string) error {
	message = boundBuildText(message, maxBuildErrorBytes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE build_jobs SET cleanup_error = ?, updated_at = ? WHERE environment_id = ? AND id = ?", message, now(), envID, id)
	if err != nil {
		return err
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return rowsErr
	}
	if n == 0 {
		return ErrNotFound
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

// PutBuildLog replaces the accumulated log snapshot and remembers whether any
// part of the job's output has been truncated. Stored output is limited to one
// MiB of UTF-8 bytes.
func (s *Store) PutBuildLog(ctx context.Context, envID, id, text string, truncated bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM build_jobs WHERE environment_id = ? AND id = ?", envID, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Shell output is arbitrary bytes. Normalize it even when it fits, then
	// enforce the database's byte limit on the normalized UTF-8 representation.
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) > maxBuildLogBytes {
		text = truncateValidUTF8(text, maxBuildLogBytes)
		truncated = true
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO build_job_logs (environment_id, job_id, content, truncated) VALUES (?, ?, ?, ?) ON CONFLICT(environment_id, job_id) DO UPDATE SET content = excluded.content, truncated = CASE WHEN build_job_logs.truncated != 0 OR excluded.truncated != 0 THEN 1 ELSE 0 END",
		envID, id, text, boolInt(truncated))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// BuildLog returns the bounded accumulated output and whether any output was
// truncated. A job with no output returns an empty string and false.
func (s *Store) BuildLog(ctx context.Context, envID, id string) (string, bool, error) {
	var found string
	var content sql.NullString
	var truncated sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT j.id, l.content, l.truncated FROM build_jobs j LEFT JOIN build_job_logs l ON l.environment_id = j.environment_id AND l.job_id = j.id WHERE j.environment_id = ? AND j.id = ?", envID, id).Scan(&found, &content, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	return content.String, truncated.Valid && truncated.Int64 != 0, nil
}
