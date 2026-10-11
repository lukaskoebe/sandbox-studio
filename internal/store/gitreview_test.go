package store

import (
	"context"
	"crypto/ed25519"
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
		PublicKey: "ssh-ed25519 AAAA studio", HostKey: hostKey}, Secret{ID: NewID(), Name: "FORGE_BOX_SSH_KEY", Sealed: []byte("sealed"), Placeholder: "box"})
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
