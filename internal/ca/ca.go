// Package ca issues the TLS certificates the gateway presents when it intercepts a
// sandbox's connection. Every environment has its own CA, which only that environment's
// sandboxes trust.
package ca

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 7 * 24 * time.Hour
	// A cached leaf is replaced this long before it expires, so a connection never gets
	// one that runs out mid-session.
	leafRenewal = 24 * time.Hour
	// Bounds the leaves kept per environment; beyond it the cache starts over.
	maxLeaves = 1024
)

// Sealer encrypts the CA private keys at rest (the secrets vault).
type Sealer interface {
	Seal(plaintext, aad []byte) []byte
	Unseal(sealed, aad []byte) ([]byte, error)
}

// Authority keeps the environments' CAs, creating each on first use.
type Authority struct {
	Store  *store.Store
	Sealer Sealer

	mu  sync.Mutex
	cas map[string]*authority // by environment ID
}

type authority struct {
	cert *x509.Certificate
	key  crypto.Signer
	// leafKey signs for every host: a fresh key per leaf would cost time on every new host
	// and buys nothing, since all leaves are only ever trusted by this environment.
	leafKey *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// CertPEM returns the environment's CA certificate, for its sandboxes to trust.
func (a *Authority) CertPEM(ctx context.Context, envID string) ([]byte, error) {
	ca, err := a.load(ctx, envID)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), nil
}

// Leaf returns a certificate for host (a DNS name or an IP address) signed by the
// environment's CA.
func (a *Authority) Leaf(ctx context.Context, envID, host string) (*tls.Certificate, error) {
	ca, err := a.load(ctx, envID)
	if err != nil {
		return nil, err
	}
	return ca.leaf(host)
}

func (a *Authority) load(ctx context.Context, envID string) (*authority, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ca, ok := a.cas[envID]; ok {
		return ca, nil
	}
	certDER, sealed, err := a.Store.EnvironmentCA(ctx, envID)
	if errors.Is(err, store.ErrNotFound) {
		certDER, sealed, err = a.create(ctx, envID)
	}
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("the CA of environment %s: %w", envID, err)
	}
	keyDER, err := a.Sealer.Unseal(sealed, aad(envID))
	if err != nil {
		return nil, fmt.Errorf("the CA key of environment %s: %w", envID, err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, fmt.Errorf("the CA key of environment %s: %w", envID, err)
	}
	key, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("the CA key of environment %s can't sign", envID)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	ca := &authority{cert: cert, key: key, leafKey: leafKey, leaves: map[string]*tls.Certificate{}}
	if a.cas == nil {
		a.cas = map[string]*authority{}
	}
	a.cas[envID] = ca
	return ca, nil
}

func (a *Authority) create(ctx context.Context, envID string) (cert, sealedKey []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{Organization: []string{"Sandbox Studio"}, CommonName: "Sandbox Studio CA " + envID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return a.Store.AddEnvironmentCA(ctx, envID, der, a.Sealer.Seal(keyDER, aad(envID)))
}

func (ca *authority) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.leaves[host]; ok && time.Until(c.Leaf.NotAfter) > leafRenewal {
		return c, nil
	}
	now := time.Now()
	notAfter := now.Add(leafLifetime)
	if ca.cert.NotAfter.Before(notAfter) {
		notAfter = ca.cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, ca.leafKey.Public(), ca.key)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: ca.leafKey, Leaf: parsed}
	if len(ca.leaves) >= maxLeaves {
		clear(ca.leaves)
	}
	ca.leaves[host] = c
	return c, nil
}

// aad binds a sealed CA key to its environment, so it can't be moved to another one.
func aad(envID string) []byte { return []byte("ca:" + envID) }

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
