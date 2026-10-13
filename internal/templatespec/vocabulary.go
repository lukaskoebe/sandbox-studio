package templatespec

import "slices"

// toolNames are the tools the parser accepts under tools, in suggestion order.
var toolNames = []string{"go", NixTool, "node", "python"}

// Vocabulary lists the names a spec editor should suggest. Each name is one the
// parser accepts; vocabulary_test.go keeps it that way.
type Vocabulary struct {
	TopLevelKeys []string `json:"topLevelKeys"`
	ResourceKeys []string `json:"resourceKeys"`
	Tools        []string `json:"tools"`
	SizeUnits    []string `json:"sizeUnits"`
}

// EditorVocabulary returns the names to suggest, in suggestion order.
func EditorVocabulary() Vocabulary {
	return Vocabulary{
		TopLevelKeys: []string{"resources", "tools", "apt", "setup"},
		ResourceKeys: []string{"cpus", "memory", "max_memory", "workspace", "docker"},
		Tools:        slices.Clone(toolNames),
		SizeUnits:    []string{"MiB", "GiB"},
	}
}
