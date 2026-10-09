package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

func createReadyAPITemplate(t *testing.T, st *store.Store, envID string) store.Template {
	t.Helper()
	encoded, err := templatespec.CanonicalJSON(templatespec.Spec{
		Resources: resources.Resources{CPUs: 3, MemoryMiB: 3072, MaxMemoryMiB: 4096, WorkspaceMiB: 8192, DockerMiB: 12288},
		Tools:     []templatespec.ToolPin{}, Apt: []string{}, Setup: "template-setup-must-not-be-public",
	})
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	baseDigest := "sha256:" + strings.Repeat("a", 64)
	platform := "linux/" + goruntime.GOARCH
	cacheKey, err := templateimage.CacheKey(encoded, baseDigest, platform, templateimage.ExporterVersion)
	if err != nil {
		t.Fatalf("CacheKey: %v", err)
	}
	template, err := st.CreateTemplate(context.Background(), store.Template{
		EnvironmentID: envID, CacheKey: cacheKey, Spec: string(encoded), BaseRef: "private-base-reference",
		BaseDigest: baseDigest, Platform: platform, ExporterVersion: templateimage.ExporterVersion,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	artifacts := []store.TemplateArtifact{
		{Role: "manifest", Digest: "sha256:" + strings.Repeat("b", 64), MediaType: templateimage.MediaManifest, Size: 1},
		{Role: "config", Digest: "sha256:" + strings.Repeat("c", 64), MediaType: templateimage.MediaConfig, Size: 1},
		{Role: "layer", Digest: "sha256:" + strings.Repeat("d", 64), MediaType: templateimage.MediaLayer, Size: 1},
	}
	if err := st.ReadyTemplate(context.Background(), envID, template.ID, artifacts); err != nil {
		t.Fatalf("ReadyTemplate: %v", err)
	}
	template, err = st.Template(context.Background(), envID, template.ID)
	if err != nil {
		t.Fatalf("read ready template: %v", err)
	}
	return template
}

func TestTemplateAPIExposesOnlyReadyScopedPublicFields(t *testing.T) {
	h, s := newTestServer(t)
	env, err := s.Store.CreateEnvironment(context.Background(), "templates")
	if err != nil {
		t.Fatal(err)
	}
	ready := createReadyAPITemplate(t, s.Store, env.ID)
	creating, err := s.Store.CreateTemplate(context.Background(), store.Template{
		EnvironmentID: env.ID, CacheKey: "sha256:" + strings.Repeat("f", 64), Spec: `{"formatVersion":1}`,
		BaseRef: "another-private-base", BaseDigest: "sha256:" + strings.Repeat("e", 64),
		Platform: "linux/" + goruntime.GOARCH, ExporterVersion: templateimage.ExporterVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Store.CreateEnvironment(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	otherTemplate := createReadyAPITemplate(t, s.Store, other.ID)

	list := do(h, http.MethodGet, "http://localhost:7878/api/environments/"+env.ID+"/templates", "")
	var views []TemplateView
	if err := json.Unmarshal(list.Body.Bytes(), &views); err != nil || list.Code != http.StatusOK {
		t.Fatalf("list templates: status=%d body=%s err=%v", list.Code, list.Body, err)
	}
	if len(views) != 1 || views[0].ID != ready.ID || views[0].EnvironmentID != env.ID || views[0].Platform != ready.Platform ||
		views[0].Resources.CPUs != 3 || views[0].Resources.DockerMiB != 12288 {
		t.Fatalf("template list = %+v", views)
	}
	for _, private := range []string{"template-setup-must-not-be-public", "private-base-reference", "cacheKey", "artifacts", ready.Artifacts[0].Digest} {
		if strings.Contains(list.Body.String(), private) {
			t.Errorf("template list exposed private field %q: %s", private, list.Body)
		}
	}

	detail := do(h, http.MethodGet, "http://localhost:7878/api/environments/"+env.ID+"/templates/"+ready.ID, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), ready.ID) {
		t.Fatalf("template detail: status=%d body=%s", detail.Code, detail.Body)
	}
	if got := do(h, http.MethodGet, "http://localhost:7878/api/environments/"+other.ID+"/templates/"+ready.ID, ""); got.Code != http.StatusNotFound {
		t.Errorf("cross-environment template detail status=%d body=%s", got.Code, got.Body)
	}
	if got := do(h, http.MethodGet, "http://localhost:7878/api/environments/"+env.ID+"/templates/"+creating.ID, ""); got.Code != http.StatusConflict {
		t.Errorf("not-ready template detail status=%d body=%s", got.Code, got.Body)
	}
	if got := do(h, http.MethodGet, "http://localhost:7878/api/environments/missing/templates", ""); got.Code != http.StatusNotFound {
		t.Errorf("unknown environment template list status=%d body=%s", got.Code, got.Body)
	}
	if otherTemplate.ID == ready.ID {
		t.Fatal("template IDs unexpectedly overlap across environments")
	}
}

type apiTemplateResolver struct {
	reference templateregistry.Reference
	err       error
}

func (r apiTemplateResolver) Resolve(context.Context, string, string) (templateregistry.Reference, error) {
	return r.reference, r.err
}

type apiTemplateRuntime struct {
	sandboxes.SandboxRuntime
	image  *runtime.ImageSource
	labels map[string]string
}

func (r *apiTemplateRuntime) Create(_ context.Context, _ string, spec runtime.Spec, _ string, labels map[string]string) error {
	r.image = spec.Image
	r.labels = labels
	return nil
}

func (*apiTemplateRuntime) Status(context.Context, string) (runtime.Status, error) {
	return runtime.StatusRunning, nil
}

func (*apiTemplateRuntime) CheckpointRestoreSupported() bool { return false }

type apiTemplateEgress struct{}

func (apiTemplateEgress) Attach(store.Sandbox) (runtime.Egress, error) {
	return runtime.Egress{Nameserver: "127.0.0.1:53000", Proxy: "127.0.0.1:7879", User: "env", PasswordEnv: "STUDIO_GW_env"}, nil
}

func (apiTemplateEgress) Detach(string) {}

func TestCreateTemplateSandboxReturnsNormalSandboxWithoutRegistryMetadata(t *testing.T) {
	h, s := newTestServer(t)
	env, err := s.Store.CreateEnvironment(context.Background(), "launch-template")
	if err != nil {
		t.Fatal(err)
	}
	template := createReadyAPITemplate(t, s.Store, env.ID)
	dataDir, err := os.MkdirTemp("", "ss-ti-")
	if err != nil {
		t.Fatal(err)
	}
	var hub *agentchan.Hub
	var rt *apiTemplateRuntime
	t.Cleanup(func() {
		if hub != nil && rt != nil && rt.labels != nil {
			hub.Close(rt.labels["studio.sandbox-id"])
		}
		_ = os.RemoveAll(dataDir)
	})
	path := paths.Paths{Data: dataDir}
	if err := path.Ensure(); err != nil {
		t.Fatal(err)
	}
	hub = agentchan.NewHub(s.Log)
	rt = &apiTemplateRuntime{}
	secret := "private-registry-password"
	image := "127.0.0.1:7880/studio/" + env.ID + "/" + template.ID + "@sha256:" + strings.Repeat("e", 64)
	s.Sandboxes = &sandboxes.Manager{
		Store: s.Store, Runtime: rt, Templates: apiTemplateResolver{reference: templateregistry.Reference{
			Image: image, Username: env.ID, Password: secret,
		}},
		Hub: hub, Egress: apiTemplateEgress{}, Paths: path, Log: s.Log,
	}

	response := do(h, http.MethodPost, "http://localhost:7878/api/environments/"+env.ID+"/templates/"+template.ID+"/sandboxes", `{"name":"fresh-copy"}`)
	var view sandboxes.View
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || response.Code != http.StatusCreated {
		t.Fatalf("create template sandbox: status=%d body=%s err=%v", response.Code, response.Body, err)
	}
	if view.TemplateID != template.ID || view.Name != "fresh-copy" || view.ID == "" || rt.image == nil || rt.image.Reference != image || rt.labels["studio.template-id"] != template.ID {
		t.Fatalf("created template sandbox = %+v runtime image=%+v labels=%+v", view, rt.image, rt.labels)
	}
	for _, private := range []string{secret, image, "template-setup-must-not-be-public", "BaseRef", "cacheKey", "artifacts"} {
		if strings.Contains(response.Body.String(), private) {
			t.Errorf("create response exposed private template metadata %q: %s", private, response.Body)
		}
	}
}

func TestTemplateAPIProviderErrorsUseSafeMessages(t *testing.T) {
	secret := "canary-registry-password"
	image := "127.0.0.1:7880/studio/canary/template@sha256:" + strings.Repeat("e", 64)
	detail := "provider rejected password=" + secret + " image=" + image
	for _, tc := range []struct {
		name       string
		cause      error
		wantStatus int
	}{
		{name: "not found", cause: store.ErrNotFound, wantStatus: http.StatusNotFound},
		{name: "conflict", cause: store.ErrConflict, wantStatus: http.StatusConflict},
		{name: "internal", cause: errors.New("provider rejected request"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s := newTestServer(t)
			env, err := s.Store.CreateEnvironment(context.Background(), "provider-error")
			if err != nil {
				t.Fatal(err)
			}
			template := createReadyAPITemplate(t, s.Store, env.ID)
			providerErr := fmt.Errorf("%s: %w", detail, tc.cause)
			s.Sandboxes = &sandboxes.Manager{Store: s.Store, Templates: apiTemplateResolver{
				reference: templateregistry.Reference{Image: image, Username: env.ID, Password: secret},
				err:       providerErr,
			}}
			response := do(h, http.MethodPost, "http://localhost:7878/api/environments/"+env.ID+"/templates/"+template.ID+"/sandboxes", `{"name":"redaction-case"}`)
			if response.Code != tc.wantStatus {
				t.Fatalf("provider error status=%d body=%s, want %d", response.Code, response.Body, tc.wantStatus)
			}
			if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), image) || strings.Contains(response.Body.String(), detail) {
				t.Fatalf("provider error leaked private details: %s", response.Body)
			}
		})
	}

	unknown := templateAPIError(errors.New(detail))
	if unknown == nil || strings.Contains(unknown.Error(), secret) || strings.Contains(unknown.Error(), image) || strings.Contains(unknown.Error(), detail) {
		t.Fatalf("template-specific internal error exposed provider details: %v", unknown)
	}
}
