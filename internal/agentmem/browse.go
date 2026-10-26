package agentmem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The browsable memory (docs/memory.md, Browsable memory): a read-only markdown copy of the
// scopes a teammate reads, in ~/memory of its sandboxes. It is a view, never a source: Studio
// writes it when a sandbox connects, at session start and whenever memory changes. The copy
// is a folder, one file per page with relative links between them, which the guest swaps in
// whole (agentproto.HomeDir). A guest of another version gets the two files of before,
// shared.md and yours.md.

// BrowseDir is the directory in the guest home that holds the copies.
const BrowseDir = "memory"

const (
	// browseBytes caps one file of the two-file copy, under the guest's limit for a home file.
	browseBytes = 200 << 10
	// pageBytes caps one page of the folder, and folderBytes all of it.
	pageBytes   = 64 << 10
	folderBytes = agentproto.MaxHomeDirBytes - 64<<10
	browseTick  = 15 * time.Second
)

// Guest is the part of the sandbox runtime that browsing needs: whether a sandbox's agent is
// connected, and writing files into its home (agentchan.Hub).
type Guest interface {
	Connected(id string) (agentproto.Hello, bool)
	WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error
	WriteHomeDir(ctx context.Context, id string, dir agentproto.HomeDir) error
}

// browser renders scopes once per environment and sync, however many sandboxes need them.
type browser struct {
	s      *Service
	env    string
	scopes map[string]string
	pages  map[string][]memory.Page
	facts  map[string][]memory.Fact
}

func newBrowser(s *Service, env string) *browser {
	return &browser{s: s, env: env, scopes: map[string]string{}, pages: map[string][]memory.Page{}, facts: map[string][]memory.Fact{}}
}

func (b *browser) scope(ctx context.Context, scope string) (string, error) {
	if text, ok := b.scopes[scope]; ok {
		return text, nil
	}
	text, err := b.s.renderScope(ctx, b.env, scope)
	if err != nil {
		return "", err
	}
	b.scopes[scope] = text
	return text, nil
}

// files are the copies a teammate's sandbox gets: shared memory, and the teammate's own.
func (b *browser) files(ctx context.Context, teammate string) ([]agentproto.HomeFile, error) {
	shared, err := b.scope(ctx, memory.SharedScope)
	if err != nil {
		return nil, err
	}
	own, err := b.scope(ctx, memory.TeammateScope(teammate))
	if err != nil {
		return nil, err
	}
	// Mode 0444: the copies are read-only for the sandbox's user too.
	return []agentproto.HomeFile{
		{Path: BrowseDir + "/shared.md", Content: shared, Mode: 0o444},
		{Path: BrowseDir + "/yours.md", Content: own, Mode: 0o444},
	}, nil
}

// BrowseFiles renders the copies of memory for a teammate's sandbox.
func (s *Service) BrowseFiles(ctx context.Context, env, teammate string) ([]agentproto.HomeFile, error) {
	return newBrowser(s, env).files(ctx, teammate)
}

// renderScope is the markdown of one scope: its pages with their compiled truth (core pages
// first), then the facts no page holds. Facts that hold in a context only are left out, as
// superseded, retracted and secret-like text are; the copies surface nothing a query would
// not.
func (s *Service) renderScope(ctx context.Context, env, scope string) (string, error) {
	pages, err := s.Memory.Pages(ctx, env, scope)
	if err != nil {
		return "", err
	}
	facts, err := s.Memory.Facts(ctx, env, memory.FactFilter{Scope: scope})
	if err != nil {
		return "", err
	}
	title := "Your memory"
	if scope == memory.SharedScope {
		title = "Shared memory"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nRead-only copies from Sandbox Studio, rewritten when the memory changes. Use memory_search for more.\n\n", title)
	if len(pages) == 0 && len(facts) == 0 {
		b.WriteString("Nothing here yet.\n")
	}
	onPage := map[string]bool{}
	for _, p := range pages {
		onPage[p.Slug] = true
	}
	if len(pages) > 0 {
		b.WriteString("## Pages\n\n")
		for _, p := range pages {
			core := ""
			if p.AlwaysLoad {
				core = ", core"
			}
			fmt.Fprintf(&b, "### %s (%s%s)\n\n%s\n\n", p.Title, p.Slug, core, strings.TrimSpace(p.Compiled))
		}
	}
	var loose strings.Builder
	for _, f := range facts {
		if f.Context != "" || (f.Status != memory.StatusActive && f.Status != memory.StatusDisputed) {
			continue
		}
		if slug := memory.FactPageSlug(f); slug != "" && onPage[slug] {
			continue
		}
		disputed := ""
		if f.Status == memory.StatusDisputed {
			disputed = ", disputed"
		}
		fmt.Fprintf(&loose, "- %s (%s, %s%s)\n", strings.Join(strings.Fields(f.Text), " "), f.Tier, f.ObservedAt.Format(time.DateOnly), disputed)
	}
	if loose.Len() > 0 {
		b.WriteString("## Facts\n\n" + loose.String() + "\n")
	}
	text := redact(b.String())
	if len(text) > browseBytes {
		text = truncateBytes(text, browseBytes) + "\n\n… Studio cut this copy at 200 KiB. Use memory_search for the rest.\n"
	}
	return text, nil
}

// redact replaces anything that looks like a credential; memory is secret-free already, so this
// only guards the copies.
func redact(text string) string {
	return secretLike.ReplaceAllString(text, "[redacted]")
}

// truncateBytes cuts text to at most n bytes, on a rune boundary.
func truncateBytes(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// RunBrowse keeps the copies current in every connected sandbox of a teammate until ctx ends.
// A sandbox gets them again when they change; nothing is written to one that is not connected.
func (s *Service) RunBrowse(ctx context.Context, g Guest) {
	t := time.NewTicker(browseTick)
	defer t.Stop()
	written := map[string]string{} // sandbox ID -> digest of the copies it has
	for {
		s.syncBrowse(ctx, g, written)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.browseNow():
		}
	}
}

// BrowseSoon has RunBrowse bring the copies up to date now rather than at its next tick, as
// when a session starts.
func (s *Service) BrowseSoon() {
	select {
	case s.browseNow() <- struct{}{}:
	default:
	}
}

func (s *Service) browseNow() chan struct{} {
	s.browseOnce.Do(func() { s.browseWake = make(chan struct{}, 1) })
	return s.browseWake
}

func (s *Service) syncBrowse(ctx context.Context, g Guest, written map[string]string) {
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		s.Log.Warn("listing environments for the memory copies failed", "err", err)
		return
	}
	for _, e := range envs {
		sandboxes, err := s.Store.Sandboxes(ctx, e.ID)
		if err != nil {
			s.Log.Warn("listing sandboxes for the memory copies failed", "env", e.ID, "err", err)
			continue
		}
		b := newBrowser(s, e.ID)
		for _, sb := range sandboxes {
			if sb.TeammateID == "" || sb.Kind == store.SandboxKindBrowser {
				continue
			}
			if _, ok := g.Connected(sb.ID); !ok {
				delete(written, sb.ID)
				continue
			}
			dir, err := b.folder(ctx, sb.TeammateID)
			if err != nil {
				s.Log.Warn("rendering the memory copies failed", "env", e.ID, "err", err)
				continue
			}
			sum := digest(dir.Files)
			if written[sb.ID] == sum {
				continue
			}
			err = g.WriteHomeDir(ctx, sb.ID, dir)
			if errors.Is(err, agentproto.ErrGuestOutdated) {
				var files []agentproto.HomeFile
				if files, err = b.files(ctx, sb.TeammateID); err == nil {
					err = g.WriteHomeFiles(ctx, sb.ID, files)
				}
			}
			if err != nil {
				s.Log.Info("writing the memory copies failed", "sandbox", sb.ID, "err", err)
				continue
			}
			written[sb.ID] = sum
		}
	}
}

func digest(files []agentproto.HomeFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%o\x00%s\x00", f.Path, f.Mode, f.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}
