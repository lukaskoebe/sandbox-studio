package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestSecretsCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, _ := s.CreateEnvironment(ctx, "work")
	other, _ := s.CreateEnvironment(ctx, "other")

	if _, err := s.CreateSecret(ctx, Secret{ID: "s1", EnvironmentID: work.ID, Name: "API_KEY", Sealed: []byte("sealed-1"), Hosts: []string{"api.example.com", "*.example.org"}, Placeholder: "studio-a", Note: "docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSecret(ctx, Secret{ID: "s2", EnvironmentID: work.ID, Name: "API_KEY", Sealed: []byte("x"), Hosts: []string{"a.com"}, Placeholder: "studio-b"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate name in one environment: %v", err)
	}
	if _, err := s.CreateSecret(ctx, Secret{ID: "s3", EnvironmentID: other.ID, Name: "API_KEY", Sealed: []byte("y"), Hosts: []string{"a.com"}, Placeholder: "studio-c"}); err != nil {
		t.Fatalf("same name in another environment rejected: %v", err)
	}
	if _, err := s.CreateSecret(ctx, Secret{ID: "s4", EnvironmentID: work.ID, Name: "ALPHA", Sealed: []byte("z"), Hosts: []string{"a.com"}, Placeholder: "studio-d"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.SecretByID(ctx, work.ID, "s1")
	if err != nil || got.Name != "API_KEY" || string(got.Sealed) != "sealed-1" || got.Placeholder != "studio-a" || got.Note != "docs" ||
		!slices.Equal(got.Hosts, []string{"api.example.com", "*.example.org"}) {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := s.SecretByID(ctx, other.ID, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment read: %v", err)
	}

	// Without a sealed value the stored one is kept; the name and placeholder never change.
	updated, err := s.UpdateSecret(ctx, Secret{ID: "s1", EnvironmentID: work.ID, Hosts: []string{"api.example.net"}, Note: "moved"})
	if err != nil || string(updated.Sealed) != "sealed-1" || updated.Name != "API_KEY" || updated.Placeholder != "studio-a" ||
		!slices.Equal(updated.Hosts, []string{"api.example.net"}) || updated.Note != "moved" {
		t.Fatalf("update without value: %+v %v", updated, err)
	}
	updated, err = s.UpdateSecret(ctx, Secret{ID: "s1", EnvironmentID: work.ID, Sealed: []byte("sealed-2"), Hosts: []string{"api.example.net"}})
	if err != nil || string(updated.Sealed) != "sealed-2" {
		t.Fatalf("update with value: %+v %v", updated, err)
	}
	if _, err := s.UpdateSecret(ctx, Secret{ID: "s1", EnvironmentID: other.ID, Hosts: []string{"a.com"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment update: %v", err)
	}

	list, err := s.Secrets(ctx, work.ID)
	if err != nil || len(list) != 2 || list[0].Name != "ALPHA" || list[1].Name != "API_KEY" {
		t.Fatalf("list ordered by name: %+v %v", list, err)
	}

	if err := s.DeleteSecret(ctx, other.ID, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment delete: %v", err)
	}
	if err := s.DeleteSecret(ctx, work.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSecret(ctx, work.ID, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestSecretsDeletedWithEnvironment(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, _ := s.CreateEnvironment(ctx, "work")
	if _, err := s.CreateSecret(ctx, Secret{ID: "s1", EnvironmentID: env.ID, Name: "KEY", Sealed: []byte("x"), Hosts: []string{"a.com"}, Placeholder: "studio-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ?", env.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := s.Secrets(ctx, env.ID); err != nil || len(list) != 0 {
		t.Fatalf("secrets outlived their environment: %+v %v", list, err)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Setting(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing setting: %v", err)
	}
	if err := s.SetSetting(ctx, "mode", []byte("keychain")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "mode", []byte("file")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Setting(ctx, "mode"); err != nil || string(v) != "file" {
		t.Fatalf("setting after overwrite: %q %v", v, err)
	}
}
