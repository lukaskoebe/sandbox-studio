package templatespec

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseReportsProblemPositions(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Problem
	}{
		{
			name:  "unknown top-level key",
			input: "apt: []\nbuild: true\n",
			want:  []Problem{{Line: 2, Column: 1, Message: `unknown top-level key "build"`}},
		},
		{
			name:  "resource value",
			input: "resources:\n  cpus: 2\n  memory: 8GB\n",
			want:  []Problem{{Line: 3, Column: 11, Message: "resource size must use MiB, GiB, or G"}},
		},
		{
			name:  "range problem at the named value",
			input: "resources:\n  memory: 8G\n  max_memory: 4G\n",
			want: []Problem{{Line: 3, Column: 15,
				Message: "resources: invalid resources: maxMemoryMiB must be at least memoryMiB"}},
		},
		{
			name:  "range problem on a default value",
			input: "resources:\n  max_memory: 1G\n",
			want: []Problem{{Line: 2, Column: 15,
				Message: "resources: invalid resources: maxMemoryMiB must be at least memoryMiB"}},
		},
		{
			name:  "empty value",
			input: "apt: []\nresources:\n",
			want:  []Problem{{Line: 2, Column: 1, Message: "resources must be a mapping"}},
		},
		{
			name:  "block collection of the wrong kind",
			input: "tools:\n  - node\n",
			want:  []Problem{{Line: 1, Column: 1, Message: "tools must be a mapping"}},
		},
		{
			name:  "unquoted tool version and unsupported tool",
			input: "tools:\n  node: 22\n  ruby: \"3.3\"\n",
			want: []Problem{
				{Line: 2, Column: 9, Message: "tool versions must be quoted numeric dotted strings of at most 64 bytes"},
				{Line: 3, Column: 3, Message: `unsupported tool "ruby"`},
			},
		},
		{
			name:  "apt items",
			input: "apt:\n  - git\n  - Bad\n  - git\n",
			want: []Problem{
				{Line: 3, Column: 5, Message: "apt entries must be package names matching [a-z0-9][a-z0-9+.-]*"},
				{Line: 4, Column: 5, Message: `duplicate apt package "git"`},
			},
		},
		{
			name:  "problems across keys in source order",
			input: "setup: 42\nresources: {cpus: 0x2}\nextra: 1\n",
			want: []Problem{
				{Line: 1, Column: 8, Message: "setup must be a string scalar"},
				{Line: 2, Column: 19, Message: "cpus must be a plain decimal integer"},
				{Line: 3, Column: 1, Message: `unknown top-level key "extra"`},
			},
		},
		{
			name:  "duplicate key and anchor",
			input: "apt: []\nsetup: &s echo\napt: []\n",
			want: []Problem{
				{Line: 2, Column: 8, Message: "YAML anchors and aliases are not supported"},
				{Line: 3, Column: 1, Message: `duplicate YAML key "apt"`},
			},
		},
		{
			name:  "merge key reported once",
			input: "resources:\n  <<: {cpus: 2}\n",
			want:  []Problem{{Line: 2, Column: 3, Message: "YAML merge keys are not supported"}},
		},
		{
			name:  "multiple documents",
			input: "{}\n---\n{}\n",
			want:  []Problem{{Line: 2, Column: 1, Message: "exactly one YAML document is allowed"}},
		},
		{
			// go-yaml reports the line of the enclosing block, without a column.
			name:  "syntax error has a line",
			input: "resources:\n  cpus: 2\n memory: 4G\n",
			want:  []Problem{{Line: 2, Message: "parse YAML: did not find expected key"}},
		},
		{
			name:  "whole input",
			input: "\xff",
			want:  []Problem{{Message: "input must be valid UTF-8"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseYAML([]byte(tc.input))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseYAML() error = %v, want ErrInvalid", err)
			}
			var specErr *Error
			if !errors.As(err, &specErr) {
				t.Fatalf("ParseYAML() error %T is not *Error", err)
			}
			if got := Problems(err); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Problems() =\n %+v\nwant\n %+v", got, tc.want)
			}
		})
	}
}

func TestErrorMessageNamesPositions(t *testing.T) {
	_, err := ParseYAML([]byte("tools:\n  node: 22\napt: [Bad]\n"))
	want := "invalid template spec: line 2, column 9: tool versions must be quoted numeric dotted strings of at most 64 bytes; " +
		"line 3, column 7: apt entries must be package names matching [a-z0-9][a-z0-9+.-]*"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
	if got := Problems(errors.New("other")); got != nil {
		t.Fatalf("Problems(non-spec error) = %+v, want nil", got)
	}
	if got := Problems(invalid("plain")); len(got) != 1 || got[0] != (Problem{Message: "plain"}) {
		t.Fatalf("Problems(plain ErrInvalid) = %+v", got)
	}
}

func TestProblemCountIsBounded(t *testing.T) {
	_, err := ParseYAML([]byte("apt:\n" + strings.Repeat("  - X\n", 200)))
	if got := len(Problems(err)); got != maxProblems {
		t.Fatalf("problems = %d, want %d", got, maxProblems)
	}
}

// The web UI prefills new builds with this example; it must stay valid.
func TestWebExampleTemplateParses(t *testing.T) {
	source, err := os.ReadFile("../../web/src/lib/example-template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseYAML(source)
	if err != nil {
		t.Fatalf("ParseYAML(example) error = %v", err)
	}
	if len(spec.Tools) != 2 || len(spec.Apt) != 1 || spec.Setup == "" {
		t.Fatalf("example spec = %+v", spec)
	}
	if r := spec.Resources; r.CPUs != 2 || r.MemoryMiB != 4096 || r.WorkspaceMiB != 20480 || r.DockerMiB != 20480 {
		t.Fatalf("example resources = %+v", r)
	}
}

// The editor offers the Studio dev example (docs/template-builds.md); it must stay valid, with
// the Go and Node versions Studio's own build needs (CONTRIBUTING.md).
func TestStudioDevTemplateParses(t *testing.T) {
	source, err := os.ReadFile("../../web/src/lib/studio-dev-template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseYAML(source)
	if err != nil {
		t.Fatalf("ParseYAML(studio dev) error = %v", err)
	}
	tools := map[string]string{}
	for _, tool := range spec.Tools {
		tools[tool.Name] = tool.Version
	}
	if tools["go"] != "1.26" || tools["node"] != "22" || len(spec.Apt) != 1 || spec.Apt[0] != "chromium" {
		t.Fatalf("studio dev spec = %+v", spec)
	}
}
