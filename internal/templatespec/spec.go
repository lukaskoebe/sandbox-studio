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
	"slices"
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

// Stack names a spec by its tool pins, such as node-22+python-3.13. A spec without
// tools is base. Parsed and canonical specs keep their tools sorted by name.
func Stack(spec Spec) string {
	if len(spec.Tools) == 0 {
		return "base"
	}
	pins := make([]string, len(spec.Tools))
	for i, tool := range spec.Tools {
		pins[i] = tool.Name + "-" + tool.Version
	}
	return strings.Join(pins, "+")
}

// ParseYAML parses one bounded YAML mapping, rejects unsupported YAML features,
// and resolves resource defaults before returning the spec. Its errors are
// *Error values listing every problem found, with source positions.
func ParseYAML(data []byte) (Spec, error) {
	if len(data) == 0 || len(data) > maxInputBytes {
		return Spec{}, inputError(invalid("input must be between 1 byte and 64 KiB"))
	}
	if !utf8.Valid(data) {
		return Spec{}, inputError(invalid("input must be valid UTF-8"))
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err == io.EOF {
		return Spec{}, inputError(invalid("document must contain one mapping"))
	} else if err != nil {
		return Spec{}, syntaxError("parse YAML: ", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			var p problems
			p.add(&trailing, "exactly one YAML document is allowed")
			return Spec{}, p.err()
		}
		return Spec{}, syntaxError("parse trailing YAML: ", err)
	}
	var p problems
	if validateYAMLTree(&document, &p); len(p.list) > 0 {
		return Spec{}, p.err()
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		p.add(&document, "document must contain one mapping")
		return Spec{}, p.err()
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || root.Tag != "!!map" {
		p.add(root, "document root must be a mapping")
		return Spec{}, p.err()
	}

	defaults := resources.Defaults()
	spec := Spec{
		Resources: defaults,
		Tools:     make([]ToolPin, 0),
		Apt:       make([]string, 0),
	}
	// Walk the mapping in source order so problems come out in a stable order.
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "resources":
			spec.Resources = parseResources(key, value, defaults, &p)
		case "tools":
			spec.Tools = parseTools(key, value, &p)
		case "apt":
			spec.Apt = parseApt(key, value, &p)
		case "setup":
			setup, err := parseSetup(value)
			if err != nil {
				p.addErr(at(key, value), err)
			}
			spec.Setup = setup
		default:
			p.add(key, "unknown top-level key %q", key.Value)
		}
	}
	if err := p.err(); err != nil {
		return Spec{}, err
	}
	if err := validateResources(spec.Resources); err != nil {
		return Spec{}, inputError(err)
	}
	if err := validateSpecFields(spec); err != nil {
		return Spec{}, inputError(err)
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

// resourceKeys maps the field names in resources.Validate errors to spec keys.
var resourceKeys = map[string]string{
	"cpus": "cpus", "memoryMiB": "memory", "maxMemoryMiB": "max_memory",
	"workspaceMiB": "workspace", "dockerMiB": "docker",
}

func parseResources(key, node *yaml.Node, defaults resources.Resources, p *problems) resources.Resources {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		p.add(at(key, node), "resources must be a mapping")
		return defaults
	}
	resolved := defaults
	maxMemorySet := false
	values := make(map[string]*yaml.Node, len(node.Content)/2)
	before := len(p.list)
	for i := 0; i < len(node.Content); i += 2 {
		name, value := node.Content[i], node.Content[i+1]
		var err error
		switch name.Value {
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
			p.add(name, "unknown resources key %q", name.Value)
			continue
		}
		if err != nil {
			p.addErr(at(name, value), err)
		}
		values[name.Value] = at(name, value)
	}
	if len(p.list) > before {
		return defaults
	}
	if !maxMemorySet {
		resolved.MaxMemoryMiB = resolved.MemoryMiB
	}
	if err := resolved.Validate(); err != nil {
		// Report a range problem at the value it names, or at "resources" when
		// that value is a default.
		where := key
		detail := strings.TrimPrefix(err.Error(), resources.ErrInvalid.Error()+": ")
		if field, _, ok := strings.Cut(detail, " "); ok && values[resourceKeys[field]] != nil {
			where = values[resourceKeys[field]]
		}
		p.addErr(where, validateResources(resolved))
	}
	return resolved
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

func parseTools(key, node *yaml.Node, p *problems) []ToolPin {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		p.add(at(key, node), "tools must be a mapping")
		return nil
	}
	if len(node.Content)/2 > maxToolPins {
		p.add(key, "at most %d tools are supported", maxToolPins)
		return nil
	}
	tools := make([]ToolPin, 0, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name, value := node.Content[i], node.Content[i+1]
		if !supportedTool(name.Value) {
			p.add(name, "unsupported tool %q", name.Value)
			continue
		}
		version, err := parseToolVersion(value)
		if err != nil {
			p.addErr(at(name, value), err)
			continue
		}
		tools = append(tools, ToolPin{Name: name.Value, Version: version})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

func parseToolVersion(node *yaml.Node) (string, error) {
	quoted := node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !quoted || len(node.Value) > 64 || !toolVersion.MatchString(node.Value) {
		return "", invalid("tool versions must be quoted numeric dotted strings of at most 64 bytes")
	}
	return node.Value, nil
}

func parseApt(key, node *yaml.Node, p *problems) []string {
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
		p.add(at(key, node), "apt must be a sequence of package-name strings")
		return nil
	}
	if len(node.Content) > maxAptPackages {
		p.add(key, "apt contains more than %d packages", maxAptPackages)
		return nil
	}
	packages := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for _, item := range node.Content {
		name, err := parsePackageName(item)
		if err != nil {
			p.addErr(item, err)
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			p.add(item, "duplicate apt package %q", name)
			continue
		}
		seen[name] = struct{}{}
		packages = append(packages, name)
	}
	return packages
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
	return slices.Contains(toolNames, name)
}

// validateYAMLTree records unsupported YAML features in p. It stops at the
// first size limit it hits, so the walk stays bounded.
func validateYAMLTree(root *yaml.Node, p *problems) {
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
		node := current.node
		nodes++
		if nodes > maxYAMLNodes {
			p.add(node, "YAML document exceeds %d nodes", maxYAMLNodes)
			return
		}
		if current.depth > maxYAMLDepth {
			p.add(node, "YAML document exceeds depth %d", maxYAMLDepth)
			return
		}
		switch {
		case node.Anchor != "" || node.Kind == yaml.AliasNode:
			p.add(node, "YAML anchors and aliases are not supported")
		case node.Kind != yaml.DocumentNode && (!knownYAMLTag(node.Tag) || node.Tag == "!!merge"):
			p.add(node, "YAML tag %q is not supported", node.Tag)
		case node.Kind != yaml.DocumentNode && node.Kind != yaml.MappingNode && node.Kind != yaml.SequenceNode && node.Kind != yaml.ScalarNode:
			p.add(node, "YAML node kind %d is not supported", node.Kind)
		}
		if node.Kind == yaml.MappingNode {
			if len(node.Content)%2 != 0 {
				p.add(node, "malformed YAML mapping")
				continue
			}
			seen := make(map[string]struct{}, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				switch _, duplicate := seen[key.Value]; {
				case key.Value == "<<":
					p.add(key, "YAML merge keys are not supported")
				case key.Kind != yaml.ScalarNode || key.Tag != "!!str":
					p.add(key, "YAML mapping keys must be strings")
				case duplicate:
					p.add(key, "duplicate YAML key %q", key.Value)
				}
				seen[key.Value] = struct{}{}
			}
		}
		for i := len(node.Content) - 1; i >= 0; i-- {
			stack = append(stack, pendingNode{node: node.Content[i], depth: current.depth + 1})
		}
	}
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
