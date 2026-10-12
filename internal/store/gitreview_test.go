package store

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testHostKey(t *testing.T) (string, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))), ssh.FingerprintSHA256(k)
}

func TestForgeHostKeys(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	hostKey, fp := testHostKey(t)
	other, otherFP := testHostKey(t)
	f, err := st.CreateForge(ctx, Forge{ID: NewID(), EnvironmentID: env.ID, Name: "box", Kind: "ssh", BaseURL: "ssh://git@box.example.net", Username: "git",
		Auth: ForgeAuthKey, PublicKey: "ssh-ed25519 AAAA studio", HostKey: hostKey}, Secret{ID: NewID(), Name: "FORGE_BOX_SSH_KEY", Sealed: []byte("sealed"), Placeholder: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if f.HostKeyFingerprint != fp || f.HostKeyMismatch != "" || f.PublicKey == "" {
		t.Fatalf("created: %+v", f)
	}
	if err := st.SetForgeHostKeyOffered(ctx, env.ID, f.ID, "SHA256:offered"); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); f.HostKeyMismatch != "SHA256:offered" || f.HostKey != hostKey {
		t.Fatalf("offered: %+v", f)
	}
	if err := st.PinForgeHostKey(ctx, env.ID, f.ID, other); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); f.HostKeyMismatch != "" || f.HostKey != other || f.HostKeyFingerprint != otherFP {
		t.Fatalf("re-pinned: %+v", f)
	}
	if err := st.PinForgeHostKey(ctx, env.ID, "nope", other); err == nil {
		t.Fatal("pinned a missing forge")
	}
}

func TestForgeAgentAuth(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	hostKey, _ := testHostKey(t)
	// An agent forge has no secret, so nothing reaches the vault.
	f, err := st.CreateForge(ctx, Forge{ID: NewID(), EnvironmentID: env.ID, Name: "box", Kind: "ssh", BaseURL: "ssh://git@box.example.net",
		Username: "git", Auth: ForgeAuthAgent, HostKey: hostKey}, Secret{ID: NewID(), Name: "UNUSED", Sealed: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if f.Auth != ForgeAuthAgent || f.SecretID != "" {
		t.Fatalf("agent forge: %+v", f)
	}
	if secrets, _ := st.Secrets(ctx, env.ID); len(secrets) != 0 {
		t.Fatalf("agent forge stored %d secrets", len(secrets))
	}
	if n, err := st.AgentForges(ctx); err != nil || n != 1 {
		t.Fatalf("agent forges = %d, %v", n, err)
	}

	// Switching to a key generates its secret; switching back to the agent deletes it.
	key := Secret{ID: NewID(), Name: "FORGE_BOX_SSH_KEY", Sealed: []byte("sealed"), Placeholder: "box"}
	if err := st.SetForgeKey(ctx, env.ID, f.ID, "ssh-ed25519 AAAA studio", key); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); f.Auth != ForgeAuthKey || f.SecretID != key.ID || f.PublicKey == "" {
		t.Fatalf("key forge: %+v", f)
	}
	if err := st.SetForgeKey(ctx, env.ID, f.ID, "ssh-ed25519 BBBB", Secret{ID: NewID(), Name: "OTHER", Sealed: []byte("x")}); !errors.Is(err, ErrExists) {
		t.Fatalf("second key: %v", err)
	}
	if err := st.SetForgeAgent(ctx, env.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); f.Auth != ForgeAuthAgent || f.SecretID != "" || f.PublicKey != "" {
		t.Fatalf("agent again: %+v", f)
	}
	if secrets, _ := st.Secrets(ctx, env.ID); len(secrets) != 0 {
		t.Fatalf("the key was not deleted: %d secrets", len(secrets))
	}

	// A slow signature is recorded, and the hint it leads to can be dismissed.
	if f.AgentSlowAt != nil || f.AgentHintDismissed {
		t.Fatalf("hint before any slow sign: %+v", f)
	}
	if err := st.SetForgeAgentSlow(ctx, env.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); f.AgentSlowAt == nil {
		t.Fatalf("slow sign not recorded: %+v", f)
	}
	if err := st.DismissForgeAgentHint(ctx, env.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Forge(ctx, env.ID, f.ID); !f.AgentHintDismissed {
		t.Fatalf("hint not dismissed: %+v", f)
	}
	if err := st.DismissForgeAgentHint(ctx, env.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dismissed a missing forge: %v", err)
	}

	// The database refuses a key or agent forge that doesn't match its secret, and a key
	// forge with a secret that isn't studio-only.
	plain, err := st.CreateSecret(ctx, Secret{ID: NewID(), EnvironmentID: env.ID, Name: "PLAIN", Sealed: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO forges (id, environment_id, name, kind, base_url, auth, secret_id, created_at, updated_at) VALUES (?, ?, 'plain', 'ssh', 'ssh://git@plain.example.net', 'key', ?, 1, 1)",
		"INSERT INTO forges (id, environment_id, name, kind, base_url, auth, secret_id, created_at, updated_at) VALUES (?, ?, 'agent', 'ssh', 'ssh://git@agent.example.net', 'agent', ?, 1, 1)",
	} {
		if _, err := st.DB().ExecContext(ctx, q, NewID(), env.ID, plain.ID); err == nil {
			t.Fatalf("accepted %s", q)
		}
	}
	if err := st.DeleteForge(ctx, env.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Forge(ctx, env.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted forge: %v", err)
	}
}
