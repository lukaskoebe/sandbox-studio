package templateregistry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

type baseLeaseKey struct {
	environmentID  string
	manifestDigest string
}

type baseArtifactLease struct {
	directory    string
	manifest     templateimage.Descriptor
	config       templateimage.Descriptor
	layerDigests []string
	references   int
}

// PrepareBase serves only normalized manifest/config metadata for a configured,
// already-inspected base. Base layers remain in microsandbox's local cache.
func (r *Registry) PrepareBase(ctx context.Context, env string, base templateimage.Base) (reference Reference, release func() error, retErr error) {
	if r == nil || r.st == nil || r.root == nil {
		return Reference{}, nil, errors.New("template registry is unavailable")
	}
	if ctx == nil {
		return Reference{}, nil, errors.New("prepare template base requires a context")
	}
	if err := ctx.Err(); err != nil {
		return Reference{}, nil, err
	}
	if !validID(env) {
		return Reference{}, nil, store.ErrNotFound
	}
	if _, err := r.st.Environment(ctx, env); err != nil {
		return Reference{}, nil, err
	}

	image, err := templateimage.ComposeBase(base)
	if err != nil {
		return Reference{}, nil, fmt.Errorf("compose template base metadata: %w", err)
	}
	if err := validateBaseMetadata(image); err != nil {
		return Reference{}, nil, err
	}
	key := baseLeaseKey{environmentID: env, manifestDigest: image.ManifestDescriptor.Digest}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reference{}, nil, err
	}
	if existing := r.baseLeases[key]; existing != nil {
		if existing.manifest != image.ManifestDescriptor || existing.config != image.ConfigDescriptor {
			return Reference{}, nil, errors.New("leased base metadata does not match its manifest digest")
		}
		existing.references++
		return r.baseReference(env, image.ManifestDescriptor.Digest), r.releaseBase(key, existing), nil
	}

	if err := r.ensureBaseIncoming(env); err != nil {
		return Reference{}, nil, err
	}
	directory, err := r.newBaseLeaseDirectory(env)
	if err != nil {
		return Reference{}, nil, err
	}
	owned := true
	defer func() {
		if owned {
			retErr = errors.Join(retErr, r.removeBaseDirectory(env, directory))
		}
	}()
	if err := r.noSymlinks(directory); err != nil {
		return Reference{}, nil, err
	}
	if err := r.writeFile(filepath.Join(directory, "manifest.json"), image.Manifest); err != nil {
		return Reference{}, nil, err
	}
	if err := r.writeFile(filepath.Join(directory, "config.json"), image.Config); err != nil {
		return Reference{}, nil, err
	}
	if err := r.syncDir(directory); err != nil {
		return Reference{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return Reference{}, nil, err
	}

	layers := make([]string, 0, len(base.Layers))
	for _, layer := range base.Layers {
		layers = append(layers, layer.Digest)
	}
	lease := &baseArtifactLease{
		directory: directory, manifest: image.ManifestDescriptor,
		config: image.ConfigDescriptor, layerDigests: layers, references: 1,
	}
	if r.baseLeases == nil {
		r.baseLeases = make(map[baseLeaseKey]*baseArtifactLease)
	}
	r.baseLeases[key] = lease
	owned = false
	return r.baseReference(env, image.ManifestDescriptor.Digest), r.releaseBase(key, lease), nil
}

func validateBaseMetadata(image templateimage.Image) error {
	if len(image.Manifest) == 0 || len(image.Manifest) > 1<<20 ||
		image.ManifestDescriptor.MediaType != templateimage.MediaManifest ||
		image.ManifestDescriptor.Size != int64(len(image.Manifest)) ||
		image.ManifestDescriptor.Digest != templateimage.Digest(image.Manifest) {
		return errors.New("composed base manifest does not match its descriptor")
	}
	if len(image.Config) == 0 || len(image.Config) > 1<<20 ||
		image.ConfigDescriptor.MediaType != templateimage.MediaConfig ||
		image.ConfigDescriptor.Size != int64(len(image.Config)) ||
		image.ConfigDescriptor.Digest != templateimage.Digest(image.Config) {
		return errors.New("composed base config does not match its descriptor")
	}
	if image.LayerDescriptor != (templateimage.Descriptor{}) {
		return errors.New("base metadata must not include an exported layer")
	}
	return nil
}

func (r *Registry) baseReference(env, digest string) Reference {
	return Reference{
		Image:    r.addr + "/studio/" + env + "/base@" + digest,
		Username: env, Password: r.password(env),
	}
}

func (r *Registry) ensureBaseIncoming(env string) error {
	if err := r.noSymlinks(env); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := r.root.Mkdir(env, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err := r.noSymlinks(env); err != nil {
		return err
	}
	incoming := filepath.Join(env, ".incoming")
	if err := r.root.MkdirAll(incoming, 0o700); err != nil {
		return err
	}
	return r.noSymlinks(incoming)
}

func (r *Registry) newBaseLeaseDirectory(env string) (string, error) {
	for range 4 {
		token := make([]byte, 16)
		if _, err := rand.Read(token); err != nil {
			return "", err
		}
		directory := filepath.Join(env, ".incoming", ".base-"+hex.EncodeToString(token))
		if err := r.root.Mkdir(directory, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", err
		}
		return directory, nil
	}
	return "", errors.New("could not allocate a private template base directory")
}

func (r *Registry) removeBaseDirectory(env, directory string) error {
	if filepath.Dir(directory) != filepath.Join(env, ".incoming") || !validID(env) {
		return errors.New("invalid template base cleanup ownership")
	}
	if err := r.noSymlinks(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := r.root.RemoveAll(directory); err != nil {
		return err
	}
	return r.syncDir(filepath.Dir(directory))
}

func (r *Registry) releaseBase(key baseLeaseKey, lease *baseArtifactLease) func() error {
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if current := r.baseLeases[key]; current != lease {
				return
			}
			if lease.references > 1 {
				lease.references--
				return
			}
			delete(r.baseLeases, key)
			releaseErr = r.removeBaseDirectory(key.environmentID, lease.directory)
		})
		return releaseErr
	}
}

func (r *Registry) openBaseArtifactLocked(env, kind, digest string) (*os.File, templateimage.Descriptor, error) {
	var empty templateimage.Descriptor
	if kind != "manifests" && kind != "blobs" {
		return nil, empty, store.ErrNotFound
	}
	for key, lease := range r.baseLeases {
		if key.environmentID != env {
			continue
		}
		if kind == "manifests" && lease.manifest.Digest == digest {
			return r.openBaseFile(lease.directory, "manifest.json", lease.manifest)
		}
	}
	if kind == "blobs" {
		// Even a digest that is a config in one lease must not expose a layer
		// referenced by another active base in the shared environment repo.
		for key, lease := range r.baseLeases {
			if key.environmentID != env {
				continue
			}
			for _, layerDigest := range lease.layerDigests {
				if layerDigest == digest {
					return nil, empty, store.ErrNotFound
				}
			}
		}
		for key, lease := range r.baseLeases {
			if key.environmentID == env && lease.config.Digest == digest {
				return r.openBaseFile(lease.directory, "config.json", lease.config)
			}
		}
	}
	return nil, empty, store.ErrNotFound
}

func (r *Registry) openBaseFile(directory, name string, descriptor templateimage.Descriptor) (*os.File, templateimage.Descriptor, error) {
	var empty templateimage.Descriptor
	path := filepath.Join(directory, name)
	if err := r.noSymlinks(path); err != nil {
		return nil, empty, err
	}
	file, err := r.root.Open(path)
	if err != nil {
		return nil, empty, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.Size {
		_ = file.Close()
		return nil, empty, errors.New("leased template base artifact is unavailable")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(file, descriptor.Size+1)); err != nil {
		_ = file.Close()
		return nil, empty, err
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != descriptor.Digest {
		_ = file.Close()
		return nil, empty, errors.New("leased template base artifact digest changed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, empty, err
	}
	return file, descriptor, nil
}
