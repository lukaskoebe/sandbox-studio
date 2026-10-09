// Package templateregistry publishes Studio's derived template images through a
// private, read-only OCI registry. Base layers remain in microsandbox's cache.
package templateregistry

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const keySetting = "template-registry-key"
const keyAAD = "sandbox-studio:template-registry-key:v1"

type Sealer interface {
	Seal(plaintext, aad []byte) []byte
	Unseal(sealed, aad []byte) ([]byte, error)
}

// Reference is only for host runtime calls. Credentials must never be serialized
// into the browser API, guest configuration, logs, or OCI image configuration.
type Reference struct {
	Image    string
	Username string `json:"-"`
	Password string `json:"-"`
}

type Registry struct {
	st   *store.Store
	root *os.Root
	dir  string
	addr string
	key  []byte
	mu   sync.Mutex // publish, delete and recovery cannot race file opens
}

// Open initializes a private artifact tree and recovers interrupted operations.
// addr must be the stable loopback listener address used in persisted image refs.
// The caller owns the listener; tests may pass its already allocated ephemeral port.
func Open(ctx context.Context, st *store.Store, dir, addr string, sealer Sealer) (*Registry, error) {
	if st == nil || sealer == nil {
		return nil, errors.New("template registry requires a store and vault")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" || !canonicalLoopbackAddr(addr) {
		return nil, errors.New("template registry requires an allocated IPv4 loopback address")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("template artifact root must be a directory, not a symlink")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("template artifact directory must be private (0700)")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	r := &Registry{st: st, root: root, dir: dir, addr: addr}
	sealed, err := st.Setting(ctx, keySetting)
	if errors.Is(err, store.ErrNotFound) {
		seed := make([]byte, 32)
		if _, err = rand.Read(seed); err == nil {
			sealed, err = st.CreateSetting(ctx, keySetting, sealer.Seal(seed, []byte(keyAAD)))
		}
	}
	if err == nil {
		r.key, err = sealer.Unseal(sealed, []byte(keyAAD))
	}
	if err == nil && len(r.key) != 32 {
		err = errors.New("template registry key has an invalid length")
	}
	if err == nil {
		err = r.Reconcile(ctx)
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) Close() error          { return r.root.Close() }
func (r *Registry) Handler() http.Handler { return &Handler{Backend: r, Addr: r.addr} }

func (r *Registry) password(env string) string {
	h := hmac.New(sha256.New, r.key)
	_, _ = io.WriteString(h, "template-pull:"+env)
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Registry) Authenticate(ctx context.Context, username, password string) (string, error) {
	if !validID(username) || !hmac.Equal([]byte(password), []byte(r.password(username))) {
		return "", errors.New("invalid registry credentials")
	}
	if _, err := r.st.Environment(ctx, username); err != nil {
		return "", err
	}
	return username, nil
}

func (r *Registry) Resolve(ctx context.Context, env, id string) (Reference, error) {
	if !validID(env) || !validID(id) {
		return Reference{}, store.ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.st.Template(ctx, env, id)
	if err != nil {
		return Reference{}, err
	}
	if t.State != store.TemplateStateReady {
		return Reference{}, store.ErrConflict
	}
	if err := r.checkArtifacts(t); err != nil {
		return Reference{}, err
	}
	for _, a := range t.Artifacts {
		if a.Role == "manifest" && templateimage.ValidDigest(a.Digest) {
			return Reference{Image: r.addr + "/studio/" + env + "/" + id + "@" + a.Digest,
				Username: env, Password: r.password(env)}, nil
		}
	}
	return Reference{}, errors.New("template has no manifest")
}

// StagingDir is a private receive directory. Pass it to Hub.Export. Publish takes
// ownership only of artifacts created here; abandoned receives are removed at startup.
func (r *Registry) StagingDir(ctx context.Context, env string) (string, error) {
	if !validID(env) {
		return "", store.ErrNotFound
	}
	if _, err := r.st.Environment(ctx, env); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.noSymlinks(env); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := r.root.MkdirAll(filepath.Join(env, ".incoming"), 0o700); err != nil {
		return "", err
	}
	if err := r.noSymlinks(filepath.Join(env, ".incoming")); err != nil {
		return "", err
	}
	return filepath.Join(r.dir, env, ".incoming"), nil
}

// Publish consumes a validated layer from StagingDir, including on failure or a
// cache hit. The manifest/config and layer become visible together via the ready
// catalog transition. Existing ready cache entries are immutable.
// The host builder must obtain base metadata from its approved, pinned Studio
// base, then use templateimage.Compose. This internal API checks consistency,
// not remote base provenance; it must not accept browser-supplied OCI metadata.
func (r *Registry) Publish(ctx context.Context, in store.Template, image templateimage.Image, layer templateexport.Layer) (out store.Template, retErr error) {
	if !validID(in.EnvironmentID) || filepath.Dir(layer.Path) != filepath.Join(r.dir, in.EnvironmentID, ".incoming") ||
		!strings.HasPrefix(filepath.Base(layer.Path), ".oci-layer-") {
		return out, errors.New("template layer must come from this environment's staging directory")
	}
	source := filepath.Join(in.EnvironmentID, ".incoming", filepath.Base(layer.Path))
	r.mu.Lock()
	defer r.mu.Unlock()
	// Establish ownership before registering cleanup: a symlinked parent must
	// not make us remove a file from another environment, even within root.
	if err := r.noSymlinks(filepath.Dir(source)); err != nil {
		return out, err
	}
	defer func() {
		if err := r.root.Remove(source); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove received template layer: %w", err))
		}
	}()
	if err := verifyImage(in, image, layer); err != nil {
		return out, err
	}
	if err := r.noSymlinks(source); err != nil {
		return out, err
	}
	if existing, err := r.st.TemplateByKey(ctx, in.EnvironmentID, in.CacheKey); err == nil {
		if existing.State != store.TemplateStateReady {
			return out, store.ErrConflict
		}
		if err := r.checkArtifacts(existing); err != nil {
			return out, err
		}
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	out, err := r.st.CreateTemplate(ctx, in)
	if err != nil {
		return out, err
	}
	stage := filepath.Join(out.EnvironmentID, "."+out.ID+".publishing")
	final := filepath.Join(out.EnvironmentID, out.ID)
	defer func() {
		if retErr == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		retErr = errors.Join(retErr, r.remove(cleanup, out.EnvironmentID, out.ID))
	}()
	if err := r.root.Mkdir(stage, 0o700); err != nil {
		return out, err
	}
	for name, data := range map[string][]byte{"manifest.json": image.Manifest, "config.json": image.Config} {
		if err := r.writeFile(filepath.Join(stage, name), data); err != nil {
			return out, err
		}
	}
	if err := r.copyLayer(ctx, source, filepath.Join(stage, "layer.tar.gz"), layer); err != nil {
		return out, err
	}
	if err := r.syncDir(stage); err != nil {
		return out, err
	}
	if _, err := r.root.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		return out, errors.New("template destination already exists or is inaccessible")
	}
	if err := r.root.Rename(stage, final); err != nil {
		return out, err
	}
	if err := r.syncDir(out.EnvironmentID); err != nil {
		return out, err
	}
	artifacts := []store.TemplateArtifact{
		artifact("manifest", image.ManifestDescriptor), artifact("config", image.ConfigDescriptor), artifact("layer", image.LayerDescriptor),
	}
	if err := r.st.ReadyTemplate(ctx, out.EnvironmentID, out.ID, artifacts); err != nil {
		return out, err
	}
	out.State, out.Artifacts = store.TemplateStateReady, artifacts
	return out, nil
}

func artifact(role string, d templateimage.Descriptor) store.TemplateArtifact {
	return store.TemplateArtifact{Role: role, Digest: d.Digest, MediaType: d.MediaType, Size: d.Size}
}

func artifactName(role string) string {
	switch role {
	case "manifest", "config":
		return role + ".json"
	case "layer":
		return "layer.tar.gz"
	default:
		return ""
	}
}

func (r *Registry) OpenArtifact(ctx context.Context, env, id, kind, digest string) (*os.File, templateimage.Descriptor, error) {
	var empty templateimage.Descriptor
	if !validID(env) || !validID(id) || !templateimage.ValidDigest(digest) {
		return nil, empty, store.ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.st.Template(ctx, env, id)
	if err != nil {
		return nil, empty, err
	}
	if t.State != store.TemplateStateReady {
		return nil, empty, store.ErrNotFound
	}
	for _, a := range t.Artifacts {
		allowed := kind == "manifests" && a.Role == "manifest" || kind == "blobs" && (a.Role == "config" || a.Role == "layer")
		if !allowed || a.Digest != digest {
			continue
		}
		name := filepath.Join(env, id, artifactName(a.Role))
		if err := r.noSymlinks(name); err != nil {
			return nil, empty, err
		}
		f, err := r.root.Open(name)
		if err != nil {
			return nil, empty, err
		}
		return f, templateimage.Descriptor{MediaType: a.MediaType, Digest: a.Digest, Size: a.Size}, nil
	}
	return nil, empty, store.ErrNotFound
}

func (r *Registry) Delete(ctx context.Context, env, id string) error {
	if !validID(env) || !validID(id) {
		return store.ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remove(ctx, env, id)
}

func (r *Registry) remove(ctx context.Context, env, id string) error {
	if err := r.noSymlinks(env); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := r.st.SetTemplateDeleting(ctx, env, id); err != nil {
		return err
	}
	for _, name := range []string{id, "." + id + ".publishing"} {
		if err := r.root.RemoveAll(filepath.Join(env, name)); err != nil {
			return err // retain deleting record so startup retries, e.g. an open Windows file
		}
	}
	return r.st.DeleteTemplate(ctx, env, id)
}

// Reconcile runs at startup, before receives or HTTP requests. Incomplete
// publications are discarded rather than presenting a partly committed image.
func (r *Registry) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.st.Templates(ctx, "")
	if err != nil {
		return err
	}
	ready := map[string]bool{}
	for _, t := range all {
		if !validID(t.EnvironmentID) || !validID(t.ID) {
			return errors.New("invalid template catalog identity")
		}
		if t.State != store.TemplateStateReady {
			if err := r.remove(ctx, t.EnvironmentID, t.ID); err != nil {
				return err
			}
		} else {
			if err := r.checkArtifacts(t); err != nil {
				return err
			}
			ready[filepath.Join(t.EnvironmentID, t.ID)] = true
		}
	}
	envs, err := r.entries(".")
	if err != nil {
		return err
	}
	for _, env := range envs {
		if !validID(env.Name()) || !env.IsDir() {
			return errors.New("unexpected entry in private template artifact root")
		}
		entries, err := r.entries(env.Name())
		if err != nil {
			return err
		}
		for _, e := range entries {
			rel := filepath.Join(env.Name(), e.Name())
			if ready[rel] {
				continue
			}
			publishing := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "."), ".publishing")
			if e.Name() != ".incoming" && !validID(e.Name()) && !(strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".publishing") && validID(publishing)) {
				return errors.New("unexpected entry in private template environment directory")
			}
			if err := r.root.RemoveAll(rel); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkArtifacts detects missing/truncated cache objects before issuing a pull
// reference. Same-size corruption is detected by the OCI client's digest check;
// startup must not rehash potentially multi-gigabyte layers. Keep broken ready
// records for explicit repair rather than silently replacing an immutable image.
func (r *Registry) checkArtifacts(t store.Template) error {
	if len(t.Artifacts) != 3 {
		return fmt.Errorf("template %s has incomplete artifact metadata", t.ID)
	}
	seen := map[string]bool{}
	for _, a := range t.Artifacts {
		name := artifactName(a.Role)
		if name == "" || seen[a.Role] || !templateimage.ValidDigest(a.Digest) || a.Size <= 0 {
			return fmt.Errorf("template %s has invalid artifact metadata", t.ID)
		}
		seen[a.Role] = true
		path := filepath.Join(t.EnvironmentID, t.ID, name)
		if err := r.noSymlinks(path); err != nil {
			return fmt.Errorf("template %s artifact %s unavailable: %w", t.ID, a.Role, err)
		}
		info, err := r.root.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != a.Size {
			return fmt.Errorf("template %s artifact %s is missing, truncated or not a regular file", t.ID, a.Role)
		}
	}
	return nil
}

func (r *Registry) entries(name string) ([]os.DirEntry, error) {
	f, err := r.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (r *Registry) noSymlinks(name string) error {
	part := ""
	for _, c := range strings.Split(name, string(filepath.Separator)) {
		part = filepath.Join(part, c)
		info, err := r.root.Lstat(part)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlinks are not permitted in template artifact paths")
		}
	}
	return nil
}

func (r *Registry) writeFile(name string, data []byte) (retErr error) {
	f, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func (r *Registry) copyLayer(ctx context.Context, src, dst string, layer templateexport.Layer) (retErr error) {
	if err := r.noSymlinks(src); err != nil {
		return err
	}
	in, err := r.root.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != layer.Size {
		return errors.New("received layer is not a regular file of its declared size")
	}
	out, err := r.root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	remaining := layer.Size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(in, buf[:min(int64(len(buf)), remaining)])
		if err != nil {
			return err
		}
		if _, err := out.Write(buf[:n]); err != nil {
			return err
		}
		_, _ = h.Write(buf[:n])
		remaining -= int64(n)
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != layer.Digest {
		return errors.New("received layer digest changed before publication")
	}
	return out.Sync()
}

func (r *Registry) syncDir(name string) error {
	// Windows does not support fsync on directory handles. Individual files are
	// flushed before rename; interrupted operations remain recoverable in SQLite.
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := r.root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func verifyImage(in store.Template, image templateimage.Image, layer templateexport.Layer) error {
	key, err := templateimage.CacheKey([]byte(in.Spec), in.BaseDigest, in.Platform, in.ExporterVersion)
	if err != nil || key != in.CacheKey || in.ExporterVersion != templateimage.ExporterVersion {
		return errors.New("template cache identity is invalid")
	}
	for _, blob := range []struct {
		data       []byte
		descriptor templateimage.Descriptor
		media      string
	}{
		{image.Manifest, image.ManifestDescriptor, templateimage.MediaManifest},
		{image.Config, image.ConfigDescriptor, templateimage.MediaConfig},
	} {
		if len(blob.data) == 0 || len(blob.data) > 1<<20 || blob.descriptor.MediaType != blob.media || blob.descriptor.Size != int64(len(blob.data)) || blob.descriptor.Digest != templateimage.Digest(blob.data) {
			return errors.New("template metadata does not match its descriptor")
		}
	}
	if image.LayerDescriptor.MediaType != templateimage.MediaLayer || image.LayerDescriptor.Digest != layer.Digest || image.LayerDescriptor.Size != layer.Size || layer.Size <= 0 || !templateimage.ValidDigest(layer.Digest) || !templateimage.ValidDigest(layer.DiffID) {
		return errors.New("template layer does not match its descriptor")
	}
	var manifest struct {
		SchemaVersion int                        `json:"schemaVersion"`
		MediaType     string                     `json:"mediaType"`
		Config        templateimage.Descriptor   `json:"config"`
		Layers        []templateimage.Descriptor `json:"layers"`
	}
	if err := decode(image.Manifest, &manifest); err != nil || manifest.SchemaVersion != 2 || manifest.MediaType != templateimage.MediaManifest || manifest.Config != image.ConfigDescriptor || len(manifest.Layers) < 2 || len(manifest.Layers) > 256 || manifest.Layers[len(manifest.Layers)-1] != image.LayerDescriptor {
		return errors.New("template manifest does not describe the published artifacts")
	}
	var config struct {
		Architecture string               `json:"architecture"`
		OS           string               `json:"os"`
		Variant      string               `json:"variant,omitempty"`
		Config       templateimage.Config `json:"config"`
		RootFS       struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := decode(image.Config, &config); err != nil || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) || config.RootFS.DiffIDs[len(config.RootFS.DiffIDs)-1] != layer.DiffID {
		return errors.New("template config does not describe the published filesystem")
	}
	platform := config.OS + "/" + config.Architecture
	if config.Variant != "" {
		platform += "/" + config.Variant
	}
	if platform != in.Platform {
		return errors.New("template config does not match its platform")
	}
	for i, d := range manifest.Layers {
		mediaOK := d.MediaType == templateimage.MediaLayer || d.MediaType == "application/vnd.oci.image.layer.v1.tar" || d.MediaType == "application/vnd.oci.image.layer.v1.tar+zstd"
		if !mediaOK || !templateimage.ValidDigest(d.Digest) || d.Size <= 0 || !templateimage.ValidDigest(config.RootFS.DiffIDs[i]) {
			return errors.New("invalid template base layer metadata")
		}
	}
	return nil
}

func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
