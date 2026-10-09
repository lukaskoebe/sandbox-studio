package templateimage

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

func TestComposeDeterministicAndPreservesBase(t *testing.T) {
	base, layer := testInputs()
	baseBefore := cloneBase(base)

	first, err := Compose(base, layer)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	second, err := Compose(base, layer)
	if err != nil {
		t.Fatalf("Compose() second error = %v", err)
	}
	if !bytes.Equal(first.Manifest, second.Manifest) || !bytes.Equal(first.Config, second.Config) {
		t.Fatal("Compose() output changed between identical calls")
	}
	if !reflect.DeepEqual(base, baseBefore) {
		t.Fatal("Compose() mutated the base")
	}

	var gotManifest manifest
	if err := json.Unmarshal(first.Manifest, &gotManifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if gotManifest.SchemaVersion != 2 || gotManifest.MediaType != MediaManifest {
		t.Fatalf("manifest header = (%d, %q)", gotManifest.SchemaVersion, gotManifest.MediaType)
	}
	if len(gotManifest.Layers) != len(base.Layers)+1 {
		t.Fatalf("manifest layer count = %d, want %d", len(gotManifest.Layers), len(base.Layers)+1)
	}
	if gotManifest.Layers[0].MediaType != MediaLayer {
		t.Fatalf("converted Docker layer media type = %q, want %q", gotManifest.Layers[0].MediaType, MediaLayer)
	}
	if gotManifest.Layers[1].MediaType != mediaLayerZstd {
		t.Fatalf("OCI zstd media type changed to %q", gotManifest.Layers[1].MediaType)
	}
	if gotManifest.Layers[2] != first.LayerDescriptor {
		t.Fatalf("exported layer descriptor = %+v, want %+v", gotManifest.Layers[2], first.LayerDescriptor)
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
	wantDiffIDs := []string{base.Layers[0].DiffID, base.Layers[1].DiffID, layer.DiffID}
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

func TestComposeLayerMediaTypes(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "Docker gzip", input: mediaDockerLayer, want: MediaLayer},
		{name: "Docker tar", input: mediaDockerLayerTar, want: mediaLayerTar},
		{name: "OCI tar", input: mediaLayerTar, want: mediaLayerTar},
		{name: "OCI gzip", input: MediaLayer, want: MediaLayer},
		{name: "OCI zstd", input: mediaLayerZstd, want: mediaLayerZstd},
		{name: "Docker foreign gzip", input: "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip", wantError: true},
		{name: "OCI nondistributable", input: "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip", wantError: true},
		{name: "unknown", input: "application/example.layer", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, layer := testInputs()
			base.Layers = base.Layers[:1]
			base.Layers[0].MediaType = tc.input

			image, err := Compose(base, layer)
			if tc.wantError {
				if err == nil {
					t.Fatal("Compose() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Compose() error = %v", err)
			}
			var got manifest
			if err := json.Unmarshal(image.Manifest, &got); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			if got.Layers[0].MediaType != tc.want {
				t.Fatalf("converted media type = %q, want %q", got.Layers[0].MediaType, tc.want)
			}
		})
	}
}

func TestComposeRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Base, *templateexport.Layer)
	}{
		{name: "unsupported OS", mutate: func(base *Base, _ *templateexport.Layer) { base.OS = "windows" }},
		{name: "unsupported architecture", mutate: func(base *Base, _ *templateexport.Layer) { base.Architecture = "386" }},
		{name: "inappropriate variant", mutate: func(base *Base, _ *templateexport.Layer) { base.Architecture, base.Variant = "amd64", "v8" }},
		{name: "malformed variant", mutate: func(base *Base, _ *templateexport.Layer) { base.Architecture, base.Variant = "arm64", "v8/other" }},
		{name: "invalid base digest", mutate: func(base *Base, _ *templateexport.Layer) { base.Digest = "SHA256:" + strings.Repeat("a", 64) }},
		{name: "empty base layers", mutate: func(base *Base, _ *templateexport.Layer) { base.Layers = nil }},
		{name: "invalid base layer digest", mutate: func(base *Base, _ *templateexport.Layer) { base.Layers[0].Digest = "sha256:abc" }},
		{name: "zero base layer size", mutate: func(base *Base, _ *templateexport.Layer) { base.Layers[0].Size = 0 }},
		{name: "invalid base diff ID", mutate: func(base *Base, _ *templateexport.Layer) { base.Layers[0].DiffID = "sha256:" + strings.Repeat("A", 64) }},
		{name: "foreign base layer", mutate: func(base *Base, _ *templateexport.Layer) {
			base.Layers[0].MediaType = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
		}},
		{name: "invalid exported digest", mutate: func(_ *Base, layer *templateexport.Layer) { layer.Digest = "sha256:" + strings.Repeat("z", 64) }},
		{name: "zero exported size", mutate: func(_ *Base, layer *templateexport.Layer) { layer.Size = 0 }},
		{name: "negative uncompressed size", mutate: func(_ *Base, layer *templateexport.Layer) { layer.UncompressedSize = -1 }},
		{name: "invalid exported diff ID", mutate: func(_ *Base, layer *templateexport.Layer) { layer.DiffID = "sha256:" + strings.Repeat("A", 64) }},
		{name: "config exceeds limit", mutate: func(base *Base, _ *templateexport.Layer) { base.Config.WorkingDir = strings.Repeat("x", maxJSONSize) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, layer := testInputs()
			tc.mutate(&base, &layer)
			if _, err := Compose(base, layer); err == nil {
				t.Fatal("Compose() error = nil, want error")
			}
		})
	}
}

func TestComposeLayerCountLimit(t *testing.T) {
	base, layer := testInputs()
	base.Layers = make([]BaseLayer, maxImageLayers-1)
	for i := range base.Layers {
		base.Layers[i] = testInputsLayer()
	}
	if _, err := Compose(base, layer); err != nil {
		t.Fatalf("Compose() at %d total layers: %v", maxImageLayers, err)
	}
	base.Layers = append(base.Layers, testInputsLayer())
	if _, err := Compose(base, layer); err == nil {
		t.Fatalf("Compose() with more than %d total layers succeeded", maxImageLayers)
	}
}

func TestCacheKeyValidationAndBoundaries(t *testing.T) {
	digest := testDigest("base")
	valid, err := CacheKey([]byte("{}"), digest, "linux/amd64", ExporterVersion)
	if err != nil {
		t.Fatalf("CacheKey() valid input error = %v", err)
	}
	if !ValidDigest(valid) {
		t.Fatalf("CacheKey() = %q, want lowercase sha256 digest", valid)
	}
	if _, err := CacheKey(make([]byte, maxJSONSize), digest, "linux/amd64", ExporterVersion); err != nil {
		t.Fatalf("CacheKey() at spec limit error = %v", err)
	}

	invalid := []struct {
		name            string
		spec            []byte
		baseDigest      string
		platform        string
		exporterVersion string
	}{
		{name: "empty spec", spec: nil, baseDigest: digest, platform: "linux/amd64", exporterVersion: "1"},
		{name: "oversized spec", spec: make([]byte, maxJSONSize+1), baseDigest: digest, platform: "linux/amd64", exporterVersion: "1"},
		{name: "invalid digest", spec: []byte("{}"), baseDigest: "sha256:ABC", platform: "linux/amd64", exporterVersion: "1"},
		{name: "invalid platform", spec: []byte("{}"), baseDigest: digest, platform: "linux/arm64/v7", exporterVersion: "1"},
		{name: "empty exporter", spec: []byte("{}"), baseDigest: digest, platform: "linux/amd64", exporterVersion: ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CacheKey(tc.spec, tc.baseDigest, tc.platform, tc.exporterVersion); err == nil {
				t.Fatal("CacheKey() error = nil, want error")
			}
		})
	}
}

func TestCacheKeyFramesFieldsAndTracksOrder(t *testing.T) {
	spec := []byte("canonical spec")
	digest := testDigest("base")
	key, err := CacheKey(spec, digest, "linux/amd64", "1")
	if err != nil {
		t.Fatalf("CacheKey() error = %v", err)
	}

	changed := []struct {
		name            string
		spec            []byte
		baseDigest      string
		platform        string
		exporterVersion string
	}{
		{name: "spec", spec: []byte("canonical spec "), baseDigest: digest, platform: "linux/amd64", exporterVersion: "1"},
		{name: "base digest", spec: spec, baseDigest: testDigest("other base"), platform: "linux/amd64", exporterVersion: "1"},
		{name: "platform", spec: spec, baseDigest: digest, platform: "linux/arm64", exporterVersion: "1"},
		{name: "exporter version", spec: spec, baseDigest: digest, platform: "linux/amd64", exporterVersion: "2"},
	}
	for _, tc := range changed {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CacheKey(tc.spec, tc.baseDigest, tc.platform, tc.exporterVersion)
			if err != nil {
				t.Fatalf("CacheKey() error = %v", err)
			}
			if got == key {
				t.Fatal("CacheKey() did not change when an input field changed")
			}
		})
	}

	ordered, err := CacheKey([]byte(`{"a":1,"b":2}`), digest, "linux/amd64", "1")
	if err != nil {
		t.Fatalf("CacheKey() ordered spec error = %v", err)
	}
	reordered, err := CacheKey([]byte(`{"b":2,"a":1}`), digest, "linux/amd64", "1")
	if err != nil {
		t.Fatalf("CacheKey() reordered spec error = %v", err)
	}
	if ordered == reordered {
		t.Fatal("CacheKey() did not preserve exact spec byte order")
	}

	first, err := CacheKey(spec, digest, "linux/arm64", "/v8x")
	if err != nil {
		t.Fatalf("CacheKey() first boundary case error = %v", err)
	}
	second, err := CacheKey(spec, digest, "linux/arm64/v8", "x")
	if err != nil {
		t.Fatalf("CacheKey() second boundary case error = %v", err)
	}
	if first == second {
		t.Fatal("CacheKey() collided when platform and exporter boundaries changed")
	}
}

func TestValidDigest(t *testing.T) {
	valid := "sha256:" + strings.Repeat("0", 64)
	if !ValidDigest(valid) {
		t.Fatalf("ValidDigest(%q) = false", valid)
	}
	for _, invalid := range []string{
		"",
		"sha256:" + strings.Repeat("0", 63),
		"sha256:" + strings.Repeat("0", 65),
		"SHA256:" + strings.Repeat("0", 64),
		"sha256:" + strings.Repeat("A", 64),
		"sha512:" + strings.Repeat("0", 64),
	} {
		if ValidDigest(invalid) {
			t.Errorf("ValidDigest(%q) = true, want false", invalid)
		}
	}
}

func testInputs() (Base, templateexport.Layer) {
	base := Base{
		Reference:    "studio/base:stable",
		Digest:       testDigest("base manifest"),
		Architecture: "arm64",
		OS:           "linux",
		Variant:      "v8",
		Config: Config{
			Env:        []string{"PATH=/usr/bin", "LANG=C"},
			Cmd:        []string{"/bin/sh", "-c", "echo ready"},
			Entrypoint: []string{"/entrypoint"},
			WorkingDir: "/workspace",
			User:       "1000:1000",
			StopSignal: "SIGTERM",
			Labels:     map[string]string{"z": "last", "a": "first"},
		},
		Layers: []BaseLayer{
			{
				Descriptor: Descriptor{MediaType: mediaDockerLayer, Digest: testDigest("base layer gzip"), Size: 123},
				DiffID:     testDigest("base layer tar"),
			},
			{
				Descriptor: Descriptor{MediaType: mediaLayerZstd, Digest: testDigest("base layer zstd"), Size: 456},
				DiffID:     testDigest("base layer zstd tar"),
			},
		},
	}
	layer := templateexport.Layer{
		Path:             "/this/path/is/not/read",
		Digest:           testDigest("exported gzip layer"),
		DiffID:           testDigest("exported tar"),
		Size:             789,
		UncompressedSize: 0,
	}
	return base, layer
}

func testInputsLayer() BaseLayer {
	return BaseLayer{
		Descriptor: Descriptor{MediaType: MediaLayer, Digest: testDigest("base layer"), Size: 1},
		DiffID:     testDigest("base diff ID"),
	}
}

func testDigest(value string) string {
	return Digest([]byte(value))
}

func cloneBase(base Base) Base {
	base.Config.Env = append([]string(nil), base.Config.Env...)
	base.Config.Cmd = append([]string(nil), base.Config.Cmd...)
	base.Config.Entrypoint = append([]string(nil), base.Config.Entrypoint...)
	if base.Config.Labels != nil {
		labels := base.Config.Labels
		base.Config.Labels = make(map[string]string, len(labels))
		for key, value := range labels {
			base.Config.Labels[key] = value
		}
	}
	base.Layers = append([]BaseLayer(nil), base.Layers...)
	return base
}
