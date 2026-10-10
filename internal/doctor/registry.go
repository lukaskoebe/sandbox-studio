package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Registry checks anonymously that an OCI registry serves an image manifest.
type Registry struct {
	Client *http.Client
	Scheme string // "https" when empty; tests use "http"
}

var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Pullable fetches the manifest's headers for ref, such as
// ghcr.io/owner/image@sha256:… or ghcr.io/owner/image:tag, getting an anonymous pull
// token first when the registry asks for one.
func (r *Registry) Pullable(ctx context.Context, ref string) error {
	host, repo, reference, err := splitRef(ref)
	if err != nil {
		return err
	}
	scheme := r.Scheme
	if scheme == "" {
		scheme = "https"
	}
	manifest := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, host, repo, reference)
	resp, err := r.head(ctx, manifest, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		token, err := r.token(ctx, resp.Header.Get("WWW-Authenticate"), repo)
		if err != nil {
			return err
		}
		if resp, err = r.head(ctx, manifest, token); err != nil {
			return err
		}
	}
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return errors.New("the registry has no such image")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("the image is not public")
	}
	return fmt.Errorf("the registry answered %s", resp.Status)
}

func (r *Registry) head(ctx context.Context, u, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestTypes)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return resp, nil
}

// token gets an anonymous pull token from the realm named in a Bearer challenge.
func (r *Registry) token(ctx context.Context, challenge, repo string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", errors.New("the registry wants credentials")
	}
	p := parseChallenge(params)
	realm, err := url.Parse(p["realm"])
	if err != nil || p["realm"] == "" {
		return "", errors.New("the registry sent no token realm")
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	scope := p["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the registry refused an anonymous token: %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

// parseChallenge reads key="value" pairs separated by commas.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for s != "" {
		s = strings.TrimLeft(s, " ,")
		key, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			value, s = rest[1:1+end], rest[2+end:]
		} else {
			value, s, _ = strings.Cut(rest, ",")
		}
		out[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return out
}

// splitRef splits host/repo[:tag][@digest] into its parts; a digest wins over a tag.
func splitRef(ref string) (host, repo, reference string, err error) {
	host, rest, ok := strings.Cut(ref, "/")
	if !ok || rest == "" {
		return "", "", "", fmt.Errorf("%q names no registry", ref)
	}
	name, digest, hasDigest := strings.Cut(rest, "@")
	tag := "latest"
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	if hasDigest {
		return host, name, digest, nil
	}
	return host, name, tag, nil
}
