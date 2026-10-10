// Package updates checks GitHub for a newer Studio release. It is off until the user turns
// it on, checks at most once a day, and only reports: it never downloads or installs.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// LatestURL is the GitHub API endpoint of the newest published (non-draft, non-prerelease)
// release.
const LatestURL = "https://api.github.com/repos/lukaskoebe/sandbox-studio/releases/latest"

// Interval is the minimum time between two checks.
const Interval = 24 * time.Hour

const settingKey = "update-check"

// Settings persists the checker's state.
type Settings interface {
	Setting(ctx context.Context, key string) ([]byte, error)
	SetSetting(ctx context.Context, key string, value []byte) error
}

// State is what the UI shows.
type State struct {
	Enabled   bool       `json:"enabled"`
	Current   string     `json:"current" doc:"The running Studio version"`
	Latest    string     `json:"latest,omitempty" doc:"The newest release's tag, from the last check"`
	URL       string     `json:"url,omitempty" doc:"The newest release's page"`
	Available bool       `json:"available" doc:"Latest is newer than Current"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty" doc:"Why the last check failed"`
}

// saved is the persisted part of State.
type saved struct {
	Enabled   bool       `json:"enabled"`
	Latest    string     `json:"latest,omitempty"`
	URL       string     `json:"url,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Checker checks for updates.
type Checker struct {
	Settings Settings
	Client   *http.Client
	URL      string // LatestURL when empty
	Current  string // the running version
	Now      func() time.Time
	Log      *slog.Logger

	mu sync.Mutex // serializes checks and settings writes
}

// State returns the current state without checking.
func (c *Checker) State(ctx context.Context) (State, error) {
	s, err := c.load(ctx)
	return c.view(s), err
}

// SetEnabled turns checking on or off. Turning it on checks right away if the last check
// is more than a day old.
func (c *Checker) SetEnabled(ctx context.Context, on bool) (State, error) {
	c.mu.Lock()
	s, err := c.load(ctx)
	if err == nil {
		s.Enabled = on
		err = c.save(ctx, s)
	}
	c.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	if on {
		return c.CheckIfDue(ctx)
	}
	return c.view(s), nil
}

// CheckIfDue checks GitHub if checking is enabled and the last check is a day old.
func (c *Checker) CheckIfDue(ctx context.Context) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.load(ctx)
	if err != nil || !s.Enabled {
		return c.view(s), err
	}
	now := c.now()
	if s.CheckedAt != nil && now.Sub(*s.CheckedAt) < Interval && now.After(*s.CheckedAt) {
		return c.view(s), nil
	}
	tag, url, err := c.fetch(ctx)
	s.CheckedAt = &now
	if err != nil {
		s.Error = err.Error()
	} else {
		s.Latest, s.URL, s.Error = tag, url, ""
	}
	if err := c.save(ctx, s); err != nil {
		return c.view(s), err
	}
	v := c.view(s)
	if v.Available && c.Log != nil {
		c.Log.Info("a Studio update is available", "current", c.Current, "latest", tag, "url", url)
	}
	return v, nil
}

// Run checks when due, now and then every hour, until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := c.CheckIfDue(ctx); err != nil && ctx.Err() == nil && c.Log != nil {
			c.Log.Warn("update check", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Checker) fetch(ctx context.Context) (tag, url string, err error) {
	u := c.URL
	if u == "" {
		u = LatestURL
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sandbox-studio/"+c.Current)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", errors.New("no release has been published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", fmt.Errorf("read the release: %w", err)
	}
	if _, ok := parse(body.TagName); !ok {
		return "", "", fmt.Errorf("the latest release has an unexpected tag %q", body.TagName)
	}
	// Only GitHub release pages are linked from the UI.
	if !strings.HasPrefix(body.HTMLURL, "https://github.com/") {
		body.HTMLURL = ""
	}
	return body.TagName, body.HTMLURL, nil
}

func (c *Checker) view(s saved) State {
	st := State{Enabled: s.Enabled, Current: c.Current, Latest: s.Latest, URL: s.URL, CheckedAt: s.CheckedAt, Error: s.Error}
	st.Available = s.Enabled && Newer(s.Latest, c.Current)
	return st
}

func (c *Checker) load(ctx context.Context) (saved, error) {
	var s saved
	data, err := c.Settings.Setting(ctx, settingKey)
	if errors.Is(err, store.ErrNotFound) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

func (c *Checker) save(ctx context.Context, s saved) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return c.Settings.SetSetting(ctx, settingKey, data)
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// version is a parsed vMAJOR.MINOR.PATCH[-PRERELEASE].
type version struct {
	n   [3]int
	pre string
}

func parse(s string) (version, bool) {
	var v version
	s = strings.TrimPrefix(s, "v")
	s, v.pre, _ = strings.Cut(s, "-")
	s, _, _ = strings.Cut(s, "+")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// Newer reports whether release tag latest is newer than current. Development builds and
// other unparsable versions never see an update.
func Newer(latest, current string) bool {
	// `git describe` builds (v1.2.3-4-gabcdef, -dirty) are development builds.
	if strings.Contains(current, "-g") || strings.HasSuffix(current, "-dirty") {
		return false
	}
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l.n {
		if l.n[i] != c.n[i] {
			return l.n[i] > c.n[i]
		}
	}
	switch {
	case l.pre == c.pre:
		return false
	case l.pre == "":
		return true // a release is newer than its prereleases
	case c.pre == "":
		return false
	}
	return l.pre > c.pre
}
