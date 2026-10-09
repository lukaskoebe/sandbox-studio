package templatespec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

func TestParsePlanExampleAndResolveResources(t *testing.T) {
	input := []byte("" +
		"resources: { cpus: 4, memory: 8G, max_memory: 16G, workspace: 40G, docker: 30G }\n" +
		"tools: { node: \"22\", python: \"3.13\", go: \"1.25\" }\n" +
		"apt: [postgresql-client]\n" +
		"setup: |\n" +
		"  corepack enable\n")
	spec, err := ParseYAML(input)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	wantResources := resources.Resources{
		CPUs: 4, MemoryMiB: 8192, MaxMemoryMiB: 16384,
		WorkspaceMiB: 40960, DockerMiB: 30720,
	}
	if spec.Resources != wantResources {
		t.Fatalf("resources = %+v, want %+v", spec.Resources, wantResources)
	}
	if spec.Setup != "corepack enable\n" {
		t.Fatalf("setup = %q, want decoded block bytes %q", spec.Setup, "corepack enable\n")
	}
	canonical, err := CanonicalJSON(spec)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	var decoded struct {
		FormatVersion int `json:"formatVersion"`
		Resources     struct {
			CPUs         int64 `json:"cpus"`
			MemoryMiB    int64 `json:"memoryMiB"`
			MaxMemoryMiB int64 `json:"maxMemoryMiB"`
			WorkspaceMiB int64 `json:"workspaceMiB"`
			DockerMiB    int64 `json:"dockerMiB"`
		} `json:"resources"`
		Tools []ToolPin `json:"tools"`
		Apt   []string  `json:"apt"`
		Setup string    `json:"setup"`
	}
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if decoded.FormatVersion != canonicalVersion || decoded.Resources.CPUs != 4 || decoded.Resources.MemoryMiB != 8192 || decoded.Resources.MaxMemoryMiB != 16384 || decoded.Resources.WorkspaceMiB != 40960 || decoded.Resources.DockerMiB != 30720 {
		t.Fatalf("canonical values = %+v", decoded)
	}
	if len(decoded.Tools) != 3 || decoded.Tools[0] != (ToolPin{Name: "go", Version: "1.25"}) || decoded.Tools[1] != (ToolPin{Name: "node", Version: "22"}) || decoded.Tools[2] != (ToolPin{Name: "python", Version: "3.13"}) {
		t.Fatalf("canonical tools = %+v", decoded.Tools)
	}
	if !reflect.DeepEqual(decoded.Apt, []string{"postgresql-client"}) || decoded.Setup != spec.Setup {
		t.Fatalf("canonical apt/setup = %q / %q", decoded.Apt, decoded.Setup)
	}
}

func TestCanonicalNormalizesEquivalentSpecsForCacheKey(t *testing.T) {
	first, err := ParseYAML([]byte("" +
		"resources:\n  cpus: 4\n  memory: 8G\n  workspace: 40G\n" +
		"tools:\n  node: \"22\"\n  python: \"3.13\"\n" +
		"apt: [postgresql-client, ca-certificates]\n" +
		"setup: |\n  corepack enable\n"))
	if err != nil {
		t.Fatalf("ParseYAML(first) error = %v", err)
	}
	second, err := ParseYAML([]byte("" +
		"setup: |\n  corepack enable\n" +
		"apt: [ca-certificates, postgresql-client]\n" +
		"tools:\n  python: \"3.13\"\n  node: \"22\"\n" +
		"resources:\n  workspace: 40960MiB\n  memory: 8192MiB\n  max_memory: 8GiB\n  cpus: 4\n"))
	if err != nil {
		t.Fatalf("ParseYAML(second) error = %v", err)
	}
	firstJSON, err := CanonicalJSON(first)
	if err != nil {
		t.Fatalf("CanonicalJSON(first) error = %v", err)
	}
	secondJSON, err := CanonicalJSON(second)
	if err != nil {
		t.Fatalf("CanonicalJSON(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("equivalent specs have different canonical JSON:\n%s\n%s", firstJSON, secondJSON)
	}
	baseDigest := templateimage.Digest([]byte("base"))
	firstKey, err := templateimage.CacheKey(firstJSON, baseDigest, "linux/amd64", templateimage.ExporterVersion)
	if err != nil {
		t.Fatalf("CacheKey(first) error = %v", err)
	}
	secondKey, err := templateimage.CacheKey(secondJSON, baseDigest, "linux/amd64", templateimage.ExporterVersion)
	if err != nil {
		t.Fatalf("CacheKey(second) error = %v", err)
	}
	if firstKey != secondKey {
		t.Fatalf("equivalent specs have different cache keys: %q and %q", firstKey, secondKey)
	}
}

func TestParseDefaultsAndMaxMemoryPresence(t *testing.T) {
	defaults, err := ParseYAML([]byte("{}"))
	if err != nil {
		t.Fatalf("ParseYAML({}) error = %v", err)
	}
	if defaults.Resources != resources.Defaults() {
		t.Fatalf("default resources = %+v, want %+v", defaults.Resources, resources.Defaults())
	}
	chosen, err := ParseYAML([]byte("resources: {memory: 8G}"))
	if err != nil {
		t.Fatalf("ParseYAML(memory only) error = %v", err)
	}
	if chosen.Resources.MemoryMiB != 8192 || chosen.Resources.MaxMemoryMiB != 8192 {
		t.Fatalf("omitted max_memory did not follow memory: %+v", chosen.Resources)
	}
	if _, err := ParseYAML([]byte("resources: {max_memory: 0MiB}")); err == nil {
		t.Fatal("explicit zero max_memory succeeded")
	} else if !errors.Is(err, ErrInvalid) {
		t.Fatalf("explicit zero error = %v, want ErrInvalid", err)
	}
}

func TestCanonicalPreservesSetupAndDoesNotMutate(t *testing.T) {
	spec := Spec{
		Resources: resources.Defaults(),
		Tools:     []ToolPin{{Name: "node", Version: "22"}, {Name: "go", Version: "1.25"}},
		Apt:       []string{"zlib1g", "ca-certificates"},
		Setup:     "printf '\x01line\n'\n",
	}
	before := Spec{
		Resources: spec.Resources,
		Tools:     append([]ToolPin(nil), spec.Tools...),
		Apt:       append([]string(nil), spec.Apt...),
		Setup:     spec.Setup,
	}
	canonical, err := CanonicalJSON(spec)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if !reflect.DeepEqual(spec, before) {
		t.Fatal("CanonicalJSON() mutated the caller's slices or fields")
	}
	var decoded struct {
		Setup string `json:"setup"`
	}
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if decoded.Setup != spec.Setup {
		t.Fatalf("canonical setup bytes = %q, want %q", decoded.Setup, spec.Setup)
	}
	changed := spec
	changed.Setup += "extra"
	other, err := CanonicalJSON(changed)
	if err != nil {
		t.Fatalf("CanonicalJSON(changed) error = %v", err)
	}
	if bytes.Equal(canonical, other) {
		t.Fatal("changing setup did not change canonical bytes")
	}
}

func TestParseRejectsMalformedOrUnsupportedYAML(t *testing.T) {
	tooManyNodes := "apt:\n" + strings.Repeat("  - aa\n", maxYAMLNodes)
	tooDeep := "unknown: " + strings.Repeat("[", maxYAMLDepth+2) + "x" + strings.Repeat("]", maxYAMLDepth+2)
	var tooManyPackages strings.Builder
	tooManyPackages.WriteString("apt:\n")
	for i := 0; i <= maxAptPackages; i++ {
		fmt.Fprintf(&tooManyPackages, "  - pkg%d\n", i)
	}
	cases := []struct {
		name  string
		input []byte
	}{
		{name: "empty input", input: nil},
		{name: "invalid UTF-8 input", input: []byte{0xff}},
		{name: "input limit", input: []byte(strings.Repeat(" ", maxInputBytes+1))},
		{name: "empty document", input: []byte("---\n")},
		{name: "multiple documents", input: []byte("{}\n---\n{}\n")},
		{name: "nonmapping root", input: []byte("[one, two]\n")},
		{name: "unknown key", input: []byte("build: true\n")},
		{name: "duplicate top level key", input: []byte("apt: []\napt: []\n")},
		{name: "duplicate nested key", input: []byte("resources: {cpus: 2, cpus: 3}\n")},
		{name: "unknown resource key", input: []byte("resources: {memory: 4G, extra: 1}\n")},
		{name: "wrong CPU scalar type", input: []byte("resources: {cpus: \"2\"}\n")},
		{name: "nondecimal CPU", input: []byte("resources: {cpus: 0x2}\n")},
		{name: "signed CPU", input: []byte("resources: {cpus: +2}\n")},
		{name: "zero CPU", input: []byte("resources: {cpus: 0}\n")},
		{name: "zero memory", input: []byte("resources: {memory: 0MiB}\n")},
		{name: "zero workspace", input: []byte("resources: {workspace: 0GiB}\n")},
		{name: "zero docker", input: []byte("resources: {docker: 0G}\n")},
		{name: "numeric resource size", input: []byte("resources: {memory: 4096}\n")},
		{name: "invalid resource unit", input: []byte("resources: {memory: 8GB}\n")},
		{name: "resource multiplication overflow", input: []byte("resources: {memory: 9007199254740992G}\n")},
		{name: "max memory below memory", input: []byte("resources: {memory: 8G, max_memory: 4G}\n")},
		{name: "numeric version needs quotes", input: []byte("tools: {node: 22}\n")},
		{name: "string version needs quotes", input: []byte("tools: {node: 22.1.2}\n")},
		{name: "unsupported tool", input: []byte("tools: {ruby: \"3.3\"}\n")},
		{name: "version placeholder", input: []byte("tools: {node: \"latest\"}\n")},
		{name: "apt not a sequence", input: []byte("apt: postgresql-client\n")},
		{name: "apt value not a string", input: []byte("apt: [123]\n")},
		{name: "invalid apt token", input: []byte("apt: [BadPackage]\n")},
		{name: "too short apt token", input: []byte("apt: [a]\n")},
		{name: "duplicate apt token", input: []byte("apt: [zlib1g, zlib1g]\n")},
		{name: "apt count limit", input: []byte(tooManyPackages.String())},
		{name: "setup not string", input: []byte("setup: 42\n")},
		{name: "custom tag", input: []byte("setup: !unsafe value\n")},
		{name: "anchor", input: []byte("setup: &script echo hi\n")},
		{name: "alias", input: []byte("setup: *script\n")},
		{name: "merge key", input: []byte("resources: {<<: {cpus: 2}}\n")},
		{name: "depth limit", input: []byte(tooDeep)},
		{name: "node limit", input: []byte(tooManyNodes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseYAML(tc.input)
			if err == nil {
				t.Fatal("ParseYAML() error = nil, want ErrInvalid")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseYAML() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCanonicalRejectsInvalidConstructedSpecs(t *testing.T) {
	valid := Spec{Resources: resources.Defaults()}
	cases := []struct {
		name   string
		mutate func(*Spec)
	}{
		{name: "unresolved resources", mutate: func(spec *Spec) { spec.Resources = resources.Resources{} }},
		{name: "resource overflow", mutate: func(spec *Spec) {
			spec.Resources.MemoryMiB = int64(^uint32(0)) + 1
			spec.Resources.MaxMemoryMiB = spec.Resources.MemoryMiB
		}},
		{name: "tool count limit", mutate: func(spec *Spec) {
			spec.Tools = []ToolPin{{Name: "node", Version: "22"}, {Name: "python", Version: "3.13"}, {Name: "go", Version: "1.25"}, {Name: "go", Version: "1.25"}}
		}},
		{name: "duplicate tool", mutate: func(spec *Spec) { spec.Tools = []ToolPin{{Name: "node", Version: "22"}, {Name: "node", Version: "23"}} }},
		{name: "unsupported tool", mutate: func(spec *Spec) { spec.Tools = []ToolPin{{Name: "ruby", Version: "3.3"}} }},
		{name: "invalid version", mutate: func(spec *Spec) { spec.Tools = []ToolPin{{Name: "go", Version: "latest"}} }},
		{name: "duplicate package", mutate: func(spec *Spec) { spec.Apt = []string{"zlib1g", "zlib1g"} }},
		{name: "package count limit", mutate: func(spec *Spec) {
			spec.Apt = make([]string, maxAptPackages+1)
			for i := range spec.Apt {
				spec.Apt[i] = fmt.Sprintf("pkg%d", i)
			}
		}},
		{name: "invalid package", mutate: func(spec *Spec) { spec.Apt = []string{"BadPackage"} }},
		{name: "oversized setup", mutate: func(spec *Spec) { spec.Setup = strings.Repeat("x", maxSetupBytes+1) }},
		{name: "invalid UTF-8 setup", mutate: func(spec *Spec) { spec.Setup = string([]byte{0xff}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := valid
			tc.mutate(&spec)
			if _, err := CanonicalJSON(spec); err == nil {
				t.Fatal("CanonicalJSON() error = nil, want ErrInvalid")
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatalf("CanonicalJSON() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSetupRejectsNULAsUnrepresentable(t *testing.T) {
	_, parseErr := ParseYAML([]byte("setup: \"before\\0after\"\n"))
	if !errors.Is(parseErr, ErrInvalid) || !strings.Contains(parseErr.Error(), "NUL") {
		t.Fatalf("ParseYAML() error = %v, want explicit NUL validation error", parseErr)
	}

	_, canonicalErr := CanonicalJSON(Spec{
		Resources: resources.Defaults(),
		Setup:     "before\x00after",
	})
	if !errors.Is(canonicalErr, ErrInvalid) || !strings.Contains(canonicalErr.Error(), "NUL") {
		t.Fatalf("CanonicalJSON() error = %v, want explicit NUL validation error", canonicalErr)
	}
}

func TestCanonicalOutputHasFixedFieldOrderAndVersion(t *testing.T) {
	canonical, err := CanonicalJSON(Spec{Resources: resources.Defaults()})
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	want := fmt.Sprintf(`{"formatVersion":%d,"resources":{"cpus":2,"memoryMiB":4096,"maxMemoryMiB":4096,"workspaceMiB":20480,"dockerMiB":20480},"tools":[],"apt":[],"setup":""}`, canonicalVersion)
	if string(canonical) != want {
		t.Fatalf("canonical JSON = %s, want %s", canonical, want)
	}
	empty, err := CanonicalJSON(Spec{
		Resources: resources.Defaults(),
		Tools:     []ToolPin{},
		Apt:       []string{},
	})
	if err != nil {
		t.Fatalf("CanonicalJSON(empty slices) error = %v", err)
	}
	if !bytes.Equal(canonical, empty) {
		t.Fatalf("nil and empty lists produced different canonical JSON: %s / %s", canonical, empty)
	}
}
