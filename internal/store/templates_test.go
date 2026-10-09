package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func templateForEnvironment(envID string, cacheChar string) Template {
	return Template{
		EnvironmentID:   envID,
		CacheKey:        "sha256:" + repeatDigestChar(cacheChar),
		Spec:            "{\n  \"schema\": \"studio\", \"cpus\": 2\n}",
		BaseRef:         "example.test/base:stable",
		BaseDigest:      "sha256:" + repeatDigestChar("a"),
		Platform:        "linux/amd64",
		ExporterVersion: "exporter-v1",
	}
}

func repeatDigestChar(c string) string {
	return strings.Repeat(c, 64)
}

func templateArtifacts() []TemplateArtifact {
	return []TemplateArtifact{
		{Role: "manifest", Digest: "sha256:" + repeatDigestChar("b"), MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 101},
		{Role: "config", Digest: "sha256:" + repeatDigestChar("c"), MediaType: "application/vnd.oci.image.config.v1+json", Size: 202},
		{Role: "layer", Digest: "sha256:" + repeatDigestChar("d"), MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Size: 303},
	}
}

func TestTemplateMigrationPersistsTemplateAndArtifacts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	in := templateForEnvironment(env.ID, "e")
	created, err := s.CreateTemplate(ctx, in)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	artifacts := templateArtifacts()
	if err := s.ReadyTemplate(ctx, env.ID, created.ID, artifacts); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.TemplateByKey(ctx, env.ID, in.CacheKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Spec != in.Spec || got.State != TemplateStateReady || !reflect.DeepEqual(got.Artifacts, artifacts) {
		t.Fatalf("reopened template %+v, want ID %q, ready state, original spec and artifacts", got, created.ID)
	}
	all, err := s.Templates(ctx, "")
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[0].Artifacts, artifacts) {
		t.Fatalf("all templates after reopen: %+v, %v", all, err)
	}
}

func TestTemplateEnvironmentIsolationAndDuplicateCacheKey(t *testing.T) {
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

	in := templateForEnvironment(work.ID, "e")
	in.ID, in.State, in.Artifacts, in.CreatedAt = "caller-id", TemplateStateReady, templateArtifacts(), time.Unix(1, 0)
	created, err := s.CreateTemplate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == in.ID || created.State != TemplateStateCreating || len(created.Artifacts) != 0 || created.CreatedAt.Equal(in.CreatedAt) {
		t.Fatalf("CreateTemplate did not assign its own generated fields: %+v", created)
	}
	if created.Spec != in.Spec {
		t.Fatalf("spec was not persisted verbatim: %q", created.Spec)
	}
	if _, err := s.CreateTemplate(ctx, templateForEnvironment(work.ID, "e")); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate cache key in one environment: %v", err)
	}
	other, err := s.CreateTemplate(ctx, templateForEnvironment(private.ID, "e"))
	if err != nil {
		t.Fatalf("same cache key in another environment: %v", err)
	}
	if _, err := s.Template(ctx, private.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment template read: %v", err)
	}
	if got, err := s.TemplateByKey(ctx, private.ID, created.CacheKey); err != nil || got.ID != other.ID {
		t.Fatalf("private environment did not resolve its own key: %+v, %v", got, err)
	}
	if got, err := s.TemplateByKey(ctx, work.ID, other.CacheKey); err != nil || got.ID != created.ID {
		t.Fatalf("shared cache key returned another environment's template: %+v, %v", got, err)
	}
	if err := s.SetTemplateDeleting(ctx, private.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment state change: %v", err)
	}
	if err := s.DeleteTemplate(ctx, private.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment delete: %v", err)
	}
	workTemplates, err := s.Templates(ctx, work.ID)
	if err != nil || len(workTemplates) != 1 || workTemplates[0].ID != created.ID {
		t.Fatalf("work templates: %+v, %v", workTemplates, err)
	}
	privateTemplates, err := s.Templates(ctx, private.ID)
	if err != nil || len(privateTemplates) != 1 || privateTemplates[0].ID != other.ID {
		t.Fatalf("private templates: %+v, %v", privateTemplates, err)
	}
}

func TestTemplateLifecycleAndArtifactImmutability(t *testing.T) {
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
	tmpl, err := s.CreateTemplate(ctx, templateForEnvironment(env.ID, "e"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, tmpl.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted a creating template: %v", err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, tmpl.ID, templateArtifacts()[:2]); err == nil {
		t.Fatal("accepted an incomplete artifact set")
	}
	got, err := s.Template(ctx, env.ID, tmpl.ID)
	if err != nil || got.State != TemplateStateCreating || len(got.Artifacts) != 0 {
		t.Fatalf("failed readiness update was not atomic: %+v, %v", got, err)
	}

	wantArtifacts := templateArtifacts()
	if err := s.ReadyTemplate(ctx, env.ID, tmpl.ID, wantArtifacts); err != nil {
		t.Fatal(err)
	}
	replacement := templateArtifacts()
	replacement[0].Digest = "sha256:" + repeatDigestChar("f")
	if err := s.ReadyTemplate(ctx, env.ID, tmpl.ID, replacement); !errors.Is(err, ErrConflict) {
		t.Fatalf("replaced ready artifacts: %v", err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, tmpl.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted a ready template directly: %v", err)
	}
	if err := s.SetTemplateDeleting(ctx, env.ID, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTemplateDeleting(ctx, env.ID, tmpl.ID); err != nil {
		t.Fatalf("repeated deleting transition: %v", err)
	}
	got, err = s.Template(ctx, env.ID, tmpl.ID)
	if err != nil || got.State != TemplateStateDeleting || !reflect.DeepEqual(got.Artifacts, wantArtifacts) {
		t.Fatalf("template while deleting: %+v, %v", got, err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, tmpl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second template deletion: %v", err)
	}
	var artifacts int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM template_artifacts WHERE template_id = ?", tmpl.ID).Scan(&artifacts); err != nil || artifacts != 0 {
		t.Fatalf("template deletion did not cascade its artifacts: count=%d err=%v", artifacts, err)
	}

	creating, err := s.CreateTemplate(ctx, templateForEnvironment(env.ID, "1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTemplateDeleting(ctx, env.ID, creating.ID); err != nil {
		t.Fatalf("mark creating template for deletion: %v", err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, creating.ID); err != nil {
		t.Fatalf("delete creating template after marking: %v", err)
	}
}

func TestTemplateEnvironmentCascadeRemovesArtifacts(t *testing.T) {
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
	tmpl, err := s.CreateTemplate(ctx, templateForEnvironment(env.ID, "e"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadyTemplate(ctx, env.ID, tmpl.ID, templateArtifacts()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", env.ID); err != nil {
		t.Fatal(err)
	}
	var templates, artifacts int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM templates WHERE environment_id = ?", env.ID).Scan(&templates); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM template_artifacts WHERE template_id = ?", tmpl.ID).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if templates != 0 || artifacts != 0 {
		t.Fatalf("environment cascade left templates=%d artifacts=%d", templates, artifacts)
	}
}

func TestCreateSettingIsRaceSafeAndDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const callers = 24
	type result struct {
		value []byte
		err   error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := []byte(fmt.Sprintf("sealed-seed-%02d", i))
			stored, err := s.CreateSetting(ctx, "registry-seed", value)
			results <- result{value: stored, err: err}
		}(i)
	}
	wg.Wait()
	close(results)

	var winner []byte
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent CreateSetting: %v", result.err)
		}
		if winner == nil {
			winner = append([]byte(nil), result.value...)
		} else if string(result.value) != string(winner) {
			t.Fatalf("CreateSetting returned different winners: %q and %q", winner, result.value)
		}
	}
	stored, err := s.CreateSetting(ctx, "registry-seed", []byte("replacement"))
	if err != nil || string(stored) != string(winner) {
		t.Fatalf("CreateSetting replaced the existing value: %q, %v", stored, err)
	}
}
