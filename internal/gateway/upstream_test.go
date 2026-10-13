package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestUpstreamOf(t *testing.T) {
	cases := []struct {
		upstream string
		host     string
		port     int
		wantHost string
		wantPort int
	}{
		{"", "api.example.com", 443, "api.example.com", 443},
		{"up.example", "api.example.com", 8443, "up.example", 8443},
		{"up.example:9443", "api.example.com", 443, "up.example", 9443},
		{"2001:db8::9", "api.example.com", 443, "2001:db8::9", 443},
		{"[2001:db8::9]:8443", "api.example.com", 443, "2001:db8::9", 8443},
	}
	for _, c := range cases {
		if h, p := upstreamOf(c.upstream, c.host, c.port); h != c.wantHost || p != c.wantPort {
			t.Errorf("upstreamOf(%q): %s:%d, want %s:%d", c.upstream, h, p, c.wantHost, c.wantPort)
		}
	}
}

// TestUpstreamCAsAddTheRulesCA: a proxy rule's CA is trusted for its upstream, next to the roots
// the gateway trusts, and it is refused if it holds no certificate.
func TestUpstreamCAsAddTheRulesCA(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Private CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "upstream.example"}, DNSNames: []string{"upstream.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(leafDER)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	verifies := func(roots *x509.CertPool) bool {
		_, err := leaf.Verify(x509.VerifyOptions{DNSName: "upstream.example", Roots: roots})
		return err == nil
	}

	g := &Gateway{upstreamRoots: x509.NewCertPool()}
	if pool, err := g.upstreamCAs(""); err != nil || verifies(pool) {
		t.Fatalf("without a CA the upstream is trusted: %v", err)
	}
	pool, err := g.upstreamCAs(caPEM)
	if err != nil || !verifies(pool) {
		t.Fatalf("with the rule's CA the upstream is not trusted: %v", err)
	}
	if verifies(g.upstreamRoots) {
		t.Fatal("the rule's CA was added to the gateway's own roots")
	}
	if _, err := g.upstreamCAs("not a certificate"); err == nil {
		t.Fatal("a CA without certificates was accepted")
	}
}
