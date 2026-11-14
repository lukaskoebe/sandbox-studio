package runtime

import (
	"slices"
	"strings"
	"testing"
)

func TestCopyLinesReadsProgressAcrossChunks(t *testing.T) {
	var got [][2]int64
	c := &copyLines{report: func(copied, total int64) { got = append(got, [2]int64{copied, total}) }}
	for _, chunk := range []string{"copied 1 ", "9\ncopied 2 9\nnoise\ncop", "ied 3 0\ncopied x y\ncopied 4"} {
		if n, err := c.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if want := [][2]int64{{1, 9}, {2, 9}, {3, 0}}; !slices.Equal(got, want) {
		t.Fatalf("reports = %v, want %v", got, want)
	}
	// Output that never ends a line isn't kept.
	if _, err := c.Write([]byte(strings.Repeat("x", 8192))); err != nil {
		t.Fatal(err)
	}
	if len(c.partial) != 0 {
		t.Fatalf("kept %d bytes", len(c.partial))
	}
}

func TestCopyLinesWithoutAReportReadsToo(t *testing.T) {
	c := &copyLines{}
	if _, err := c.Write([]byte("copied 1 2\n")); err != nil {
		t.Fatal(err)
	}
}

// A tools export is told what the target has in its environment, unless the list is too long
// for one; other trees ignore it.
func TestExportOptionsPassWhatTheTargetHas(t *testing.T) {
	have := []string{"installs/node/22.1.0", "plugins/foo"}
	args, env := ExportOptions{Fast: true, ToolsHave: have}.command(TreeTools)
	if !slices.Equal(args, []string{"tools-export", "--zstd"}) || env["STUDIO_TOOLS_HAVE"] != "installs/node/22.1.0\nplugins/foo" {
		t.Fatalf("args %q, env %q", args, env)
	}
	if args, env := (ExportOptions{ToolsHave: have}).command(TreeWorkspace); !slices.Equal(args, []string{"workspace-export"}) || env != nil {
		t.Fatalf("workspace: args %q, env %q", args, env)
	}
	long := []string{strings.Repeat("x", 64<<10+1)}
	if _, env := (ExportOptions{Fast: true, ToolsHave: long}).command(TreeTools); env != nil {
		t.Fatalf("a list too long was passed: %d bytes", len(env["STUDIO_TOOLS_HAVE"]))
	}
	var b limitedBuffer
	b.max = 8
	_, _ = b.Write([]byte("12345"))
	_, _ = b.Write([]byte("6789"))
	if !b.over || b.b.String() != "12345" {
		t.Fatalf("limitedBuffer kept %q, over %v", b.b.String(), b.over)
	}
}
