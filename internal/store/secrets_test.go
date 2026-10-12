package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
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

func TestSecretScopes(t *testing.T) {
	ctx := context.Background()
	s, work, other := openTeammateStore(t)
	if _, err := s.CreateProvider(ctx, Provider{ID: "p1", EnvironmentID: work.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(work.ID, "pk", "PROVIDER_ANTHROPIC_KEY")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(ctx, Provider{ID: "p2", EnvironmentID: other.ID, Name: "anthropic", Kind: "anthropic_api"}, providerSecret(other.ID, "pk2", "PROVIDER_ANTHROPIC_KEY")); err != nil {
		t.Fatal(err)
	}
	for _, tm := range []Teammate{
		{ID: "ada", EnvironmentID: work.ID, Name: "Ada", Harness: "claude", ProviderID: "p1", GitName: "Ada", GitEmail: "ada@agents.invalid"},
		{ID: "bob", EnvironmentID: work.ID, Name: "Bob", Harness: "claude", ProviderID: "p1", GitName: "Bob", GitEmail: "bob@agents.invalid"},
		{ID: "eve", EnvironmentID: other.ID, Name: "Eve", Harness: "claude", ProviderID: "p2", GitName: "Eve", GitEmail: "eve@agents.invalid"},
	} {
		if _, err := s.CreateTeammate(ctx, tm); err != nil {
			t.Fatal(err)
		}
	}
	sandbox := func(env, name, teammate string) Sandbox {
		sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env, Name: name, TeammateID: teammate})
		if err != nil {
			t.Fatal(err)
		}
		return sb
	}
	adaBox, ada2, bobBox, loose := sandbox(work.ID, "ada-box", "ada"), sandbox(work.ID, "ada-2", "ada"), sandbox(work.ID, "bob-box", "bob"), sandbox(work.ID, "loose", "")
	elsewhere := sandbox(other.ID, "elsewhere", "")

	n := 0
	create := func(sec Secret) error {
		n++
		sec.ID, sec.Placeholder = fmt.Sprintf("s%d", n), fmt.Sprintf("studio-%d", n)
		sec.Sealed, sec.Hosts = []byte("x"), []string{"a.com"}
		_, err := s.CreateSecret(ctx, sec)
		return err
	}
	for _, sec := range []Secret{
		{EnvironmentID: work.ID, Name: "KEY"},
		{EnvironmentID: work.ID, TeammateID: "ada", Name: "KEY"},
		{EnvironmentID: work.ID, TeammateID: "bob", Name: "KEY"},
		{EnvironmentID: work.ID, SandboxID: adaBox.ID, Name: "KEY"},
		{EnvironmentID: work.ID, SandboxID: bobBox.ID, Name: "KEY"},
		{EnvironmentID: work.ID, TeammateID: "ada", Name: "ADA_ONLY"},
		{EnvironmentID: work.ID, SandboxID: loose.ID, Name: "LOOSE_ONLY"},
	} {
		if err := create(sec); err != nil {
			t.Fatalf("%+v: %v", sec, err)
		}
	}
	// A name is unique per scope.
	for _, sec := range []Secret{
		{EnvironmentID: work.ID, Name: "KEY"},
		{EnvironmentID: work.ID, TeammateID: "ada", Name: "KEY"},
		{EnvironmentID: work.ID, SandboxID: adaBox.ID, Name: "KEY"},
	} {
		if err := create(sec); !errors.Is(err, ErrExists) {
			t.Fatalf("duplicate %+v: %v", sec, err)
		}
	}
	// One scope, and in the same environment.
	for _, sec := range []Secret{
		{EnvironmentID: work.ID, TeammateID: "ada", SandboxID: adaBox.ID, Name: "BOTH"},
		{EnvironmentID: work.ID, TeammateID: "eve", Name: "FOREIGN"},
		{EnvironmentID: work.ID, SandboxID: elsewhere.ID, Name: "FOREIGN"},
		{EnvironmentID: other.ID, SandboxID: adaBox.ID, Name: "FOREIGN"},
	} {
		if err := create(sec); err == nil {
			t.Fatalf("created %+v", sec)
		}
	}
	// Studio's own secrets and providers' keys belong to the environment.
	if _, err := s.db.ExecContext(ctx, "INSERT INTO secrets ("+secretCols+", studio_only, teammate_id) VALUES ('so', ?, 'FORGE_X_TOKEN', x'00', 'a.com', 'studio-so', '', 1, 1, 1, 'ada')", work.ID); err == nil {
		t.Fatal("a studio-only secret was scoped to a teammate")
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO providers (id, environment_id, name, kind, secret_id, created_at, updated_at) VALUES ('p3', ?, 'scoped', 'anthropic_api', 's2', 1, 1)", work.ID); err == nil {
		t.Fatal("a provider took a teammate's secret as its key")
	}
	// The scope never changes.
	for _, q := range []string{"UPDATE secrets SET teammate_id = NULL WHERE id = 's2'", "UPDATE secrets SET sandbox_id = '" + loose.ID + "' WHERE id = 's1'", "UPDATE secrets SET teammate_id = 'bob' WHERE id = 's2'"} {
		if _, err := s.db.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("%s: %v", q, err)
		}
	}

	list, err := s.Secrets(ctx, work.ID)
	if err != nil || len(list) != 8 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	if got, _ := s.SecretByID(ctx, work.ID, "s2"); got.TeammateID != "ada" || got.SandboxID != "" {
		t.Fatalf("teammate scope read back as %+v", got)
	}
	if got, _ := s.SecretByID(ctx, work.ID, "s4"); got.SandboxID != adaBox.ID || got.TeammateID != "" {
		t.Fatalf("sandbox scope read back as %+v", got)
	}

	// The most specific secret of each name wins.
	visible := func(sb Sandbox) map[string]string {
		out := map[string]string{}
		for _, sec := range VisibleSecrets(list, sb.ID, sb.TeammateID) {
			if _, dup := out[sec.Name]; dup {
				t.Fatalf("%s sees %s twice", sb.Name, sec.Name)
			}
			out[sec.Name] = sec.ID
		}
		return out
	}
	for _, tc := range []struct {
		sb   Sandbox
		want map[string]string
	}{
		{adaBox, map[string]string{"PROVIDER_ANTHROPIC_KEY": "pk", "KEY": "s4", "ADA_ONLY": "s6"}},
		{ada2, map[string]string{"PROVIDER_ANTHROPIC_KEY": "pk", "KEY": "s2", "ADA_ONLY": "s6"}},
		{bobBox, map[string]string{"PROVIDER_ANTHROPIC_KEY": "pk", "KEY": "s5"}},
		{loose, map[string]string{"PROVIDER_ANTHROPIC_KEY": "pk", "KEY": "s1", "LOOSE_ONLY": "s7"}},
		{Sandbox{EnvironmentID: work.ID}, map[string]string{"PROVIDER_ANTHROPIC_KEY": "pk", "KEY": "s1"}},
	} {
		if got := visible(tc.sb); !maps.Equal(got, tc.want) {
			t.Errorf("%s sees %v, want %v", tc.sb.Name, got, tc.want)
		}
	}

	// Deleting a sandbox or teammate deletes its secrets.
	if err := s.DeleteSandbox(ctx, work.ID, adaBox.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSandbox(ctx, work.ID, bobBox.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTeammate(ctx, work.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s3", "s4", "s5"} {
		if _, err := s.SecretByID(ctx, work.ID, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("secret %s outlived its scope: %v", id, err)
		}
	}
	if list, _ := s.Secrets(ctx, work.ID); len(list) != 5 {
		t.Errorf("%d secrets left, want 5", len(list))
	}
}
