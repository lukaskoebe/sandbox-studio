package templatespec

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
)

func TestParseCanonicalJSONRoundTrip(t *testing.T) {
	want := Spec{
		Resources: resources.Resources{
			CPUs: 4, MemoryMiB: 8192, MaxMemoryMiB: 16384,
			WorkspaceMiB: 40960, DockerMiB: 30720,
		},
		Tools: []ToolPin{{Name: "python", Version: "3.13"}, {Name: "go", Version: "1.25"}},
		Apt:   []string{"zlib1g", "ca-certificates"},
		Setup: "corepack enable\n",
	}
	canonical, err := CanonicalJSON(want)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}

	got, err := ParseCanonicalJSON(canonical)
	if err != nil {
		t.Fatalf("ParseCanonicalJSON() error = %v", err)
	}
	want.Tools = []ToolPin{{Name: "go", Version: "1.25"}, {Name: "python", Version: "3.13"}}
	want.Apt = []string{"ca-certificates", "zlib1g"}
	if got.Resources != want.Resources || got.Setup != want.Setup || !equalToolPins(got.Tools, want.Tools) || !equalStrings(got.Apt, want.Apt) {
		t.Fatalf("ParseCanonicalJSON() = %+v, want %+v", got, want)
	}
	encoded, err := CanonicalJSON(got)
	if err != nil {
		t.Fatalf("CanonicalJSON(parsed) error = %v", err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Fatalf("parsed spec did not preserve canonical bytes:\n got %s\nwant %s", encoded, canonical)
	}
}

func TestParseCanonicalJSONRejectsMalformedAndNoncanonicalInput(t *testing.T) {
	base, err := CanonicalJSON(Spec{Resources: resources.Defaults()})
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	valid := string(base)
	cases := []struct {
		name  string
		input []byte
	}{
		{name: "empty", input: nil},
		{name: "oversized", input: []byte(strings.Repeat(" ", maxCanonicalBytes+1))},
		{name: "malformed JSON", input: []byte(`{"formatVersion":`)},
		{name: "trailing JSON value", input: append(append([]byte(nil), base...), []byte(` {}`)...)},
		{name: "unknown top-level field", input: []byte(strings.Replace(valid, `"formatVersion":1,`, `"formatVersion":1,"extra":true,`, 1))},
		{name: "unknown resource field", input: []byte(strings.Replace(valid, `"cpus":2,`, `"cpus":2,"extra":1,`, 1))},
		{name: "unknown tool field", input: []byte(strings.Replace(valid, `"tools":[],`, `"tools":[{"name":"go","version":"1.25","extra":true}],`, 1))},
		{name: "unsupported version", input: []byte(strings.Replace(valid, `"formatVersion":1`, `"formatVersion":2`, 1))},
		{name: "duplicate field", input: []byte(strings.Replace(valid, `"formatVersion":1,`, `"formatVersion":1,"formatVersion":1,`, 1))},
		{name: "noncanonical whitespace", input: append([]byte(" "), base...)},
		{name: "invalid resources", input: []byte(strings.Replace(valid, `"memoryMiB":4096`, `"memoryMiB":0`, 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseCanonicalJSON(tc.input); err == nil {
				t.Fatal("ParseCanonicalJSON() error = nil, want ErrInvalid")
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseCanonicalJSON() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func equalToolPins(a, b []ToolPin) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
