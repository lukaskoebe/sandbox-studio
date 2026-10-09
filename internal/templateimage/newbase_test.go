package templateimage

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestComposeBaseDeterministicAndPreservesBase(t *testing.T) {
	base, _ := testInputs()
	base.Layers = base.Layers[:1]
	baseBefore := cloneBase(base)

	first, err := ComposeBase(base)
	if err != nil {
		t.Fatalf("ComposeBase() error = %v", err)
	}
	second, err := ComposeBase(base)
	if err != nil {
		t.Fatalf("ComposeBase() second error = %v", err)
	}
	if !bytes.Equal(first.Manifest, second.Manifest) || !bytes.Equal(first.Config, second.Config) {
		t.Fatal("ComposeBase() output changed between identical calls")
	}
	if !reflect.DeepEqual(first.LayerDescriptor, Descriptor{}) {
		t.Fatalf("LayerDescriptor = %+v, want zero descriptor", first.LayerDescriptor)
	}
	if !reflect.DeepEqual(base, baseBefore) {
		t.Fatal("ComposeBase() mutated the base")
	}

	var gotManifest manifest
	if err := json.Unmarshal(first.Manifest, &gotManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if gotManifest.SchemaVersion != 2 || gotManifest.MediaType != MediaManifest {
		t.Fatalf("manifest header = (%d, %q)", gotManifest.SchemaVersion, gotManifest.MediaType)
	}
	if len(gotManifest.Layers) != len(base.Layers) {
		t.Fatalf("manifest layer count = %d, want %d", len(gotManifest.Layers), len(base.Layers))
	}
	wantLayers := make([]Descriptor, 0, len(base.Layers))
	wantDiffIDs := make([]string, 0, len(base.Layers))
	for _, baseLayer := range base.Layers {
		mediaType, ok := ociLayerMediaType(baseLayer.MediaType)
		if !ok {
			t.Fatalf("test base media type %q is unsupported", baseLayer.MediaType)
		}
		descriptor := baseLayer.Descriptor
		descriptor.MediaType = mediaType
		wantLayers = append(wantLayers, descriptor)
		wantDiffIDs = append(wantDiffIDs, baseLayer.DiffID)
	}
	if !reflect.DeepEqual(gotManifest.Layers, wantLayers) {
		t.Fatalf("manifest layers = %#v, want normalized base layers %#v", gotManifest.Layers, wantLayers)
	}

	var gotConfig imageConfig
	if err := json.Unmarshal(first.Config, &gotConfig); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if gotConfig.Architecture != base.Architecture || gotConfig.OS != base.OS || gotConfig.Variant != base.Variant {
		t.Fatalf("config platform = (%q, %q, %q)", gotConfig.OS, gotConfig.Architecture, gotConfig.Variant)
	}
	if !reflect.DeepEqual(gotConfig.Config, base.Config) {
		t.Fatalf("common config = %#v, want %#v", gotConfig.Config, base.Config)
	}
	if gotConfig.RootFS.Type != "layers" || !reflect.DeepEqual(gotConfig.RootFS.DiffIDs, wantDiffIDs) {
		t.Fatalf("rootfs = %#v, want type layers and diff IDs %#v", gotConfig.RootFS, wantDiffIDs)
	}
	if first.ConfigDescriptor.MediaType != MediaConfig || first.ConfigDescriptor.Digest != Digest(first.Config) || first.ConfigDescriptor.Size != int64(len(first.Config)) {
		t.Fatalf("config descriptor does not describe config bytes: %+v", first.ConfigDescriptor)
	}
	if first.ManifestDescriptor.MediaType != MediaManifest || first.ManifestDescriptor.Digest != Digest(first.Manifest) || first.ManifestDescriptor.Size != int64(len(first.Manifest)) {
		t.Fatalf("manifest descriptor does not describe manifest bytes: %+v", first.ManifestDescriptor)
	}
}

func TestComposeBaseRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Base)
	}{
		{name: "unsupported OS", mutate: func(base *Base) { base.OS = "windows" }},
		{name: "unsupported architecture", mutate: func(base *Base) { base.Architecture = "386" }},
		{name: "inappropriate variant", mutate: func(base *Base) { base.Architecture, base.Variant = "amd64", "v8" }},
		{name: "malformed variant", mutate: func(base *Base) { base.Architecture, base.Variant = "arm64", "v8/other" }},
		{name: "invalid base digest", mutate: func(base *Base) { base.Digest = "SHA256:" + strings.Repeat("a", 64) }},
		{name: "empty base layers", mutate: func(base *Base) { base.Layers = nil }},
		{name: "invalid base layer digest", mutate: func(base *Base) { base.Layers[0].Digest = "sha256:abc" }},
		{name: "zero base layer size", mutate: func(base *Base) { base.Layers[0].Size = 0 }},
		{name: "invalid base diff ID", mutate: func(base *Base) { base.Layers[0].DiffID = "sha256:" + strings.Repeat("A", 64) }},
		{name: "unsupported base media type", mutate: func(base *Base) { base.Layers[0].MediaType = "application/example.layer" }},
		{name: "config exceeds limit", mutate: func(base *Base) { base.Config.WorkingDir = strings.Repeat("x", maxJSONSize) }},
		{name: "too many base layers", mutate: func(base *Base) {
			base.Layers = make([]BaseLayer, maxImageLayers+1)
			for i := range base.Layers {
				base.Layers[i] = testInputsLayer()
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, _ := testInputs()
			tc.mutate(&base)
			if _, err := ComposeBase(base); err == nil {
				t.Fatal("ComposeBase() error = nil, want error")
			}
		})
	}
}

func TestComposeBaseLayerLimit(t *testing.T) {
	base, _ := testInputs()
	base.Layers = make([]BaseLayer, maxImageLayers)
	for i := range base.Layers {
		base.Layers[i] = testInputsLayer()
	}
	if _, err := ComposeBase(base); err != nil {
		t.Fatalf("ComposeBase() with %d layers: %v", maxImageLayers, err)
	}
}
