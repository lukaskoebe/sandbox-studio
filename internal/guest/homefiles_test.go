//go:build linux

package guest

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func writeAsMe(home string, files ...agentproto.HomeFile) error {
	return writeHomeFiles(home, os.Getuid(), os.Getgid(), files)
}

func block(body string) string {
	return agentproto.BlockBegin + "\n" + body + "\n" + agentproto.BlockEnd + "\n"
}

func TestHomeFilesWrite(t *testing.T) {
	home := t.TempDir()
	must(t, writeAsMe(home,
		agentproto.HomeFile{Path: ".config/opencode/opencode.json", Content: "{}\n", Mode: 0o644},
		agentproto.HomeFile{Path: ".codex/config.toml", Content: "model = \"x\"\n", Mode: 0o600},
	))
	b, err := os.ReadFile(filepath.Join(home, ".config/opencode/opencode.json"))
	if err != nil || string(b) != "{}\n" {
		t.Fatalf("written: %q %v", b, err)
	}
	info, err := os.Stat(filepath.Join(home, ".codex/config.toml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(home, ".config")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
		t.Fatalf("created directory: %v %v", info, err)
	}

	// Rewriting replaces the file and leaves no temporary files behind.
	must(t, writeAsMe(home, agentproto.HomeFile{Path: ".codex/config.toml", Content: "model = \"y\"\n", Mode: 0o644}))
	entries, _ := os.ReadDir(filepath.Join(home, ".codex"))
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml")); string(b) != "model = \"y\"\n" {
		t.Fatalf("rewritten: %q", b)
	}
}

func TestHomeFilesRefused(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(home, ".claude")))
	must(t, os.WriteFile(filepath.Join(outside, "target"), []byte("keep"), 0o644))
	must(t, os.Symlink(filepath.Join(outside, "target"), filepath.Join(home, "AGENTS.md")))
	must(t, os.Mkdir(filepath.Join(home, ".codex"), 0o755))
	must(t, os.Symlink(outside, filepath.Join(home, ".codex", "sub")))

	for _, tc := range []struct {
		name string
		file agentproto.HomeFile
	}{
		{"absolute", agentproto.HomeFile{Path: "/etc/passwd", Mode: 0o644}},
		{"dotdot", agentproto.HomeFile{Path: "../x", Mode: 0o644}},
		{"inner dotdot", agentproto.HomeFile{Path: ".codex/../../x", Mode: 0o644}},
		{"empty", agentproto.HomeFile{Path: "", Mode: 0o644}},
		{"trailing slash", agentproto.HomeFile{Path: "x/", Mode: 0o644}},
		{"symlinked dir", agentproto.HomeFile{Path: ".claude/settings.json", Content: "{}", Mode: 0o644}},
		{"nested symlinked dir", agentproto.HomeFile{Path: ".codex/sub/x", Content: "{}", Mode: 0o644}},
		{"symlinked block file", agentproto.HomeFile{Path: "AGENTS.md", Content: block("x"), Mode: 0o644, Block: true}},
		{"setuid", agentproto.HomeFile{Path: "x", Mode: 0o4755}},
		{"too large", agentproto.HomeFile{Path: "x", Content: strings.Repeat("x", agentproto.MaxHomeFileBytes+1), Mode: 0o644}},
		{"block without markers", agentproto.HomeFile{Path: "x.md", Content: "x", Mode: 0o644, Block: true}},
		{"block with two begins", agentproto.HomeFile{Path: "x.md", Content: block(agentproto.BlockBegin), Mode: 0o644, Block: true}},
	} {
		if err := writeAsMe(home, tc.file); err == nil {
			t.Errorf("%s: written", tc.name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "target")); string(b) != "keep" {
		t.Fatalf("wrote through a symlink: %q", b)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Fatalf("wrote outside home: %v", entries)
	}

	// A symlinked home is refused too.
	link := filepath.Join(t.TempDir(), "home")
	must(t, os.Symlink(home, link))
	if err := writeAsMe(link, agentproto.HomeFile{Path: "x", Mode: 0o644}); err == nil {
		t.Fatal("followed a symlinked home")
	}

	many := make([]agentproto.HomeFile, agentproto.MaxHomeFiles+1)
	for i := range many {
		many[i] = agentproto.HomeFile{Path: "f", Mode: 0o644}
	}
	if err := writeAsMe(home, many...); err == nil {
		t.Fatal("wrote too many files")
	}
}

func TestHomeFilesHardlinkReplaced(t *testing.T) {
	home := t.TempDir()
	victim := filepath.Join(home, "victim")
	must(t, os.WriteFile(victim, []byte("keep"), 0o644))
	must(t, os.Link(victim, filepath.Join(home, "CLAUDE.md")))
	must(t, writeAsMe(home, agentproto.HomeFile{Path: "CLAUDE.md", Content: "new", Mode: 0o644}))
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("wrote through a hardlink: %q", b)
	}
}

func TestHomeFilesManagedBlock(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	must(t, writeAsMe(home, agentproto.HomeFile{Path: ".claude/CLAUDE.md", Content: block("one"), Mode: 0o644, Block: true}))
	if b, _ := os.ReadFile(path); string(b) != block("one") {
		t.Fatalf("fresh: %q", b)
	}
	// The user's notes around the block survive a rewrite.
	must(t, os.WriteFile(path, []byte("# mine\n\n"+block("one")+"\nmore of mine\n"), 0o644))
	must(t, writeAsMe(home, agentproto.HomeFile{Path: ".claude/CLAUDE.md", Content: block("two"), Mode: 0o644, Block: true}))
	if b, _ := os.ReadFile(path); string(b) != "# mine\n\n"+block("two")+"\nmore of mine\n" {
		t.Fatalf("merged: %q", b)
	}
	// A file without a block gets it in front.
	must(t, os.WriteFile(path, []byte("mine\n"), 0o644))
	must(t, writeAsMe(home, agentproto.HomeFile{Path: ".claude/CLAUDE.md", Content: block("three"), Mode: 0o644, Block: true}))
	if b, _ := os.ReadFile(path); string(b) != block("three")+"\nmine\n" {
		t.Fatalf("prepended: %q", b)
	}
	// A broken block is refused rather than guessed at.
	must(t, os.WriteFile(path, []byte(agentproto.BlockBegin+"\nunterminated\n"), 0o644))
	if err := writeAsMe(home, agentproto.HomeFile{Path: ".claude/CLAUDE.md", Content: block("four"), Mode: 0o644, Block: true}); err == nil {
		t.Fatal("merged into a broken block")
	}
	// An oversized existing file is refused.
	must(t, os.WriteFile(path, []byte(strings.Repeat("x", agentproto.MaxHomeFileBytes+1)), 0o644))
	if err := writeAsMe(home, agentproto.HomeFile{Path: ".claude/CLAUDE.md", Content: block("five"), Mode: 0o644, Block: true}); err == nil {
		t.Fatal("read an oversized file")
	}
}

func TestReadBodyBounded(t *testing.T) {
	var v agentproto.HomeFiles
	long := `{"files":[{"path":"` + strings.Repeat("x", 100) + `"}]}` + "\n"
	if err := readBody(bufio.NewReader(strings.NewReader(long)), 50, &v); err == nil {
		t.Fatal("read an oversized body")
	}
	if err := readBody(bufio.NewReader(strings.NewReader(long)), 200, &v); err != nil || len(v.Files) != 1 {
		t.Fatalf("read: %v %+v", err, v)
	}
}

func TestStartSessionCommand(t *testing.T) {
	req := agentproto.StartSession{
		Name: "claude", Harness: "claude", Command: []string{"claude"},
		Env: map[string]string{"ANTHROPIC_API_KEY": "studio-abc", "B": "1"},
	}
	if err := checkStartSession(req); err != nil {
		t.Fatal(err)
	}
	got := startCommand(req)
	want := []string{"-f", tmuxConfPath, "new-session", "-d", "-s", "claude", "-c", Workdir,
		"bash", "-lc", `exec env "$@"`, "studio-session", "ANTHROPIC_API_KEY=studio-abc", "B=1", "claude"}
	if !slices.Equal(got, want) {
		t.Fatalf("command:\n got %q\nwant %q", got, want)
	}
	for _, bad := range []agentproto.StartSession{
		{Name: "a b", Harness: "claude", Command: []string{"claude"}},
		{Name: "a", Harness: "Claude!", Command: []string{"claude"}},
		{Name: "a", Harness: "claude"},
		{Name: "a", Harness: "claude", Command: []string{"X=1"}},
		{Name: "a", Harness: "claude", Command: []string{"-i"}},
		{Name: "a", Harness: "claude", Command: []string{"claude"}, Env: map[string]string{"A": "$(id)"}},
		{Name: "a", Harness: "claude", Command: []string{"claude"}, Env: map[string]string{"a-b": "1"}},
	} {
		if checkStartSession(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
