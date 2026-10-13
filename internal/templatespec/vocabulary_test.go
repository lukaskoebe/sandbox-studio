package templatespec

import (
	"slices"
	"testing"
)

// Every name the editor suggests must parse as written.
func TestEditorVocabularyParses(t *testing.T) {
	vocabulary := EditorVocabulary()
	specs := map[string]string{
		"resources":  "resources: {}\n",
		"tools":      "tools: {}\n",
		"apt":        "apt: []\n",
		"setup":      "setup: ''\n",
		"cpus":       "resources:\n  cpus: 2\n",
		"memory":     "resources:\n  memory: 4GiB\n",
		"max_memory": "resources:\n  max_memory: 4096MiB\n",
		"workspace":  "resources:\n  workspace: 20GiB\n",
		"docker":     "resources:\n  docker: 20GiB\n",
		"go":         "tools:\n  go: \"1.24\"\n",
		"nix":        "tools:\n  nix: \"2.35.1\"\n",
		"node":       "tools:\n  node: \"22\"\n",
		"python":     "tools:\n  python: \"3.13\"\n",
	}
	for _, name := range slices.Concat(vocabulary.TopLevelKeys, vocabulary.ResourceKeys, vocabulary.Tools) {
		spec, ok := specs[name]
		if !ok {
			t.Errorf("no sample spec for suggested name %q", name)
			continue
		}
		if _, err := ParseYAML([]byte(spec)); err != nil {
			t.Errorf("suggested name %q is rejected: %v", name, err)
		}
	}
	for _, unit := range vocabulary.SizeUnits {
		if _, err := ParseYAML([]byte("resources:\n  memory: 4096" + unit + "\n")); err != nil {
			t.Errorf("suggested size unit %q is rejected: %v", unit, err)
		}
	}
}
