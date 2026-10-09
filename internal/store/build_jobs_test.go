package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
)

func newBuildJobForTest(t *testing.T, ctx context.Context, s *Store, envID, requestKey string) BuildJob {
	t.Helper()
	job, created, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: envID, RequestKey: requestKey, Source: "setup: echo hello\n",
		Spec:    `{"formatVersion":1,"setup":"echo hello"}`,
		BaseRef: "example.test/base:stable", TargetPlatform: "linux/amd64", ExporterVersion: "exporter-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("new request %q was not created", requestKey)
	}
	return job
}

func resolveBuildJobForTest(t *testing.T, ctx context.Context, s *Store, envID, id, cacheChar, baseChar string) BuildJob {
	t.Helper()
	job, err := s.ResolveBuildJob(ctx, envID, id, "sha256:"+repeatDigestChar(cacheChar), "sha256:"+repeatDigestChar(baseChar), "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestBuildJobRequestDedupAndEnvironmentIsolation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.CreateEnvironment(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}

	first := newBuildJobForTest(t, ctx, s, work.ID, "request-1")
	duplicate, created, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: work.ID, RequestKey: "request-1", Source: "# equivalent formatting\n" + first.Source, Spec: first.Spec,
		BaseRef: first.BaseRef, TargetPlatform: first.TargetPlatform, ExporterVersion: first.ExporterVersion,
	})
	if err != nil || created || duplicate.ID != first.ID {
		t.Fatalf("deduplicated request = %+v, created=%v, err=%v", duplicate, created, err)
	}
	changed := BuildJob{
		EnvironmentID: work.ID, RequestKey: "request-1", Source: "setup: echo changed\n", Spec: `{"formatVersion":1,"setup":"echo changed"}`,
		BaseRef: first.BaseRef, TargetPlatform: first.TargetPlatform, ExporterVersion: first.ExporterVersion,
	}
	if _, _, err := s.CreateBuildJob(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same request key with changed payload: %v", err)
	}
	other, otherCreated, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: private.ID, RequestKey: "request-1", Source: first.Source, Spec: first.Spec,
		BaseRef: first.BaseRef, TargetPlatform: first.TargetPlatform, ExporterVersion: first.ExporterVersion,
	})
	if err != nil || !otherCreated || other.ID == first.ID {
		t.Fatalf("same request key in another environment = %+v, created=%v, %v", other, otherCreated, err)
	}
	if _, err := s.BuildJob(ctx, private.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment job read: %v", err)
	}
	if first.Status != BuildQueued || first.CleanupPending || first.SandboxID != "" {
		t.Fatalf("new job state = %+v", first)
	}
	if _, _, err := s.CancelBuildJob(ctx, work.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	retry, created, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: work.ID, RequestKey: "request-1", Source: first.Source, Spec: first.Spec,
		BaseRef: first.BaseRef, TargetPlatform: first.TargetPlatform, ExporterVersion: first.ExporterVersion,
	})
	if err != nil || !created || retry.ID == first.ID {
		t.Fatalf("terminal request retry = %+v, created=%v, err=%v", retry, created, err)
	}
	if err := s.FailBuildJob(ctx, work.ID, retry.ID, "failed build"); err != nil {
		t.Fatal(err)
	}
	failedRetry, created, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: work.ID, RequestKey: "request-1", Source: first.Source, Spec: first.Spec,
		BaseRef: first.BaseRef, TargetPlatform: first.TargetPlatform, ExporterVersion: first.ExporterVersion,
	})
	if err != nil || !created || failedRetry.ID == retry.ID {
		t.Fatalf("failed request retry = %+v, created=%v, err=%v", failedRetry, created, err)
	}
}

func TestBuildJobSourceAndCanonicalSpecBounds(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	in := BuildJob{
		EnvironmentID: env.ID, RequestKey: "boundary", Source: strings.Repeat("s", maxBuildSourceBytes),
		Spec:    `{"x":"` + strings.Repeat("a", maxBuildSpecBytes-8) + `"}`,
		BaseRef: "example.test/base:stable", TargetPlatform: "linux/amd64", ExporterVersion: "1",
	}
	if len(in.Spec) != maxBuildSpecBytes {
		t.Fatalf("test spec is %d bytes, want %d", len(in.Spec), maxBuildSpecBytes)
	}
	if _, created, err := s.CreateBuildJob(ctx, in); err != nil || !created {
		t.Fatalf("accepted boundary-sized request: created=%v, err=%v", created, err)
	}
	in.RequestKey = "source-too-large"
	in.Source += "s"
	if _, _, err := s.CreateBuildJob(ctx, in); err == nil {
		t.Fatal("accepted source larger than 64 KiB")
	}
	in.RequestKey = "spec-too-large"
	in.Source = "source"
	in.Spec = `{"x":"` + strings.Repeat("a", maxBuildSpecBytes-7) + `"}`
	if _, _, err := s.CreateBuildJob(ctx, in); err == nil {
		t.Fatal("accepted canonical JSON larger than 512 KiB")
	}
}

func TestClaimBuildJobExclusionAndCancelledCleanupRecovery(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	first := newBuildJobForTest(t, ctx, s, env.ID, "request-1")
	second := newBuildJobForTest(t, ctx, s, env.ID, "request-2")
	claimed, err := s.ClaimBuildJob(ctx)
	if err != nil || (claimed.ID != first.ID && claimed.ID != second.ID) || claimed.Status != BuildPreparing {
		t.Fatalf("claimed job = %+v, %v", claimed, err)
	}
	if _, err := s.ClaimBuildJob(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("claimed another job while one was active: %v", err)
	}
	if _, _, err := s.CancelBuildJob(ctx, env.ID, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginBuildPrewarm(ctx, env.ID, claimed.ID, "prewarm-1", "token-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("began prewarm after cancellation: %v", err)
	}
	secondClaim, err := s.ClaimBuildJob(ctx)
	if err != nil || secondClaim.ID == claimed.ID || secondClaim.Status != BuildPreparing {
		t.Fatalf("second queued job after cancellation = %+v, %v", secondClaim, err)
	}
	if _, _, err := s.CancelBuildJob(ctx, env.ID, secondClaim.ID); err != nil {
		t.Fatal(err)
	}

	// The cleanup marker remains a global worker exclusion even for a terminal
	// job, until the exact prewarm identity is finished.
	prewarm := newBuildJobForTest(t, ctx, s, env.ID, "request-3")
	if claimed, err := s.ClaimBuildJob(ctx); err != nil || claimed.ID != prewarm.ID {
		t.Fatalf("claim prewarm case = %+v, %v", claimed, err)
	}
	if err := s.BeginBuildPrewarm(ctx, env.ID, prewarm.ID, "prewarm-3", "token-3"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CancelBuildJob(ctx, env.ID, prewarm.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimBuildJob(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("claimed while cancelled job still owns active work: %v", err)
	}
	recoverable, err := s.RecoverableBuildJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundCancelledOwner := false
	for _, job := range recoverable {
		if job.ID == prewarm.ID && job.Status == BuildCancelled && job.CleanupPending {
			foundCancelledOwner = true
		}
	}
	if !foundCancelledOwner {
		t.Fatalf("cancelled job with pending prewarm was omitted from recovery: %+v", recoverable)
	}
	if err := s.FinishBuildPrewarm(ctx, env.ID, prewarm.ID, "prewarm-3", "wrong-token"); !errors.Is(err, ErrConflict) {
		t.Fatalf("finished prewarm with wrong token: %v", err)
	}
	if err := s.SetBuildCleanupError(ctx, env.ID, prewarm.ID, "stale cleanup warning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishBuildPrewarm(ctx, env.ID, prewarm.ID, "prewarm-3", "token-3"); err != nil {
		t.Fatal(err)
	}
	finished, err := s.BuildJob(ctx, env.ID, prewarm.ID)
	if err != nil || finished.Status != BuildCancelled || finished.CleanupPending || finished.PrewarmName != "" || finished.PrewarmToken != "" || finished.CleanupError != "" {
		t.Fatalf("finished cancelled prewarm = %+v, %v", finished, err)
	}
}

func TestResolveCacheCollisionAndBuildSandboxOwnership(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	otherEnv, err := s.CreateEnvironment(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	job := newBuildJobForTest(t, ctx, s, env.ID, "request-1")
	claimed, err := s.ClaimBuildJob(ctx)
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("claim build owner = %+v, %v", claimed, err)
	}
	queued := newBuildJobForTest(t, ctx, s, env.ID, "request-2")
	key := "sha256:" + repeatDigestChar("e")
	base := "sha256:" + repeatDigestChar("a")
	job, err = s.ResolveBuildJob(ctx, env.ID, job.ID, key, base, "linux/amd64")
	if err != nil || job.CacheKey != key || job.BaseDigest != base {
		t.Fatalf("resolved identity = %+v, %v", job, err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE build_jobs SET status = ? WHERE id = ?", BuildPreparing, queued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveBuildJob(ctx, env.ID, queued.ID, key, base, "linux/amd64"); !errors.Is(err, ErrExists) {
		t.Fatalf("active cache collision was not rejected: %v", err)
	}

	// Restore the single-worker state before reserving the owned sandbox.
	if _, err := s.db.ExecContext(ctx, "UPDATE build_jobs SET status = ? WHERE id = ?", BuildQueued, queued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveBuildSandbox(ctx, env.ID, job.ID, resources.Resources{
		CPUs: 0, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024,
	}); !errors.Is(err, resources.ErrInvalid) {
		t.Fatalf("invalid build resources accepted: %v", err)
	}
	if all, err := s.AllSandboxes(ctx); err != nil || len(all) != 0 {
		t.Fatalf("invalid resources inserted a sandbox: %+v, %v", all, err)
	}
	sb, err := s.ReserveBuildSandbox(ctx, env.ID, job.ID, resources.Resources{
		CPUs: 2, MemoryMiB: 2048, MaxMemoryMiB: 4096, WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sb.BuildJobID != job.ID || sb.Name != "build-"+job.ID {
		t.Fatalf("reserved sandbox owner = %+v", sb)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", env.ID); err == nil {
		t.Fatal("deleted environment while a builder sandbox was owned")
	}
	retainedOwner, err := s.BuildJob(ctx, env.ID, job.ID)
	if err != nil || !retainedOwner.CleanupPending || retainedOwner.SandboxID != sb.ID {
		t.Fatalf("environment delete lost builder cleanup ownership: %+v, %v", retainedOwner, err)
	}
	if _, err := s.Sandbox(ctx, otherEnv.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment sandbox read: %v", err)
	}
	if err := s.DeleteSandbox(ctx, env.ID, sb.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary delete removed owned sandbox: %v", err)
	}
	if err := s.FinishBuildSandbox(ctx, otherEnv.ID, job.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment finish: %v", err)
	}
	if err := s.FinishBuildSandbox(ctx, env.ID, job.ID, "wrong-sandbox"); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish with wrong sandbox: %v", err)
	}
	if err := s.AdvanceBuildJob(ctx, env.ID, job.ID, BuildSettingUp, BuildExporting); err != nil {
		t.Fatal(err)
	}
	template, err := s.CreateTemplate(ctx, Template{
		EnvironmentID: env.ID, CacheKey: job.CacheKey, Spec: job.Spec,
		BaseRef: job.BaseRef, BaseDigest: job.BaseDigest,
		Platform: job.Platform, ExporterVersion: job.ExporterVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, template.ID, templateArtifacts()); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteBuildJob(ctx, env.ID, job.ID, BuildExporting, template.ID); err != nil {
		t.Fatalf("completed export while preserving owned builder cleanup: %v", err)
	}
	if err := s.CompleteBuildJob(ctx, otherEnv.ID, job.ID, BuildExporting, template.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment completion: %v", err)
	}
	readyWithCleanup, err := s.BuildJob(ctx, env.ID, job.ID)
	if err != nil || readyWithCleanup.Status != BuildReady || !readyWithCleanup.CleanupPending || readyWithCleanup.SandboxID != sb.ID || readyWithCleanup.TemplateID != template.ID {
		t.Fatalf("ready job lost its independent cleanup owner: %+v, %v", readyWithCleanup, err)
	}
	recoverable, err := s.RecoverableBuildJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundReadyCleanup := false
	for _, recovered := range recoverable {
		if recovered.ID == job.ID && recovered.Status == BuildReady && recovered.CleanupPending && recovered.SandboxID == sb.ID {
			foundReadyCleanup = true
		}
	}
	if !foundReadyCleanup {
		t.Fatalf("ready job with pending cleanup was omitted from recovery: %+v", recoverable)
	}
	retry, created, err := s.CreateBuildJob(ctx, BuildJob{
		EnvironmentID: env.ID, RequestKey: job.RequestKey, Source: job.Source, Spec: job.Spec,
		BaseRef: job.BaseRef, TargetPlatform: job.TargetPlatform, ExporterVersion: job.ExporterVersion,
	})
	if err != nil || !created || retry.ID == job.ID {
		t.Fatalf("ready request retry = %+v, created=%v, err=%v", retry, created, err)
	}
	if _, err := s.ClaimBuildJob(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("claimed queued work while ready job still owned its sandbox: %v", err)
	}
	if err := s.SetBuildCleanupError(ctx, env.ID, job.ID, "stale cleanup warning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishBuildSandbox(ctx, env.ID, job.ID, sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sandbox(ctx, env.ID, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finished owned sandbox remains: %v", err)
	}
	finished, err := s.BuildJob(ctx, env.ID, job.ID)
	if err != nil || finished.Status != BuildReady || finished.SandboxID != "" || finished.CleanupPending || finished.CleanupError != "" {
		t.Fatalf("finished sandbox job state = %+v, %v", finished, err)
	}
	claimed, err = s.ClaimBuildJob(ctx)
	if err != nil || claimed.Status != BuildPreparing || (claimed.ID != queued.ID && claimed.ID != retry.ID) {
		t.Fatalf("claim after owned cleanup finished = %+v, %v", claimed, err)
	}
	if _, _, err := s.CancelBuildJob(ctx, env.ID, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceBuildJob(ctx, env.ID, job.ID, BuildSettingUp, BuildExporting); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated setup transition: %v", err)
	}
	if _, err := s.ReserveBuildSandbox(ctx, env.ID, queued.ID, resources.Defaults()); !errors.Is(err, ErrConflict) {
		t.Fatalf("reserved sandbox for unresolved/nonpreparing job: %v", err)
	}
}

func TestBuildCompletionAndCancellationAreCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}

	readyCase := newBuildJobForTest(t, ctx, s, env.ID, "ready-case")
	if _, err := s.ClaimBuildJob(ctx); err != nil {
		t.Fatal(err)
	}
	readyCase = resolveBuildJobForTest(t, ctx, s, env.ID, readyCase.ID, "1", "a")
	template, err := s.CreateTemplate(ctx, Template{
		EnvironmentID: env.ID, CacheKey: readyCase.CacheKey, Spec: readyCase.Spec,
		BaseRef: readyCase.BaseRef, BaseDigest: readyCase.BaseDigest,
		Platform: readyCase.Platform, ExporterVersion: readyCase.ExporterVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, template.ID, templateArtifacts()); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginBuildPrewarm(ctx, env.ID, readyCase.ID, "cache-prewarm", "cache-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteBuildJob(ctx, env.ID, readyCase.ID, BuildPreparing, template.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cache hit completed while prewarm ownership was pending: %v", err)
	}
	if err := s.SetBuildCleanupError(ctx, env.ID, readyCase.ID, "stale cleanup warning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishBuildPrewarm(ctx, env.ID, readyCase.ID, "cache-prewarm", "cache-token"); err != nil {
		t.Fatal(err)
	}
	cleanedCacheHit, err := s.BuildJob(ctx, env.ID, readyCase.ID)
	if err != nil || cleanedCacheHit.CleanupError != "" || cleanedCacheHit.CleanupPending {
		t.Fatalf("finished cache-hit prewarm retained stale cleanup state: %+v, %v", cleanedCacheHit, err)
	}
	if err := s.CompleteBuildJob(ctx, env.ID, readyCase.ID, BuildPreparing, template.ID); err != nil {
		t.Fatal(err)
	}
	if cancelled, changed, err := s.CancelBuildJob(ctx, env.ID, readyCase.ID); err != nil || changed || cancelled.Status != BuildReady {
		t.Fatalf("cancel after completion = %+v, changed=%v, err=%v", cancelled, changed, err)
	}

	cancelCase := newBuildJobForTest(t, ctx, s, env.ID, "cancel-case")
	if claimed, err := s.ClaimBuildJob(ctx); err != nil || claimed.ID != cancelCase.ID {
		t.Fatalf("claim cancellation case = %+v, %v", claimed, err)
	}
	if _, err := s.ResolveBuildJob(ctx, env.ID, cancelCase.ID, "sha256:"+repeatDigestChar("2"), "sha256:"+repeatDigestChar("a"), "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	cancelCase, err = s.BuildJob(ctx, env.ID, cancelCase.ID)
	if err != nil {
		t.Fatal(err)
	}
	template, err = s.CreateTemplate(ctx, Template{
		EnvironmentID: env.ID, CacheKey: cancelCase.CacheKey, Spec: cancelCase.Spec,
		BaseRef: cancelCase.BaseRef, BaseDigest: cancelCase.BaseDigest,
		Platform: cancelCase.Platform, ExporterVersion: cancelCase.ExporterVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, template.ID, templateArtifacts()); err != nil {
		t.Fatal(err)
	}
	if cancelled, changed, err := s.CancelBuildJob(ctx, env.ID, cancelCase.ID); err != nil || !changed || cancelled.Status != BuildCancelled {
		t.Fatalf("cancel before completion = %+v, changed=%v, err=%v", cancelled, changed, err)
	}
	if err := s.CompleteBuildJob(ctx, env.ID, cancelCase.ID, BuildPreparing, template.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("completion overwrote cancellation: %v", err)
	}
	if err := s.FailBuildJob(ctx, env.ID, cancelCase.ID, "late failure"); !errors.Is(err, ErrConflict) {
		t.Fatalf("failure overwrote cancellation: %v", err)
	}
}

func TestBuildLogsAreScopedAndBounded(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateEnvironment(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	job := newBuildJobForTest(t, ctx, s, env.ID, "log-case")
	if err := s.PutBuildLog(ctx, env.ID, job.ID, strings.Repeat("x", maxBuildLogBytes+10), false); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBuildLog(ctx, other.ID, job.ID, "cross-environment", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment log write: %v", err)
	}
	log, truncated, err := s.BuildLog(ctx, env.ID, job.ID)
	if err != nil || len(log) != maxBuildLogBytes || !truncated {
		t.Fatalf("bounded log = len(%d), truncated=%v, err=%v", len(log), truncated, err)
	}
	if err := s.PutBuildLog(ctx, env.ID, job.ID, "short replacement", false); err != nil {
		t.Fatal(err)
	}
	log, truncated, err = s.BuildLog(ctx, env.ID, job.ID)
	if err != nil || log != "short replacement" || !truncated {
		t.Fatalf("log replacement cleared truncation history: content=%q, truncated=%v, err=%v", log, truncated, err)
	}
	if _, _, err := s.BuildLog(ctx, other.ID, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment log read: %v", err)
	}

	invalidBytes := "prefix" + string([]byte{0xff}) + "suffix"
	invalidJob := newBuildJobForTest(t, ctx, s, env.ID, "invalid-byte-log")
	if err := s.PutBuildLog(ctx, env.ID, invalidJob.ID, invalidBytes, false); err != nil {
		t.Fatal(err)
	}
	log, truncated, err = s.BuildLog(ctx, env.ID, invalidJob.ID)
	if err != nil || log != "prefix\uFFFDsuffix" || !utf8.ValidString(log) || truncated {
		t.Fatalf("invalid UTF-8 log = %q, valid=%v, truncated=%v, err=%v", log, utf8.ValidString(log), truncated, err)
	}
	nearLimit := strings.Repeat("x", maxBuildLogBytes-2) + string([]byte{0xff})
	if err := s.PutBuildLog(ctx, env.ID, invalidJob.ID, nearLimit, false); err != nil {
		t.Fatal(err)
	}
	log, truncated, err = s.BuildLog(ctx, env.ID, invalidJob.ID)
	if err != nil || len(log) > maxBuildLogBytes || !utf8.ValidString(log) || !truncated {
		t.Fatalf("normalized byte limit = len(%d), valid=%v, truncated=%v, err=%v", len(log), utf8.ValidString(log), truncated, err)
	}
}

func TestBuildJobEnvironmentAndTemplateReferences(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	job := newBuildJobForTest(t, ctx, s, env.ID, "foreign-key-case")
	if _, err := s.ClaimBuildJob(ctx); err != nil {
		t.Fatal(err)
	}
	job = resolveBuildJobForTest(t, ctx, s, env.ID, job.ID, "3", "a")
	template, err := s.CreateTemplate(ctx, Template{
		EnvironmentID: env.ID, CacheKey: job.CacheKey, Spec: job.Spec,
		BaseRef: job.BaseRef, BaseDigest: job.BaseDigest,
		Platform: job.Platform, ExporterVersion: job.ExporterVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, template.ID, templateArtifacts()); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteBuildJob(ctx, env.ID, job.ID, BuildPreparing, template.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", env.ID); err == nil {
		t.Fatal("deleted an environment while build history still references it")
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM templates WHERE id = ?", template.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.BuildJob(ctx, env.ID, job.ID)
	if err != nil || retained.TemplateID != "" || retained.Status != BuildReady {
		t.Fatalf("template deletion left a dangling reference or changed terminal status: %+v, %v", retained, err)
	}
}
