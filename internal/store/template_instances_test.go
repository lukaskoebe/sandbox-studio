package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func createReadyTemplate(t *testing.T, ctx context.Context, s *Store, envID, cacheChar string) Template {
	t.Helper()
	tmpl, err := s.CreateTemplate(ctx, templateForEnvironment(envID, cacheChar))
	if err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if err := s.ReadyTemplate(ctx, envID, tmpl.ID, templateArtifacts()); err != nil {
		t.Fatalf("ReadyTemplate() error = %v", err)
	}
	return tmpl
}

func TestCreateSandboxRequiresSameEnvironmentReadyTemplate(t *testing.T) {
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
	creating, err := s.CreateTemplate(ctx, templateForEnvironment(work.ID, "e"))
	if err != nil {
		t.Fatal(err)
	}
	privateReady := createReadyTemplate(t, ctx, s, private.ID, "f")
	workReady := createReadyTemplate(t, ctx, s, work.ID, "1")

	cases := []struct {
		name       string
		templateID string
		wantErr    error
	}{
		{name: "missing", templateID: "missing-template", wantErr: ErrNotFound},
		{name: "wrong environment", templateID: privateReady.ID, wantErr: ErrNotFound},
		{name: "not ready", templateID: creating.ID, wantErr: ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "rejected-" + tc.name, TemplateID: tc.templateID})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateSandbox() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
	tryRawAttachment := func(environmentID, templateID string) error {
		_, err := s.db.ExecContext(ctx, "INSERT INTO sandboxes (id, environment_id, build_job_id, template_id, name, generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib, created_at, dns_port) VALUES (?, ?, NULL, ?, ?, 1, 1, 512, 512, 1024, 1024, ?, ?)",
			NewID(), environmentID, templateID, "raw-"+NewID(), now(), firstDNSPort+1)
		return err
	}
	if err := tryRawAttachment(work.ID, privateReady.ID); err == nil {
		t.Fatal("database allowed a cross-environment template attachment")
	}
	if err := tryRawAttachment(work.ID, creating.ID); err == nil {
		t.Fatal("database allowed an attachment to a non-ready template")
	}

	created, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "from-template", TemplateID: workReady.ID})
	if err != nil {
		t.Fatalf("CreateSandbox(ready template) error = %v", err)
	}
	if created.TemplateID != workReady.ID {
		t.Fatalf("CreateSandbox() TemplateID = %q, want %q", created.TemplateID, workReady.ID)
	}
	got, err := s.Sandbox(ctx, work.ID, created.ID)
	if err != nil || got.TemplateID != workReady.ID {
		t.Fatalf("Sandbox() = %+v, %v; want template %q", got, err, workReady.ID)
	}
	listed, err := s.Sandboxes(ctx, work.ID)
	if err != nil || len(listed) != 1 || listed[0].TemplateID != workReady.ID {
		t.Fatalf("Sandboxes() = %+v, %v; want one template-pinned sandbox", listed, err)
	}
}

func TestTemplateCannotBeMarkedDeletingOrRemovedWhileReferenced(t *testing.T) {
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
	tmpl := createReadyTemplate(t, ctx, s, env.ID, "e")
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "pinned", TemplateID: tmpl.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTemplateDeleting(ctx, env.ID, tmpl.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetTemplateDeleting() error = %v, want ErrConflict", err)
	}
	if err := s.DeleteTemplate(ctx, env.ID, tmpl.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteTemplate() error = %v, want ErrConflict", err)
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE templates SET state = ? WHERE id = ?", TemplateStateDeleting, tmpl.ID); err == nil {
		t.Fatal("database allowed a referenced ready template to become deleting")
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM templates WHERE id = ?", tmpl.ID); err == nil {
		t.Fatal("database allowed a referenced template to be deleted")
	}
	got, err := s.Template(ctx, env.ID, tmpl.ID)
	if err != nil || got.State != TemplateStateReady || len(got.Artifacts) != 3 {
		t.Fatalf("referenced template after rejected transitions = %+v, %v", got, err)
	}
}

func TestSandboxTemplateCreateAndDeleteAcrossStoreHandles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	creator, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	deleter, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer deleter.Close()
	env, err := creator.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := createReadyTemplate(t, ctx, creator, env.ID, "e")

	start := make(chan struct{})
	type result struct {
		operation string
		err       error
	}
	results := make(chan result, 2)
	go func() {
		<-start
		_, err := creator.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "race", TemplateID: tmpl.ID})
		results <- result{operation: "create", err: err}
	}()
	go func() {
		<-start
		results <- result{operation: "delete", err: deleter.SetTemplateDeleting(ctx, env.ID, tmpl.ID)}
	}()
	close(start)
	first, second := <-results, <-results
	createErr, deleteErr := first.err, second.err
	if first.operation == "delete" {
		deleteErr, createErr = first.err, second.err
	}

	current, err := deleter.Template(ctx, env.ID, tmpl.ID)
	if err != nil {
		t.Fatalf("Template() after concurrent operations: %v", err)
	}
	sandboxes, err := deleter.Sandboxes(ctx, env.ID)
	if err != nil {
		t.Fatalf("Sandboxes() after concurrent operations: %v", err)
	}
	var attached bool
	for _, sb := range sandboxes {
		if sb.TemplateID == tmpl.ID {
			attached = true
		}
	}
	if attached && current.State != TemplateStateReady {
		t.Fatalf("sandbox references template in %q state (create error %v, delete error %v)", current.State, createErr, deleteErr)
	}
	if current.State == TemplateStateDeleting && attached {
		t.Fatal("concurrent creation left a reference to a deleting template")
	}
	if createErr == nil && deleteErr == nil {
		t.Fatalf("both conflicting operations succeeded (template=%q, attached=%v)", current.State, attached)
	}
}
