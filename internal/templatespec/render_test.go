package templatespec

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
)

func TestYAMLRoundTripsToTheSameCanonicalSpec(t *testing.T) {
	res := resources.Resources{CPUs: 3, MemoryMiB: 1536, MaxMemoryMiB: 4096, WorkspaceMiB: 2048, DockerMiB: 1024}
	for name, spec := range map[string]Spec{
		"minimal":          {Resources: resources.Defaults(), Tools: []ToolPin{}, Apt: []string{}},
		"full":             {Resources: res, Tools: []ToolPin{{Name: "go", Version: "1.22"}, {Name: "node", Version: "22"}}, Apt: []string{"git", "123", "true", "null"}, Setup: "set -e\necho hi\n"},
		"awkward setup":    {Resources: res, Tools: []ToolPin{}, Apt: []string{}, Setup: "  leading\ntrailing  \n\n\tTab: # not a comment\r\n"},
		"no final newline": {Resources: res, Tools: []ToolPin{}, Apt: []string{}, Setup: "echo 'x' \"y\""},
		"yaml lookalike":   {Resources: res, Tools: []ToolPin{}, Apt: []string{}, Setup: "- a\n- b: c\n"},
	} {
		t.Run(name, func(t *testing.T) {
			want, err := CanonicalJSON(spec)
			if err != nil {
				t.Fatal(err)
			}
			out, err := YAML(spec)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseYAML(out)
			if err != nil {
				t.Fatalf("ParseYAML(%q): %v", out, err)
			}
			got, err := CanonicalJSON(parsed)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("canonical = %s, want %s (%v)\n%s", got, want, err, out)
			}
		})
	}
}

func TestYAMLRejectsInvalidAndOversizeSpecs(t *testing.T) {
	if _, err := YAML(Spec{Resources: resources.Resources{CPUs: 0}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid resources: err = %v", err)
	}
	big := Spec{Resources: resources.Defaults(), Tools: []ToolPin{}, Apt: []string{}, Setup: strings.Repeat("\x01", 60<<10)}
	if _, err := YAML(big); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize: err = %v", err)
	}
}
