// Package updates checks GitHub for a newer Studio release, at most once a day. By default it
// only tells the user; "Install automatically" also downloads the release, verifies it and
// swaps it in when Studio is idle (install.go).
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

// The update modes.
const (
	ModeOff     = "off"     // no checks
	ModeTell    = "tell"    // check daily and show a newer release; the default
	ModeInstall = "install" // also install it when Studio is idle
)

// Settings persists the checker's state.
type Settings interface {
	Setting(ctx context.Context, key string) ([]byte, error)
	SetSetting(ctx context.Context, key string, value []byte) error
}

// State is what the UI shows.
type State struct {
	Mode      string           `json:"mode" enum:"off,tell,install"`
	Enabled   bool             `json:"enabled" doc:"Mode is not off"`
	Current   string           `json:"current" doc:"The running Studio version"`
	Latest    string           `json:"latest,omitempty" doc:"The newest release's tag, from the last check"`
	URL       string           `json:"url,omitempty" doc:"The newest release's page"`
	Available bool             `json:"available" doc:"Latest is newer than Current"`
	CheckedAt *time.Time       `json:"checkedAt,omitempty"`
	Error     string           `json:"error,omitempty" doc:"Why the last check failed"`
	Install   *InstallProgress `json:"install,omitempty" doc:"The automatic install of a release: under way, or how the last one ended"`
	// CanInstall is false where Studio can't replace its own binary, like a development build.
	CanInstall bool `json:"canInstall" doc:"This Studio can install releases itself"`
}

// saved is the persisted part of State. Mode is empty in a row written before the modes, where
// Enabled says whether the user turned the check on.
type saved struct {
	Mode      string     `json:"mode,omitempty"`
	Enabled   bool       `json:"enabled"`
	Latest    string     `json:"latest,omitempty"`
	URL       string     `json:"url,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
	Assets    []Asset    `json:"assets,omitempty"`
}

// mode is the saved mode. Without a row it is "tell"; a row from before the modes is "tell" if
// the user turned the check on and "off" if they turned it off.
func (s saved) mode(exists bool) string {
	switch {
	case s.Mode != "":
		return s.Mode
	case !exists || s.Enabled:
		return ModeTell
	}
	return ModeOff
}

// Checker checks for updates.
type Checker struct {
	Settings Settings
	Client   *http.Client
	URL      string // LatestURL when empty
	Current  string // the running version
	Now      func() time.Time
	Log      *slog.Logger
	// Installer installs releases in the "install" mode; nil means this Studio can't, and the
	// mode then only tells.
	Installer *Installer

	mu sync.Mutex // serializes checks and settings writes
}

// State returns the current state without checking.
func (c *Checker) State(ctx context.Context) (State, error) {
	s, err := c.load(ctx)
	return c.view(s), err
}

// SetMode sets the mode. A mode that checks checks right away if the last check is more than
// a day old, and "install" starts installing a newer release it knows of.
func (c *Checker) SetMode(ctx context.Context, mode string) (State, error) {
	switch mode {
	case ModeOff, ModeTell, ModeInstall:
	default:
		return State{}, fmt.Errorf("unknown update mode %q", mode)
	}
	if mode == ModeInstall && c.Installer == nil {
		return State{}, ErrCannotInstall
	}
	c.mu.Lock()
	s, err := c.load(ctx)
	if err == nil {
		s.Mode, s.Enabled = mode, mode != ModeOff
		err = c.save(ctx, s)
	}
	c.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	if mode == ModeOff {
		if c.Installer != nil {
			c.Installer.Cancel()
		}
		return c.view(s), nil
	}
	return c.CheckIfDue(ctx)
}

// ErrCannotInstall is the "install" mode on a Studio that can't replace its own binary.
var ErrCannotInstall = errors.New("this Studio can't install releases itself; install them with install.sh or from the release page")

// CheckIfDue checks GitHub if the mode checks and the last check is a day old. In the
// "install" mode it then starts installing a newer release, unless an install is under way.
func (c *Checker) CheckIfDue(ctx context.Context) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.load(ctx)
	// A development build never sees an update (Newer), so it doesn't ask.
	if err != nil || s.Mode == ModeOff || !releaseBuild(c.Current) {
		return c.view(s), err
	}
	now := c.now()
	if s.CheckedAt == nil || now.Sub(*s.CheckedAt) >= Interval || !now.After(*s.CheckedAt) {
		rel, err := c.fetch(ctx)
		s.CheckedAt = &now
		if err != nil {
			s.Error = err.Error()
		} else {
			s.Latest, s.URL, s.Assets, s.Error = rel.Tag, rel.URL, rel.Assets, ""
		}
		if err := c.save(ctx, s); err != nil {
			return c.view(s), err
		}
		if v := c.view(s); v.Available && c.Log != nil {
			c.Log.Info("a Studio update is available", "current", c.Current, "latest", rel.Tag, "url", rel.URL)
		}
	}
	v := c.view(s)
	if s.Mode == ModeInstall && v.Available && c.Installer != nil {
		c.Installer.Start(Release{Tag: s.Latest, URL: s.URL, Assets: s.Assets})
		v = c.view(s)
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

// Release is a published release, from GitHub's API.
type Release struct {
	Tag    string
	URL    string
	Assets []Asset
}

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

func (c *Checker) fetch(ctx context.Context) (Release, error) {
	u := c.URL
	if u == "" {
		u = LatestURL
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sandbox-studio/"+c.Current)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, errors.New("no release has been published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("read the release: %w", err)
	}
	if _, ok := parse(body.TagName); !ok {
		return Release{}, fmt.Errorf("the latest release has an unexpected tag %q", body.TagName)
	}
	// Only GitHub release pages are linked from the UI.
	if !strings.HasPrefix(body.HTMLURL, "https://github.com/") {
		body.HTMLURL = ""
	}
	rel := Release{Tag: body.TagName, URL: body.HTMLURL}
	for _, a := range body.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel, nil
}

func (c *Checker) view(s saved) State {
	st := State{Mode: s.Mode, Enabled: s.Mode != ModeOff, Current: c.Current, Latest: s.Latest, URL: s.URL, CheckedAt: s.CheckedAt,
		Error: s.Error, CanInstall: c.Installer != nil}
	st.Available = st.Enabled && Newer(s.Latest, c.Current)
	if c.Installer != nil {
		st.Install = c.Installer.Progress()
	}
	return st
}

// load reads the saved state, with Mode filled in.
func (c *Checker) load(ctx context.Context) (saved, error) {
	var s saved
	data, err := c.Settings.Setting(ctx, settingKey)
	if errors.Is(err, store.ErrNotFound) {
		s.Mode = s.mode(false)
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	s.Mode = s.mode(true)
	return s, err
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

// releaseBuild reports whether a running version is a release (or pre-release) build: `git
// describe` builds (v1.2.3-4-gabcdef, -dirty) are development builds, and so is anything that
// doesn't parse.
func releaseBuild(current string) bool {
	_, ok := parse(current)
	return ok && !strings.Contains(current, "-g") && !strings.HasSuffix(current, "-dirty")
}

// Newer reports whether release tag latest is newer than current. Development builds and
// other unparsable versions never see an update.
func Newer(latest, current string) bool {
	if !releaseBuild(current) {
		return false
	}
	l, ok1 := parse(latest)
	c, _ := parse(current)
	if !ok1 {
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
