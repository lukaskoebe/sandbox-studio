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
