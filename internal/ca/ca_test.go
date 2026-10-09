package ca

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeSealer stands in for the vault: it refuses to unseal with a different AAD.
type fakeSealer struct{}

func (fakeSealer) Seal(plaintext, aad []byte) []byte {
	return append(append([]byte{byte(len(aad))}, aad...), plaintext...)
}

func (fakeSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	n := int(sealed[0])
	if !bytes.Equal(sealed[1:1+n], aad) {
		return nil, errors.New("wrong aad")
	}
	return sealed[1+n:], nil
}

func setup(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	work, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.CreateEnvironment(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	return st, work.ID, private.ID
}

func pool(t *testing.T, a *Authority, envID string) *x509.CertPool {
	t.Helper()
	pemBytes, err := a.CertPEM(context.Background(), envID)
	if err != nil {
		t.Fatal(err)
	}
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(pemBytes) {
		t.Fatal("CA PEM did not parse")
	}
	return p
}

func TestLeavesVerifyOnlyInTheirEnvironment(t *testing.T) {
	st, work, private := setup(t)
	ctx := context.Background()
	a := &Authority{Store: st, Sealer: fakeSealer{}}

	for _, host := range []string{"api.example.com", "203.0.113.9", "2001:db8::1"} {
		leaf, err := a.Leaf(ctx, work, host)
		if err != nil {
			t.Fatal(err)
		}
		opts := x509.VerifyOptions{DNSName: host, Roots: pool(t, a, work), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if _, err := leaf.Leaf.Verify(opts); err != nil {
			t.Errorf("%s: %v", host, err)
		}
		opts.Roots = pool(t, a, private)
		if _, err := leaf.Leaf.Verify(opts); err == nil {
			t.Errorf("%s: a work leaf verified against the private CA", host)
		}
	}
	first, _ := a.Leaf(ctx, work, "api.example.com")
	if again, _ := a.Leaf(ctx, work, "api.example.com"); again != first {
		t.Error("leaf not cached")
	}
}

func TestCAPersists(t *testing.T) {
	st, work, _ := setup(t)
	ctx := context.Background()
	first, err := (&Authority{Store: st, Sealer: fakeSealer{}}).CertPEM(ctx, work)
	if err != nil {
		t.Fatal(err)
	}
	// A restarted Studio loads the same CA and can still sign with it.
	again := &Authority{Store: st, Sealer: fakeSealer{}}
	second, err := again.CertPEM(ctx, work)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("CA changed after a restart")
	}
	block, _ := pem.Decode(second)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !cert.IsCA || !cert.MaxPathLenZero {
		t.Fatalf("CA certificate: %v %+v", err, cert)
	}
	if _, err := again.Leaf(ctx, work, "example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestSealedKeyIsBoundToItsEnvironment(t *testing.T) {
	st, work, private := setup(t)
	ctx := context.Background()
	if _, err := (&Authority{Store: st, Sealer: fakeSealer{}}).CertPEM(ctx, work); err != nil {
		t.Fatal(err)
	}
	cert, sealed, err := st.EnvironmentCA(ctx, work)
	if err != nil {
		t.Fatal(err)
	}
	// Copying work's CA row to another environment must not yield a usable CA there.
	if _, _, err := st.AddEnvironmentCA(ctx, private, cert, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Authority{Store: st, Sealer: fakeSealer{}}).Leaf(ctx, private, "example.com"); err == nil {
		t.Fatal("a CA key sealed for one environment worked in another")
	}
}
