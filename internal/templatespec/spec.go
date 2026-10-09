// Package templatespec parses and canonicalizes Studio's deliberately small
// sandbox template specification.
package templatespec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"go.yaml.in/yaml/v3"
)

const (
	maxInputBytes        = 64 << 10
	maxYAMLDepth         = 16
	maxYAMLNodes         = 4096
	maxSetupBytes        = 64 << 10
	maxCanonicalBytes    = 1 << 20
	maxAptPackages       = 256
	maxToolPins          = 3
	canonicalVersion     = 1
	mebibytesPerGibibyte = 1024
)

var (
	// ErrInvalid marks malformed or unsupported template specs.
	ErrInvalid = errors.New("invalid template spec")

	decimalInteger = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	toolVersion    = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	packageName    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
)

// Spec is the supported, resolved template specification. Setup is user code;
// it is retained exactly as decoded and is intentionally not sanitized.
type Spec struct {
	Resources resources.Resources
	Tools     []ToolPin
	Apt       []string
	Setup     string
}

// ToolPin is one explicitly supported versioned runtime tool.
type ToolPin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ParseYAML parses one bounded YAML mapping, rejects unsupported YAML features,
// and resolves resource defaults before returning the spec.
func ParseYAML(data []byte) (Spec, error) {
	if len(data) == 0 || len(data) > maxInputBytes {
		return Spec{}, invalid("input must be between 1 byte and 64 KiB")
	}
	if !utf8.Valid(data) {
		return Spec{}, invalid("input must be valid UTF-8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Spec{}, invalid("parse YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Spec{}, invalid("exactly one YAML document is allowed")
		}
		return Spec{}, invalid("parse trailing YAML: %v", err)
	}
	if err := validateYAMLTree(&document); err != nil {
		return Spec{}, err
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return Spec{}, invalid("document must contain one mapping")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || root.Tag != "!!map" {
		return Spec{}, invalid("document root must be a mapping")
	}

	defaults := resources.Defaults()
	spec := Spec{
		Resources: defaults,
		Tools:     make([]ToolPin, 0),
		Apt:       make([]string, 0),
	}
	entries := mappingEntries(root)
	var err error
	for key, value := range entries {
		switch key {
		case "resources":
			spec.Resources, err = parseResources(value, defaults)
		case "tools":
			spec.Tools, err = parseTools(value)
		case "apt":
			spec.Apt, err = parseApt(value)
		case "setup":
			spec.Setup, err = parseSetup(value)
		default:
			err = invalid("unknown top-level key %q", key)
		}
		if err != nil {
			return Spec{}, err
		}
	}
	if err := validateResources(spec.Resources); err != nil {
		return Spec{}, err
	}
	if err := validateSpecFields(spec); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

// CanonicalJSON validates even a programmatically constructed Spec and returns
// its stable cache representation. Resource values must already be resolved.
func CanonicalJSON(spec Spec) ([]byte, error) {
	if err := validateResources(spec.Resources); err != nil {
		return nil, err
	}
	if err := validateSpecFields(spec); err != nil {
		return nil, err
	}
	tools := append([]ToolPin{}, spec.Tools...)
	apt := append([]string{}, spec.Apt...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	sort.Strings(apt)

	canonical := canonicalSpec{
		FormatVersion: canonicalVersion,
		Resources: canonicalResources{
			CPUs:         spec.Resources.CPUs,
			MemoryMiB:    spec.Resources.MemoryMiB,
			MaxMemoryMiB: spec.Resources.MaxMemoryMiB,
			WorkspaceMiB: spec.Resources.WorkspaceMiB,
			DockerMiB:    spec.Resources.DockerMiB,
		},
		Tools: tools,
		Apt:   apt,
		Setup: spec.Setup,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, invalid("encode canonical spec: %v", err)
	}
	if len(encoded) > maxCanonicalBytes {
		return nil, invalid("canonical spec exceeds 1 MiB")
	}
	return encoded, nil
}

// ParseCanonicalJSON parses only the exact serialized form emitted by
// CanonicalJSON. It is intended for persisted internal template records, not
// for accepting arbitrary client-provided JSON metadata.
func ParseCanonicalJSON(data []byte) (Spec, error) {
	if len(data) == 0 || len(data) > maxCanonicalBytes {
		return Spec{}, invalid("canonical spec must be between 1 byte and 1 MiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var canonical canonicalSpec
	if err := decoder.Decode(&canonical); err != nil {
		return Spec{}, invalid("decode canonical spec: %v", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Spec{}, invalid("canonical spec must contain exactly one JSON value")
		}
		return Spec{}, invalid("decode trailing canonical spec data: %v", err)
	}
	if canonical.FormatVersion != canonicalVersion {
		return Spec{}, invalid("unsupported canonical spec version %d", canonical.FormatVersion)
	}

	spec := Spec{
		Resources: resources.Resources{
			CPUs: canonical.Resources.CPUs, MemoryMiB: canonical.Resources.MemoryMiB,
			MaxMemoryMiB: canonical.Resources.MaxMemoryMiB,
			WorkspaceMiB: canonical.Resources.WorkspaceMiB, DockerMiB: canonical.Resources.DockerMiB,
		},
		Tools: canonical.Tools,
		Apt:   canonical.Apt,
		Setup: canonical.Setup,
	}
	encoded, err := CanonicalJSON(spec)
	if err != nil {
		return Spec{}, err
	}
	if !bytes.Equal(data, encoded) {
		return Spec{}, invalid("canonical spec bytes do not match the supported representation")
	}
	return spec, nil
}

type canonicalSpec struct {
	FormatVersion int                `json:"formatVersion"`
	Resources     canonicalResources `json:"resources"`
	Tools         []ToolPin          `json:"tools"`
	Apt           []string           `json:"apt"`
	Setup         string             `json:"setup"`
}

type canonicalResources struct {
	CPUs         int64 `json:"cpus"`
	MemoryMiB    int64 `json:"memoryMiB"`
	MaxMemoryMiB int64 `json:"maxMemoryMiB"`
	WorkspaceMiB int64 `json:"workspaceMiB"`
	DockerMiB    int64 `json:"dockerMiB"`
}

func parseResources(node *yaml.Node, defaults resources.Resources) (resources.Resources, error) {
	entries, err := requireMapping(node, "resources")
	if err != nil {
		return resources.Resources{}, err
	}
	resolved := defaults
	maxMemorySet := false
	for key, value := range entries {
		switch key {
		case "cpus":
			resolved.CPUs, err = parseCPU(value)
		case "memory":
			resolved.MemoryMiB, err = parseSize(value)
		case "max_memory":
			resolved.MaxMemoryMiB, err = parseSize(value)
			maxMemorySet = true
		case "workspace":
			resolved.WorkspaceMiB, err = parseSize(value)
		case "docker":
			resolved.DockerMiB, err = parseSize(value)
		default:
			err = invalid("unknown resources key %q", key)
		}
		if err != nil {
			return resources.Resources{}, err
		}
	}
	if !maxMemorySet {
		resolved.MaxMemoryMiB = resolved.MemoryMiB
	}
	if err := validateResources(resolved); err != nil {
		return resources.Resources{}, err
	}
	return resolved, nil
}

func parseCPU(node *yaml.Node) (int64, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !isPlain(node) || !decimalInteger.MatchString(node.Value) {
		return 0, invalid("cpus must be a plain decimal integer")
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, invalid("cpus is out of range")
	}
	return value, nil
}

func parseSize(node *yaml.Node) (int64, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return 0, invalid("resource sizes must be strings with MiB, GiB, or G units")
	}
	value := node.Value
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(value, "MiB"):
		value = strings.TrimSuffix(value, "MiB")
	case strings.HasSuffix(value, "GiB"):
		value = strings.TrimSuffix(value, "GiB")
		multiplier = mebibytesPerGibibyte
	case strings.HasSuffix(value, "G"):
		value = strings.TrimSuffix(value, "G")
		multiplier = mebibytesPerGibibyte
	default:
		return 0, invalid("resource size must use MiB, GiB, or G")
	}
	if !decimalInteger.MatchString(value) {
		return 0, invalid("resource size must be a nonnegative decimal integer")
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count > math.MaxInt64/multiplier {
		return 0, invalid("resource size is out of range")
	}
	return count * multiplier, nil
}

func parseTools(node *yaml.Node) ([]ToolPin, error) {
	entries, err := requireMapping(node, "tools")
	if err != nil {
		return nil, err
	}
	if len(entries) > maxToolPins {
		return nil, invalid("at most %d tools are supported", maxToolPins)
	}
	tools := make([]ToolPin, 0, len(entries))
	for name, value := range entries {
		if !supportedTool(name) {
			return nil, invalid("unsupported tool %q", name)
		}
		version, err := parseToolVersion(value)
		if err != nil {
			return nil, err
		}
		tools = append(tools, ToolPin{Name: name, Version: version})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func parseToolVersion(node *yaml.Node) (string, error) {
	quoted := node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !quoted || len(node.Value) > 64 || !toolVersion.MatchString(node.Value) {
		return "", invalid("tool versions must be quoted numeric dotted strings of at most 64 bytes")
	}
	return node.Value, nil
}

func parseApt(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
		return nil, invalid("apt must be a sequence of package-name strings")
	}
	if len(node.Content) > maxAptPackages {
		return nil, invalid("apt contains more than %d packages", maxAptPackages)
	}
	packages := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for _, item := range node.Content {
		name, err := parsePackageName(item)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, invalid("duplicate apt package %q", name)
		}
		seen[name] = struct{}{}
		packages = append(packages, name)
	}
	return packages, nil
}

func parsePackageName(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || len(node.Value) < 2 || len(node.Value) > 128 || !packageName.MatchString(node.Value) {
		return "", invalid("apt entries must be package names matching [a-z0-9][a-z0-9+.-]*")
	}
	return node.Value, nil
}

func parseSetup(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", invalid("setup must be a string scalar")
	}
	if err := validateSetup(node.Value); err != nil {
		return "", err
	}
	return node.Value, nil
}

func validateSpecFields(spec Spec) error {
	if len(spec.Tools) > maxToolPins {
		return invalid("at most %d tools are supported", maxToolPins)
	}
	seenTools := make(map[string]struct{}, len(spec.Tools))
	for _, tool := range spec.Tools {
		if !supportedTool(tool.Name) {
			return invalid("unsupported tool %q", tool.Name)
		}
		if _, duplicate := seenTools[tool.Name]; duplicate {
			return invalid("duplicate tool %q", tool.Name)
		}
		seenTools[tool.Name] = struct{}{}
		if len(tool.Version) > 64 || !toolVersion.MatchString(tool.Version) {
			return invalid("tool versions must be numeric dotted strings of at most 64 bytes")
		}
	}
	if len(spec.Apt) > maxAptPackages {
		return invalid("apt contains more than %d packages", maxAptPackages)
	}
	seenPackages := make(map[string]struct{}, len(spec.Apt))
	for _, name := range spec.Apt {
		if len(name) < 2 || len(name) > 128 || !packageName.MatchString(name) {
			return invalid("apt entries must be package names matching [a-z0-9][a-z0-9+.-]*")
		}
		if _, duplicate := seenPackages[name]; duplicate {
			return invalid("duplicate apt package %q", name)
		}
		seenPackages[name] = struct{}{}
	}
	if err := validateSetup(spec.Setup); err != nil {
		return err
	}
	return nil
}

func validateSetup(value string) error {
	if strings.IndexByte(value, 0) >= 0 {
		return invalid("setup must not contain NUL bytes")
	}
	if len(value) > maxSetupBytes || !utf8.ValidString(value) {
		return invalid("setup must be valid UTF-8 and at most 64 KiB")
	}
	return nil
}

func validateResources(value resources.Resources) error {
	if err := value.Validate(); err != nil {
		return invalid("resources: %v", err)
	}
	return nil
}

func supportedTool(name string) bool {
	// This is Studio's initial supported spec set, not a statement about the
	// broader set of tools that mise can install.
	switch name {
	case "node", "python", "go":
		return true
	default:
		return false
	}
}

func requireMapping(node *yaml.Node, description string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, invalid("%s must be a mapping", description)
	}
	return mappingEntries(node), nil
}

func mappingEntries(node *yaml.Node) map[string]*yaml.Node {
	entries := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		entries[node.Content[i].Value] = node.Content[i+1]
	}
	return entries
}

func validateYAMLTree(root *yaml.Node) error {
	type pendingNode struct {
		node  *yaml.Node
		depth int
	}
	stack := []pendingNode{{node: root, depth: 1}}
	nodes := 0
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		nodes++
		if nodes > maxYAMLNodes {
			return invalid("YAML document exceeds %d nodes", maxYAMLNodes)
		}
		if current.depth > maxYAMLDepth {
			return invalid("YAML document exceeds depth %d", maxYAMLDepth)
		}
		node := current.node
		if node.Anchor != "" || node.Kind == yaml.AliasNode {
			return invalid("YAML anchors and aliases are not supported")
		}
		if node.Kind != yaml.DocumentNode && (!knownYAMLTag(node.Tag) || node.Tag == "!!merge") {
			return invalid("YAML tag %q is not supported", node.Tag)
		}
		switch node.Kind {
		case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode, yaml.ScalarNode:
		default:
			return invalid("YAML node kind %d is not supported", node.Kind)
		}
		if node.Kind == yaml.MappingNode {
			if len(node.Content)%2 != 0 {
				return invalid("malformed YAML mapping")
			}
			seen := make(map[string]struct{}, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
					return invalid("YAML mapping keys must be strings")
				}
				if key.Value == "<<" {
					return invalid("YAML merge keys are not supported")
				}
				if _, duplicate := seen[key.Value]; duplicate {
					return invalid("duplicate YAML key %q", key.Value)
				}
				seen[key.Value] = struct{}{}
			}
		}
		for i := len(node.Content) - 1; i >= 0; i-- {
			stack = append(stack, pendingNode{node: node.Content[i], depth: current.depth + 1})
		}
	}
	return nil
}

func knownYAMLTag(tag string) bool {
	switch tag {
	case "!!map", "!!seq", "!!str", "!!int", "!!bool", "!!null", "!!float", "!!timestamp", "!!binary", "!!merge":
		return true
	default:
		return false
	}
}

func isPlain(node *yaml.Node) bool {
	return node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
