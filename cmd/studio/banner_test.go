package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBannerWanted(t *testing.T) {
	for _, tt := range []struct {
		terminal bool
		noColor  string
		want     bool
	}{
		{true, "", true},
		{true, "1", false},
		{true, "anything", false},
		{false, "", false},
		{false, "1", false},
	} {
		if got := bannerWanted(tt.terminal, tt.noColor); got != tt.want {
			t.Errorf("bannerWanted(%v, %q) = %v, want %v", tt.terminal, tt.noColor, got, tt.want)
		}
	}
}

func TestLoginBannerKeepsTheLinkOnOneFramedLine(t *testing.T) {
	url := loginURL("127.0.0.1:7878", strings.Repeat("t", 43))
	var b strings.Builder
	writeLoginBanner(&b, url)
	out := b.String()
	var framed []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "│") || strings.Contains(line, "╭") || strings.Contains(line, "╰") {
			framed = append(framed, line)
		}
	}
	if len(framed) < 3 {
		t.Fatalf("banner has no frame:\n%s", out)
	}
	width := -1
	found := false
	for _, line := range framed {
		plain := stripANSI(line)
		if n := utf8.RuneCountInString(plain); width == -1 {
			width = n
		} else if n != width {
			t.Errorf("frame line %q is %d wide, want %d", plain, n, width)
		}
		found = found || strings.Contains(plain, url)
	}
	if !found {
		t.Fatalf("banner does not contain the link unbroken:\n%s", out)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
