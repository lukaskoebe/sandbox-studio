package templatespec

import (
	"bytes"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// YAML renders spec as a document that ParseYAML reads back to the same canonical spec.
// All resources are written out, so the result does not depend on future defaults.
func YAML(spec Spec) ([]byte, error) {
	want, err := CanonicalJSON(spec)
	if err != nil {
		return nil, err
	}
	// Literal style keeps a setup script readable; double quotes represent any string.
	for _, setupStyle := range []yaml.Style{yaml.LiteralStyle, yaml.DoubleQuotedStyle} {
		out, err := renderYAML(spec, setupStyle)
		if err != nil {
			return nil, invalid("render YAML: %v", err)
		}
		parsed, err := ParseYAML(out)
		if err != nil {
			continue
		}
		if got, err := CanonicalJSON(parsed); err == nil && bytes.Equal(got, want) {
			return out, nil
		}
	}
	return nil, invalid("spec cannot be rendered as YAML of at most 64 KiB")
}

func renderYAML(spec Spec, setupStyle yaml.Style) ([]byte, error) {
	str := func(value string, style yaml.Style) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style}
	}
	size := func(mib int64) *yaml.Node { return str(strconv.FormatInt(mib, 10)+"MiB", 0) }
	mapping := func(pairs ...*yaml.Node) *yaml.Node {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
	}
	r := spec.Resources
	root := mapping(
		str("resources", 0), mapping(
			str("cpus", 0), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(r.CPUs, 10)},
			str("memory", 0), size(r.MemoryMiB),
			str("max_memory", 0), size(r.MaxMemoryMiB),
			str("workspace", 0), size(r.WorkspaceMiB),
			str("docker", 0), size(r.DockerMiB),
		),
	)
	if len(spec.Tools) > 0 {
		tools := mapping()
		for _, tool := range spec.Tools {
			tools.Content = append(tools.Content, str(tool.Name, 0), str(tool.Version, yaml.DoubleQuotedStyle))
		}
		root.Content = append(root.Content, str("tools", 0), tools)
	}
	if len(spec.Apt) > 0 {
		apt := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, name := range spec.Apt {
			apt.Content = append(apt.Content, str(name, 0))
		}
		root.Content = append(root.Content, str("apt", 0), apt)
	}
	if spec.Setup != "" {
		root.Content = append(root.Content, str("setup", 0), str(spec.Setup, setupStyle))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
