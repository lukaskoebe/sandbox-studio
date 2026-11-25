// Package npmrelease looks up the newest stable release of an npm package, which a sandbox's
// harness can update to (docs/harnesses.md). Studio asks only when the user opens an update,
// and remembers the answer for a while.
package npmrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Registry is npm's public registry.
const Registry = "https://registry.npmjs.org"

const (
	ttl     = 10 * time.Minute
	timeout = 15 * time.Second
)

// Finder finds packages' newest stable releases.
type Finder struct {
	Client   *http.Client // a client with a 15 s timeout when nil
	Registry string       // Registry when empty
	Now      func() time.Time

	mu    sync.Mutex
	known map[string]found
}

type found struct {
	version string
	at      time.Time
}

// Stable is pkg's newest stable release: its "stable" dist-tag where it has one, as Claude
// Code does, else its "latest".
func (f *Finder) Stable(ctx context.Context, pkg string) (string, error) {
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	f.mu.Lock()
	if v, ok := f.known[pkg]; ok && now().Sub(v.at) < ttl {
		f.mu.Unlock()
		return v.version, nil
	}
	f.mu.Unlock()

	v, err := f.fetch(ctx, pkg)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	if f.known == nil {
		f.known = map[string]found{}
	}
	f.known[pkg] = found{v, now()}
	f.mu.Unlock()
	return v, nil
}

func (f *Finder) fetch(ctx context.Context, pkg string) (string, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	registry := f.Registry
	if registry == "" {
		registry = Registry
	}
	// A scoped name is one path segment, its slash escaped, as npm asks for it.
	url := strings.TrimSuffix(registry, "/") + "/-/package/" + strings.ReplaceAll(pkg, "/", "%2f") + "/dist-tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sandbox-studio")
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("asking npm for %s's releases: %w", pkg, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asking npm for %s's releases: %s", pkg, res.Status)
	}
	var tags map[string]string
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&tags); err != nil {
		return "", fmt.Errorf("reading %s's releases: %w", pkg, err)
	}
	for _, tag := range []string{"stable", "latest"} {
		if v := tags[tag]; v != "" {
			if !agentproto.HarnessVersionPattern.MatchString(v) {
				return "", fmt.Errorf("npm's %s release of %s is %q, not a version", tag, pkg, v)
			}
			return v, nil
		}
	}
	return "", errors.New("npm names no stable release of " + pkg)
}
