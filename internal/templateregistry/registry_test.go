package templateregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateexport"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	_ "modernc.org/sqlite"
)

const registryTestAddress = "127.0.0.1:51889"

type boundTestSealer struct{}

func (boundTestSealer) Seal(plain, aad []byte) []byte {
	sealed := append(append([]byte(nil), aad...), 0)
	return append(sealed, plain...)
}

func (boundTestSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	prefix := append(append([]byte(nil), aad...), 0)
	if !bytes.HasPrefix(sealed, prefix) {
		return nil, errors.New("test sealed value has wrong AAD")
	}
	return append([]byte(nil), sealed[len(prefix):]...), nil
}

type registryFixture struct {
	ctx    context.Context
	st     *store.Store
	reg    *Registry
	root   string
	dbPath string
	env    store.Environment
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	f := &registryFixture{ctx: context.Background(), root: filepath.Join(t.TempDir(), "registry"), dbPath: filepath.Join(t.TempDir(), "catalog.db")}
	var err error
	f.st, err = store.Open(f.ctx, f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.Close() })
	f.env, err = f.st.CreateEnvironment(f.ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	f.reg, err = Open(f.ctx, f.st, f.root, registryTestAddress, boundTestSealer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.reg != nil {
			_ = f.reg.Close()
		}
	})
	return f
}

const registryCanonicalSpec = `{"schema":"sandbox-studio/v1","cpus":2}`

func registryBase() templateimage.Base {
	baseDigest := templateimage.Digest([]byte("controlled base manifest"))
	return templateimage.Base{
		Reference: "registry.example/base:stable", Digest: baseDigest,
		OS: "linux", Architecture: "amd64", Config: templateimage.Config{Env: []string{"PATH=/usr/bin"}},
		Layers: []templateimage.BaseLayer{{Descriptor: templateimage.Descriptor{
			MediaType: templateimage.MediaLayer, Digest: templateimage.Digest([]byte("controlled base layer")), Size: 4096,
		}, DiffID: templateimage.Digest([]byte("controlled base diff"))}},
	}
}

func registryTemplate(env, spec string, base templateimage.Base) store.Template {
	platform := base.OS + "/" + base.Architecture
	if base.Variant != "" {
		platform += "/" + base.Variant
	}
	key, _ := templateimage.CacheKey([]byte(spec), base.Digest, platform, templateimage.ExporterVersion)
	return store.Template{EnvironmentID: env, CacheKey: key, Spec: spec, BaseRef: base.Reference,
		BaseDigest: base.Digest, Platform: platform, ExporterVersion: templateimage.ExporterVersion}
}

func registryBundle(t *testing.T, f *registryFixture, env, contents string) (store.Template, templateimage.Base, templateimage.Image, templateexport.Layer, string) {
	t.Helper()
	stage, err := f.reg.StagingDir(f.ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	payload := []byte(contents)
	if err := tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o600, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if _, err := agentproto.WriteExport(f.ctx, &wire, bytes.NewReader(raw.Bytes()), int64(raw.Len()+1)); err != nil {
		t.Fatal(err)
	}
	layer, err := templateexport.Receive(f.ctx, stage, io.NopCloser(bytes.NewReader(wire.Bytes())), ocilayer.Limits{MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	base := registryBase()
	image, err := templateimage.Compose(base, layer)
	if err != nil {
		t.Fatal(err)
	}
	return registryTemplate(env, registryCanonicalSpec, base), base, image, layer, stage
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected %q removed, Lstat error = %v", path, err)
	}
}

func TestRegistryPublishCacheHitReopenAndScope(t *testing.T) {
	f := newRegistryFixture(t)
	other, err := f.st.CreateEnvironment(f.ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	in, _, image, layer, _ := registryBundle(t, f, f.env.ID, "first filesystem")
	first, err := f.reg.Publish(f.ctx, in, image, layer)
	if err != nil || first.State != store.TemplateStateReady || len(first.Artifacts) != 3 {
		t.Fatalf("Publish = %+v, %v", first, err)
	}
	assertMissing(t, layer.Path)
	ref, err := f.reg.Resolve(f.ctx, f.env.ID, first.ID)
	if err != nil || ref.Username != f.env.ID || ref.Password == "" {
		t.Fatalf("Resolve = %+v, %v", ref, err)
	}
	if env, err := f.reg.Authenticate(f.ctx, ref.Username, ref.Password); err != nil || env != f.env.ID {
		t.Fatalf("Authenticate = %q, %v", env, err)
	}
	if _, err := f.reg.Authenticate(f.ctx, other.ID, ref.Password); err == nil {
		t.Fatal("accepted credentials under another environment")
	}
	if _, err := f.reg.Resolve(f.ctx, other.ID, first.ID); err == nil {
		t.Fatal("resolved a template through another environment")
	}
	file, _, err := f.reg.OpenArtifact(f.ctx, other.ID, first.ID, "manifests", first.Artifacts[0].Digest)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("opened an artifact through another environment")
	}

	secondIn, _, secondImage, secondLayer, _ := registryBundle(t, f, f.env.ID, "different filesystem")
	if secondIn.CacheKey != in.CacheKey {
		t.Fatal("fixture changed the canonical cache key")
	}
	second, err := f.reg.Publish(f.ctx, secondIn, secondImage, secondLayer)
	if err != nil || second.ID != first.ID || !reflect.DeepEqual(second.Artifacts, first.Artifacts) {
		t.Fatalf("cache hit changed the published template: %+v, %v", second, err)
	}
	assertMissing(t, secondLayer.Path)
	refAgain, err := f.reg.Resolve(f.ctx, f.env.ID, first.ID)
	if err != nil || refAgain != ref {
		t.Fatalf("cache hit changed reference credentials: %+v, %v", refAgain, err)
	}

	if err := f.reg.Close(); err != nil {
		t.Fatal(err)
	}
	f.reg, err = Open(f.ctx, f.st, f.root, registryTestAddress, boundTestSealer{})
	if err != nil {
		t.Fatal(err)
	}
	refAfterReopen, err := f.reg.Resolve(f.ctx, f.env.ID, first.ID)
	if err != nil || refAfterReopen != ref {
		t.Fatalf("reopened registry changed reference: %+v, %v", refAfterReopen, err)
	}
	if env, err := f.reg.Authenticate(f.ctx, ref.Username, ref.Password); err != nil || env != f.env.ID {
		t.Fatalf("credentials did not survive reopen: %q, %v", env, err)
	}
}

func TestRegistryRejectsTamperedAndUnownedLayers(t *testing.T) {
	f := newRegistryFixture(t)
	in, _, image, layer, stage := registryBundle(t, f, f.env.ID, "tamper me")
	blob, err := os.ReadFile(layer.Path)
	if err != nil {
		t.Fatal(err)
	}
	blob[0] ^= 0xff
	if err := os.WriteFile(layer.Path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := f.reg.Publish(f.ctx, in, image, layer)
	if err == nil {
		t.Fatal("published a tampered compressed layer")
	}
	if templates, err := f.st.Templates(f.ctx, f.env.ID); err != nil || len(templates) != 0 {
		t.Fatalf("failed publication left catalog rows %+v: %v", templates, err)
	}
	assertMissing(t, layer.Path)
	assertMissing(t, filepath.Join(f.root, f.env.ID, failed.ID))
	assertMissing(t, filepath.Join(f.root, f.env.ID, "."+failed.ID+".publishing"))
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatalf("failed publication left staging entries %v: %v", entries, err)
	}

	for _, mismatch := range []string{"manifest", "config"} {
		t.Run(mismatch+" descriptor", func(t *testing.T) {
			in, _, image, layer, _ := registryBundle(t, f, f.env.ID, mismatch)
			if mismatch == "manifest" {
				image.ManifestDescriptor.Digest = templateimage.Digest([]byte("wrong manifest"))
			} else {
				image.ConfigDescriptor.Size++
			}
			if _, err := f.reg.Publish(f.ctx, in, image, layer); err == nil {
				t.Fatalf("published mismatched %s descriptor", mismatch)
			}
			assertMissing(t, layer.Path)
			if templates, err := f.st.Templates(f.ctx, f.env.ID); err != nil || len(templates) != 0 {
				t.Fatalf("descriptor failure left templates %+v: %v", templates, err)
			}
		})
	}

	in, _, image, layer, _ = registryBundle(t, f, f.env.ID, "owned receive")
	outside := filepath.Join(t.TempDir(), "outside.tar.gz")
	want := []byte("outside file must survive")
	if err := os.WriteFile(outside, want, 0o600); err != nil {
		t.Fatal(err)
	}
	layer.Path = outside
	if _, err := f.reg.Publish(f.ctx, in, image, layer); err == nil {
		t.Fatal("accepted a layer outside the staging directory")
	}
	if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("outside file was consumed or changed: %q, %v", got, err)
	}
}

func TestRegistryRecoveryAndDeletedEnvironmentCleanup(t *testing.T) {
	f := newRegistryFixture(t)
	in, _, image, layer, _ := registryBundle(t, f, f.env.ID, "ready image")
	ready, err := f.reg.Publish(f.ctx, in, image, layer)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := f.reg.Resolve(f.ctx, f.env.ID, ready.ID)
	if err != nil {
		t.Fatal(err)
	}

	creating, err := f.st.CreateTemplate(f.ctx, registryTemplate(f.env.ID, `{"incomplete":true}`, registryBase()))
	if err != nil {
		t.Fatal(err)
	}
	deleting, err := f.st.CreateTemplate(f.ctx, registryTemplate(f.env.ID, `{"deleting":true}`, registryBase()))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetTemplateDeleting(f.ctx, f.env.ID, deleting.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{creating.ID, deleting.ID} {
		for _, name := range []string{id, "." + id + ".publishing"} {
			path := filepath.Join(f.root, f.env.ID, name)
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "partial"), []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	stage, err := f.reg.StagingDir(f.ctx, f.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, ".abandoned-receive"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if templates, err := f.st.Templates(f.ctx, f.env.ID); err != nil || len(templates) != 1 || templates[0].ID != ready.ID {
		t.Fatalf("recovery retained wrong catalog rows %+v: %v", templates, err)
	}
	assertMissing(t, stage)
	for _, id := range []string{creating.ID, deleting.ID} {
		assertMissing(t, filepath.Join(f.root, f.env.ID, id))
		assertMissing(t, filepath.Join(f.root, f.env.ID, "."+id+".publishing"))
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(f.dbPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(f.ctx, "DELETE FROM environments WHERE id = ?", f.env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.Authenticate(f.ctx, ref.Username, ref.Password); err == nil {
		t.Fatal("authenticated a deleted environment")
	}
	if err := f.reg.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(f.root, f.env.ID, ready.ID))
	if templates, err := f.st.Templates(f.ctx, ""); err != nil || len(templates) != 0 {
		t.Fatalf("environment cascade left catalog rows %+v: %v", templates, err)
	}
}

func TestRegistryRefusesSymlinkArtifact(t *testing.T) {
	f := newRegistryFixture(t)
	in, _, image, layer, _ := registryBundle(t, f, f.env.ID, "symlink check")
	tmpl, err := f.reg.Publish(f.ctx, in, image, layer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, f.env.ID, tmpl.ID, "manifest.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	file, _, err := f.reg.OpenArtifact(f.ctx, f.env.ID, tmpl.ID, "manifests", tmpl.Artifacts[0].Digest)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("opened a symlinked template artifact")
	}
}

func TestRegistryRejectsBrokenReadyArtifacts(t *testing.T) {
	for _, mode := range []string{"missing", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			f := newRegistryFixture(t)
			in, _, image, layer, _ := registryBundle(t, f, f.env.ID, "ready artifact")
			ready, err := f.reg.Publish(f.ctx, in, image, layer)
			if err != nil {
				t.Fatal(err)
			}
			artifactPath := filepath.Join(f.root, f.env.ID, ready.ID, "manifest.json")
			switch mode {
			case "missing":
				if err := os.Remove(artifactPath); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				info, err := os.Stat(artifactPath)
				if err != nil {
					t.Fatal(err)
				}
				if info.Size() < 2 {
					t.Fatalf("manifest unexpectedly small: %d bytes", info.Size())
				}
				if err := os.Truncate(artifactPath, info.Size()-1); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := f.reg.Resolve(f.ctx, f.env.ID, ready.ID); err == nil {
				t.Fatal("Resolve succeeded with a broken ready artifact")
			}

			cacheIn, _, cacheImage, cacheLayer, _ := registryBundle(t, f, f.env.ID, "cache hit")
			if _, err := f.reg.Publish(f.ctx, cacheIn, cacheImage, cacheLayer); err == nil {
				t.Fatal("cache hit succeeded with a broken ready artifact")
			}
			assertMissing(t, cacheLayer.Path)

			if err := f.reg.Reconcile(f.ctx); err == nil {
				t.Fatal("Reconcile succeeded with a broken ready artifact")
			}
			stored, err := f.st.Template(f.ctx, f.env.ID, ready.ID)
			if err != nil || stored.State != store.TemplateStateReady {
				t.Fatalf("broken ready record was discarded or changed: %+v, %v", stored, err)
			}

			if err := f.reg.Delete(f.ctx, f.env.ID, ready.ID); err != nil {
				t.Fatalf("explicit Delete repair: %v", err)
			}
			assertMissing(t, filepath.Join(f.root, f.env.ID, ready.ID))
			if templates, err := f.st.Templates(f.ctx, f.env.ID); err != nil || len(templates) != 0 {
				t.Fatalf("explicit Delete left catalog rows %+v: %v", templates, err)
			}
		})
	}
}

func TestRegistryArm64V8ComposePublishResolve(t *testing.T) {
	f := newRegistryFixture(t)
	_, base, _, layer, _ := registryBundle(t, f, f.env.ID, "arm64 image")
	base.Architecture = "arm64"
	base.Variant = "v8"
	image, err := templateimage.Compose(base, layer)
	if err != nil {
		t.Fatalf("Compose arm64/v8: %v", err)
	}
	in := registryTemplate(f.env.ID, registryCanonicalSpec, base)
	if in.Platform != "linux/arm64/v8" {
		t.Fatalf("fixture platform = %q", in.Platform)
	}
	ready, err := f.reg.Publish(f.ctx, in, image, layer)
	if err != nil || ready.State != store.TemplateStateReady {
		t.Fatalf("Publish arm64/v8 = %+v, %v", ready, err)
	}
	ref, err := f.reg.Resolve(f.ctx, f.env.ID, ready.ID)
	if err != nil || ref.Image == "" {
		t.Fatalf("Resolve arm64/v8 = %+v, %v", ref, err)
	}
}

func TestRegistryRejectsSymlinkedIncomingParentWithoutConsumingOtherEnvironmentFile(t *testing.T) {
	f := newRegistryFixture(t)
	other, err := f.st.CreateEnvironment(f.ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	in, _, image, ownedLayer, ownedStage := registryBundle(t, f, f.env.ID, "owned staging file")
	if err := os.Remove(ownedLayer.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ownedStage); err != nil {
		t.Fatal(err)
	}
	_, _, _, otherLayer, _ := registryBundle(t, f, other.ID, "other environment file")
	otherBytes, err := os.ReadFile(otherLayer.Path)
	if err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(f.root, f.env.ID, ".incoming")
	if err := os.Symlink(filepath.Dir(otherLayer.Path), incoming); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ownedLayer.Path = filepath.Join(incoming, filepath.Base(otherLayer.Path))
	if _, err := f.reg.Publish(f.ctx, in, image, ownedLayer); err == nil {
		t.Fatal("accepted a symlinked incoming parent")
	}
	if got, err := os.ReadFile(otherLayer.Path); err != nil || !bytes.Equal(got, otherBytes) {
		t.Fatalf("rejected publish consumed another environment's file: %v", err)
	}
}
