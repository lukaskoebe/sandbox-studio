package updates

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/golang/snappy"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Repository is the GitHub repository releases come from.
const Repository = "lukaskoebe/sandbox-studio"

// actionsIssuer is the OIDC issuer of GitHub Actions, which signs the release workflow's
// attestations.
const actionsIssuer = "https://token.actions.githubusercontent.com"

// provenance is the predicate type of actions/attest-build-provenance.
const provenance = "https://slsa.dev/provenance/v1"

// verifyAttestation checks that GitHub has a build provenance attestation for the file with
// this sha256 digest, signed by the release workflow running for exactly this tag. The
// attestations come from GitHub's API; the signature, its certificate and its transparency
// log entry are checked against Sigstore's public-good trusted root.
func (in *Installer) verifyAttestation(ctx context.Context, digest []byte, tag string) error {
	bundles, err := in.attestations(ctx, digest)
	if err != nil {
		return err
	}
	if len(bundles) == 0 {
		return errors.New("GitHub has no attestation for the download")
	}
	tm, err := in.trustedRoot(ctx)
	if err != nil {
		return fmt.Errorf("Sigstore's trusted root: %w", err)
	}
	return verifyBundles(tm, bundles, digest, in.repo(), tag)
}

// verifyBundles accepts the digest if one of the bundles verifies.
func verifyBundles(tm root.TrustedMaterial, bundles []*bundle.Bundle, digest []byte, repo, tag string) error {
	v, err := verify.NewVerifier(tm, verify.WithSignedCertificateTimestamps(1), verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return err
	}
	san := "https://github.com/" + repo + "/.github/workflows/release.yml@refs/tags/" + tag
	id, err := verify.NewShortCertificateIdentity(actionsIssuer, "", san, "")
	if err != nil {
		return err
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(id))
	var errs []error
	for _, b := range bundles {
		res, err := v.Verify(b, policy)
		if err == nil && (res.Statement == nil || res.Statement.GetPredicateType() != provenance) {
			err = errors.New("the attestation is not build provenance")
		}
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("no attestation verifies as the release workflow's for %s: %w", tag, errors.Join(errs...))
}

// attestations fetches the attestation bundles GitHub has for a digest. A bundle the listing
// leaves out is fetched from its bundle_url, snappy-compressed.
func (in *Installer) attestations(ctx context.Context, digest []byte) ([]*bundle.Bundle, error) {
	api := in.APIURL
	if api == "" {
		api = "https://api.github.com"
	}
	u := api + "/repos/" + in.repo() + "/attestations/sha256:" + hex.EncodeToString(digest)
	var list struct {
		Attestations []struct {
			Bundle    json.RawMessage `json:"bundle"`
			BundleURL string          `json:"bundle_url"`
		} `json:"attestations"`
	}
	body, err := in.get(ctx, u, 4<<20, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("fetch the attestations: %w", err)
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("read the attestations: %w", err)
	}
	var out []*bundle.Bundle
	for _, a := range list.Attestations {
		raw := []byte(a.Bundle)
		if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
			if !strings.HasPrefix(a.BundleURL, "https://") {
				continue
			}
			packed, err := in.get(ctx, a.BundleURL, 4<<20, "")
			if err != nil {
				return nil, fmt.Errorf("fetch an attestation: %w", err)
			}
			if raw, err = snappy.Decode(nil, packed); err != nil {
				raw = packed
			}
		}
		var b bundle.Bundle
		if err := b.UnmarshalJSON(raw); err != nil {
			return nil, fmt.Errorf("read an attestation: %w", err)
		}
		out = append(out, &b)
	}
	return out, nil
}

// trustedRoot is Sigstore's public-good trusted root, from its TUF repository with the
// metadata cached in the updates directory.
func (in *Installer) trustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	if in.TrustedRoot != nil {
		return in.TrustedRoot(ctx)
	}
	opts := tuf.DefaultOptions().WithCachePath(filepath.Join(in.Dir, "tuf")).WithContext(ctx)
	return root.FetchTrustedRootWithOptions(opts)
}

func (in *Installer) repo() string {
	if in.Repo != "" {
		return in.Repo
	}
	return Repository
}

// get fetches a URL into memory, at most limit bytes.
func (in *Installer) get(ctx context.Context, u string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "sandbox-studio/"+in.Current)
	resp, err := in.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", req.URL.Host, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s sent more than %d bytes", req.URL.Host, limit)
	}
	return body, nil
}
