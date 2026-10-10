package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The action log keeps what agents and the user did in a persona's browser, with a JPEG
// of the page after each action that changed it. It is bounded twice: Keep entries per
// persona and ShotBudget bytes of screenshots in all, the oldest going first. It never
// holds a credential: details are scrubbed, a credential fill is logged by name, and no
// screenshot is taken while a filled credential may be visible.

// entry is one action to log.
type entry struct {
	actor   string // store actor: agent or user
	sandbox string // the driving agent sandbox
	action  string
	target  string
	url     string
	outcome string
	detail  string
	shot    []byte
}

var shotName = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// record appends to the action log, then prunes it.
func (s *Service) record(ctx context.Context, st *state, e entry) {
	ctx = context.WithoutCancel(ctx)
	s.mu.Lock()
	session, secrets, visible := st.session, st.secrets, st.visible
	s.mu.Unlock()
	if session == "" {
		session = "-"
	}
	a := store.BrowserAction{
		EnvironmentID: st.env, PersonaID: st.persona, SessionID: session, SandboxID: e.sandbox,
		Actor: e.actor, Action: e.action, Target: clip(Scrub(e.target, secrets), 300), URL: clip(Scrub(e.url, secrets), 2000),
		Outcome: e.outcome, Detail: clip(Scrub(e.detail, secrets), 500), At: s.now(),
	}
	if len(e.shot) > 0 && !visible && s.Dir != "" {
		if name, err := s.saveShot(e.shot); err == nil {
			a.Screenshot = name
		} else {
			s.log().Warn("saving a browser screenshot failed", "err", err)
		}
	}
	if _, err := s.Store.AddBrowserAction(ctx, a); err != nil {
		s.log().Warn("logging a browser action failed", "action", e.action, "err", err)
		s.removeShot(a.Screenshot)
		return
	}
	s.prune(ctx, st.env, st.persona)
}

func (s *Service) saveShot(data []byte) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	var b [16]byte
	rand.Read(b[:])
	name := hex.EncodeToString(b[:]) + ".jpg"
	return name, os.WriteFile(filepath.Join(s.Dir, name), data, 0o600)
}

func (s *Service) removeShot(name string) {
	if shotName.MatchString(name) {
		os.Remove(filepath.Join(s.Dir, name))
	}
}

// ShotPath returns the file of an action's screenshot.
func (s *Service) ShotPath(ctx context.Context, envID, personaID string, id int64) (string, error) {
	a, err := s.Store.BrowserAction(ctx, envID, personaID, id)
	if err != nil {
		return "", err
	}
	if !shotName.MatchString(a.Screenshot) {
		return "", store.ErrNotFound
	}
	return filepath.Join(s.Dir, a.Screenshot), nil
}

// prune keeps the newest Keep actions of a persona and ShotBudget bytes of screenshots.
func (s *Service) prune(ctx context.Context, envID, personaID string) {
	dropped, err := s.Store.PruneBrowserActions(ctx, envID, personaID, or(s.Keep, defaultKeep))
	if err != nil {
		s.log().Warn("pruning the browser action log failed", "err", err)
		return
	}
	for _, name := range dropped {
		s.removeShot(name)
	}
	s.enforceBudget(ctx)
}

// enforceBudget drops the oldest screenshots until the rest fit ShotBudget.
func (s *Service) enforceBudget(ctx context.Context) {
	shots, err := s.Store.BrowserScreenshots(ctx)
	if err != nil {
		return
	}
	sizes := make([]int64, len(shots))
	var total int64
	for i, a := range shots {
		if fi, err := os.Stat(filepath.Join(s.Dir, a.Screenshot)); err == nil {
			sizes[i] = fi.Size()
			total += sizes[i]
		}
	}
	budget := or(s.ShotBudget, defaultShotBudget)
	for i := 0; total > budget && i < len(shots); i++ {
		if s.Store.ClearBrowserScreenshot(ctx, shots[i].ID) == nil {
			s.removeShot(shots[i].Screenshot)
			total -= sizes[i]
		}
	}
}

// sweepScreenshots deletes screenshot files no action refers to, such as those of a log
// entry that failed to save.
func (s *Service) sweepScreenshots(ctx context.Context) {
	if s.Dir == "" {
		return
	}
	shots, err := s.Store.BrowserScreenshots(ctx)
	if err != nil {
		return
	}
	known := make([]string, len(shots))
	for i, a := range shots {
		known[i] = a.Screenshot
	}
	slices.Sort(known)
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if _, found := slices.BinarySearch(known, e.Name()); !found && shotName.MatchString(e.Name()) {
			s.removeShot(e.Name())
		}
	}
	s.enforceBudget(ctx)
}

// DeleteHistory drops a persona's action log and its screenshots.
func (s *Service) DeleteHistory(ctx context.Context, envID, personaID string) error {
	names, err := s.Store.DeleteBrowserActions(ctx, envID, personaID)
	for _, n := range names {
		s.removeShot(n)
	}
	return err
}

// Actions lists a persona's action log, newest first.
func (s *Service) Actions(ctx context.Context, envID, personaID, sessionID string, limit int) ([]store.BrowserAction, error) {
	if _, err := s.Store.Persona(ctx, envID, personaID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return s.Store.BrowserActions(ctx, envID, personaID, sessionID, limit)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func target(role, name string) string {
	switch {
	case role == "":
		return ""
	case name == "":
		return role
	}
	return role + ` "` + strings.ReplaceAll(name, `"`, `'`) + `"`
}
