package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// exportTimeout bounds streaming one export; an upload is bounded by its size limit.
const exportTimeout = 2 * time.Hour

const exportMediaType = "application/octet-stream"

// registerExports adds export and import. Both stream files of up to the workspace size,
// which huma would buffer in memory, so they are plain handlers documented in OpenAPI.
// The mux sits behind Guard and the auth middleware like every other /api route.
func (s *Server) registerExports(api huma.API, mux *http.ServeMux) {
	mux.HandleFunc("GET /api/environments/{env}/sandboxes/{id}/export", s.exportSandbox)
	mux.HandleFunc("POST /api/environments/{env}/sandbox-imports", s.importSandbox)

	binary := map[string]*huma.MediaType{exportMediaType: {Schema: &huma.Schema{Type: "string", Format: "binary"}}}
	problem := func(description string) *huma.Response {
		return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
			"application/problem+json": {Schema: &huma.Schema{Ref: "#/components/schemas/ErrorModel"}},
		}}
	}
	pathParam := func(name, doc string) *huma.Param {
		return &huma.Param{Name: name, In: "path", Required: true, Description: doc, Schema: &huma.Schema{Type: "string"}}
	}
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "exportSandbox", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandboxes/{id}/export", Tags: []string{"sandboxes"},
		Summary: "Download a sandbox as a .studio-sandbox file",
		Description: "Streams the template spec, resources and /workspace. Secrets, network identity, approvals, checkpoints and Docker state are not included. " +
			"A running sandbox is exported while it runs, so files written meanwhile may be inconsistent. A stopped one is started without services and stopped again.",
		Parameters: []*huma.Param{pathParam("env", "Environment ID"), pathParam("id", "Sandbox ID")},
		Responses: map[string]*huma.Response{
			"200":     {Description: "The export file", Content: binary},
			"default": problem("Error"),
		},
	})
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "importSandbox", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandbox-imports", Tags: []string{"sandboxes"},
		Summary: "Upload a .studio-sandbox file",
		Description: "Creates a sandbox from an export in the background; poll the returned import. " +
			"The workspace archive may be at most the exported workspace size. A name already in use gets a numeric suffix.",
		Parameters:  []*huma.Param{pathParam("env", "Environment ID")},
		RequestBody: &huma.RequestBody{Required: true, Content: binary},
		Responses: map[string]*huma.Response{
			"202": {Description: "The import was accepted", Content: map[string]*huma.MediaType{
				"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(store.SandboxImport{}), true, "")},
			}},
			"default": problem("Error"),
		},
	})

	type importIn struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Import ID"`
	}
	type importOut struct{ Body store.SandboxImport }
	huma.Register(api, huma.Operation{
		OperationID: "getSandboxImport", Method: http.MethodGet,
		Path: "/api/environments/{env}/sandbox-imports/{id}", Tags: []string{"sandboxes"},
		Summary: "Get the status of a sandbox import",
	}, func(ctx context.Context, in *importIn) (*importOut, error) {
		imp, err := s.Sandboxes.SandboxImport(ctx, in.Env, in.ID)
		return &importOut{imp}, apiError(err)
	})
	huma.Register(api, huma.Operation{
		OperationID: "cancelSandboxImport", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandbox-imports/{id}/cancel", Tags: []string{"sandboxes"},
		Summary:     "Cancel a sandbox import",
		Description: "Marks the import failed and removes its sandbox if one was created. A template build it started keeps running.",
	}, func(ctx context.Context, in *importIn) (*importOut, error) {
		imp, err := s.Sandboxes.CancelImport(ctx, in.Env, in.ID)
		return &importOut{imp}, apiError(err)
	})
}

func (s *Server) exportSandbox(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	started := false
	err := s.Sandboxes.Export(ctx, r.PathValue("env"), r.PathValue("id"), func(m sandboxes.ExportManifest) (io.Writer, error) {
		filename := m.Name + sandboxes.ExportExtension // ValidName keeps this a safe token
		w.Header().Set("Content-Type", exportMediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		started = true
		return w, nil
	})
	if err == nil {
		return
	}
	if started {
		// The status line is sent; abort the response so the client sees a failed download.
		s.Log.Warn("sandbox export failed", "sandbox", r.PathValue("id"), "err", err)
		panic(http.ErrAbortHandler)
	}
	s.writeProblem(w, "sandbox export failed", transferError(err))
}

func (s *Server) importSandbox(w http.ResponseWriter, r *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != exportMediaType {
		s.writeProblem(w, "", huma.Error415UnsupportedMediaType("the body must be "+exportMediaType))
		return
	}
	imp, err := s.Sandboxes.Import(r.Context(), r.PathValue("env"), r.Body, r.ContentLength)
	if err != nil {
		s.writeProblem(w, "sandbox import failed", transferError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", strings.TrimSuffix(r.URL.Path, "/")+"/"+imp.ID)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(imp)
}

func transferError(err error) error {
	switch {
	case errors.Is(err, sandboxes.ErrInvalidExport):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, sandboxes.ErrWorkspaceTooLarge):
		return huma.NewError(http.StatusRequestEntityTooLarge, "the workspace archive exceeds the exported workspace size")
	case errors.Is(err, sandboxes.ErrInsufficientDisk):
		return huma.NewError(http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, sandboxes.ErrTransferUnsupported), errors.Is(err, sandboxes.ErrBuildsUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	}
	return apiError(err)
}

// writeProblem writes err as problem JSON. Internal errors may carry runtime or host
// details, so they are logged and replaced by a generic message.
func (s *Server) writeProblem(w http.ResponseWriter, what string, err error) {
	status := http.StatusInternalServerError
	var se huma.StatusError
	if errors.As(err, &se) {
		status = se.GetStatus()
	}
	if status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable && status != http.StatusInsufficientStorage {
		s.Log.Warn(what, "err", err)
		err = huma.Error500InternalServerError(what + "; see the Studio log for details")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(err)
}
