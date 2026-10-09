// Package templateimage composes deterministic OCI image metadata for
// Studio-controlled base images.
package templateimage

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
)

const (
	MediaManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaConfig   = "application/vnd.oci.image.config.v1+json"
	MediaLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"

	ExporterVersion = "1"
)

const (
	mediaLayerTar       = "application/vnd.oci.image.layer.v1.tar"
	mediaLayerZstd      = "application/vnd.oci.image.layer.v1.tar+zstd"
	mediaDockerLayer    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	mediaDockerLayerTar = "application/vnd.docker.image.rootfs.diff.tar"
	maxJSONSize         = 1 << 20
	maxImageLayers      = 256
	digestPrefix        = "sha256:"
)

// Descriptor is the OCI descriptor subset used by image manifests.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// BaseLayer describes a layer already present in a Studio-controlled base.
type BaseLayer struct {
	Descriptor
	DiffID string
}

// Config contains the common OCI runtime configuration subset inherited from
// the Studio-controlled base image.
type Config struct {
	Env        []string          `json:"Env,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	User       string            `json:"User,omitempty"`
	StopSignal string            `json:"StopSignal,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

// Base is metadata for a Studio-controlled OCI base image. Compose supports
// this known base metadata contract; it does not claim arbitrary image support.
type Base struct {
	Reference    string
	Digest       string
	Architecture string
	OS           string
	Variant      string
	Config       Config
	Layers       []BaseLayer
}

// Image contains deterministic OCI manifest and config JSON plus their
// descriptors and the descriptor for the exported gzip layer.
type Image struct {
	Manifest           []byte
	Config             []byte
	ManifestDescriptor Descriptor
	ConfigDescriptor   Descriptor
	LayerDescriptor    Descriptor
}

type imageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
	Config       Config `json:"config"`
	RootFS       rootFS `json:"rootfs"`
}

type rootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// Compose builds OCI manifest and config metadata from a Studio-controlled
// base and metadata for an exported gzip layer. It never reads layer.Path.
func Compose(base Base, layer templateexport.Layer) (Image, error) {
	if !validPlatform(base.OS, base.Architecture, base.Variant) {
		return Image{}, errors.New("base platform must be supported linux amd64 or arm64")
	}
	if !ValidDigest(base.Digest) {
		return Image{}, errors.New("base digest must be lowercase sha256")
	}
	if len(base.Layers) == 0 {
		return Image{}, errors.New("base must contain at least one layer")
	}
	if len(base.Layers) >= maxImageLayers {
		return Image{}, errors.New("composed image exceeds the layer limit")
	}
	if !ValidDigest(layer.Digest) {
		return Image{}, errors.New("exported layer digest must be lowercase sha256")
	}
	if layer.Size <= 0 {
		return Image{}, errors.New("exported layer size must be positive")
	}
	if layer.UncompressedSize < 0 {
		return Image{}, errors.New("exported layer uncompressed size must not be negative")
	}
	if !ValidDigest(layer.DiffID) {
		return Image{}, errors.New("exported layer diff ID must be lowercase sha256")
	}

	imageLayers := make([]Descriptor, 0, len(base.Layers)+1)
	diffIDs := make([]string, 0, len(base.Layers)+1)
	for _, baseLayer := range base.Layers {
		if !ValidDigest(baseLayer.Digest) {
			return Image{}, errors.New("base layer digest must be lowercase sha256")
		}
		if baseLayer.Size <= 0 {
			return Image{}, errors.New("base layer size must be positive")
		}
		mediaType, ok := ociLayerMediaType(baseLayer.MediaType)
		if !ok {
			return Image{}, errors.New("base layer media type is not a supported distributable layer")
		}
		if !ValidDigest(baseLayer.DiffID) {
			return Image{}, errors.New("base layer diff ID must be lowercase sha256")
		}

		descriptor := baseLayer.Descriptor
		descriptor.MediaType = mediaType
		imageLayers = append(imageLayers, descriptor)
		diffIDs = append(diffIDs, baseLayer.DiffID)
	}

	layerDescriptor := Descriptor{
		MediaType: MediaLayer,
		Digest:    layer.Digest,
		Size:      layer.Size,
	}
	imageLayers = append(imageLayers, layerDescriptor)
	diffIDs = append(diffIDs, layer.DiffID)

	configBytes, err := json.Marshal(imageConfig{
		Architecture: base.Architecture,
		OS:           base.OS,
		Variant:      base.Variant,
		Config:       base.Config,
		RootFS: rootFS{
			Type:    "layers",
			DiffIDs: diffIDs,
		},
	})
	if err != nil {
		return Image{}, errors.New("marshal OCI image config")
	}
	if len(configBytes) > maxJSONSize {
		return Image{}, errors.New("OCI image config exceeds 1 MiB")
	}
	configDescriptor := Descriptor{
		MediaType: MediaConfig,
		Digest:    Digest(configBytes),
		Size:      int64(len(configBytes)),
	}

	manifestBytes, err := json.Marshal(manifest{
		SchemaVersion: 2,
		MediaType:     MediaManifest,
		Config:        configDescriptor,
		Layers:        imageLayers,
	})
	if err != nil {
		return Image{}, errors.New("marshal OCI image manifest")
	}
	if len(manifestBytes) > maxJSONSize {
		return Image{}, errors.New("OCI image manifest exceeds 1 MiB")
	}

	return Image{
		Manifest: manifestBytes,
		Config:   configBytes,
		ManifestDescriptor: Descriptor{
			MediaType: MediaManifest,
			Digest:    Digest(manifestBytes),
			Size:      int64(len(manifestBytes)),
		},
		ConfigDescriptor: configDescriptor,
		LayerDescriptor:  layerDescriptor,
	}, nil
}

// CacheKey hashes exact canonical spec bytes, base digest, platform, and
// exporter version with length framing. The caller canonicalizes the spec;
// environment isolation belongs to the store and is not included in this key.
func CacheKey(spec []byte, baseDigest, platform, exporterVersion string) (string, error) {
	if len(spec) == 0 || len(spec) > maxJSONSize {
		return "", errors.New("canonical spec must be between 1 byte and 1 MiB")
	}
	if !ValidDigest(baseDigest) {
		return "", errors.New("base digest must be lowercase sha256")
	}
	if !validPlatformString(platform) {
		return "", errors.New("platform must be a supported linux amd64 or arm64 platform")
	}
	if exporterVersion == "" {
		return "", errors.New("exporter version is required")
	}

	h := sha256.New()
	_, _ = h.Write([]byte("sandbox-studio-template-image-cache-key-v1\x00"))
	writeFrame(h, spec)
	writeFrame(h, []byte(baseDigest))
	writeFrame(h, []byte(platform))
	writeFrame(h, []byte(exporterVersion))
	return digestSum(h.Sum(nil)), nil
}

// ValidDigest reports whether value is a lowercase sha256 digest in OCI form.
func ValidDigest(value string) bool {
	if len(value) != len(digestPrefix)+sha256.Size*2 || value[:len(digestPrefix)] != digestPrefix {
		return false
	}
	for i := len(digestPrefix); i < len(value); i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Digest returns the lowercase sha256 digest of data in OCI form.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return digestSum(sum[:])
}

func digestSum(sum []byte) string {
	return digestPrefix + hex.EncodeToString(sum)
}

func ociLayerMediaType(mediaType string) (string, bool) {
	switch mediaType {
	case mediaLayerTar, MediaLayer, mediaLayerZstd:
		return mediaType, true
	case mediaDockerLayer:
		return MediaLayer, true
	case mediaDockerLayerTar:
		return mediaLayerTar, true
	default:
		return "", false
	}
}

func validPlatform(os, architecture, variant string) bool {
	if os != "linux" {
		return false
	}
	if variant == "" {
		return architecture == "amd64" || architecture == "arm64"
	}
	if !validSimpleVariant(variant) {
		return false
	}
	switch architecture {
	case "amd64":
		return variant == "v1" || variant == "v2" || variant == "v3" || variant == "v4"
	case "arm64":
		return validARM64Variant(variant)
	default:
		return false
	}
}

func validPlatformString(platform string) bool {
	if len(platform) > 44 {
		return false
	}
	parts := strings.Split(platform, "/")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	variant := ""
	if len(parts) == 3 {
		variant = parts[2]
		if variant == "" {
			return false
		}
	}
	return validPlatform(parts[0], parts[1], variant)
}

func validSimpleVariant(variant string) bool {
	if len(variant) == 0 || len(variant) > 32 {
		return false
	}
	previousSeparator := false
	for i := 0; i < len(variant); i++ {
		c := variant[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			previousSeparator = false
			continue
		}
		if (c == '.' || c == '_' || c == '-') && i > 0 && i < len(variant)-1 && !previousSeparator {
			previousSeparator = true
			continue
		}
		return false
	}
	return true
}

func validARM64Variant(variant string) bool {
	if !strings.HasPrefix(variant, "v") {
		return false
	}
	parts := strings.Split(variant[1:], ".")
	if len(parts) == 0 || len(parts[0]) == 0 || len(parts[0]) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	major, err := strconv.Atoi(parts[0])
	return err == nil && major >= 8 && strconv.Itoa(major) == parts[0]
}

func writeFrame(h hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}
