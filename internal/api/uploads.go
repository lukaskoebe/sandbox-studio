package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// uploadTimeout bounds one upload; the size limit bounds how much it moves.
const uploadTimeout = 30 * time.Minute

// UploadedFile is a file an upload wrote into a sandbox's workspace.
type UploadedFile struct {
	Path string `json:"path" doc:"The file's path, relative to /workspace"`
	Name string `json:"name" doc:"The name the file was written under; under conflict keep it can differ from the requested name"`
	Size int64  `json:"size"`
}

// registerUploads adds the upload of a file into a folder of a sandbox's workspace. A file can
// be up to 100 MiB, which huma would buffer, so it is a plain handler documented in OpenAPI, as
// the exports are.
func (s *Server) registerUploads(api huma.API, mux *http.ServeMux) {
	mux.HandleFunc("POST /api/environments/{env}/sandboxes/{id}/files", s.uploadFile)

	binary := map[string]*huma.MediaType{exportMediaType: {Schema: &huma.Schema{Type: "string", Format: "binary"}}}
	problem := &huma.Response{Description: "Error", Content: map[string]*huma.MediaType{
		"application/problem+json": {Schema: &huma.Schema{Ref: "#/components/schemas/ErrorModel"}},
	}}
	param := func(name, in, doc string, required bool) *huma.Param {
		return &huma.Param{Name: name, In: in, Required: required, Description: doc, Schema: &huma.Schema{Type: "string"}}
	}
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "uploadFile", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/files", Tags: []string{"chats"},
		Summary: "Upload a file into a folder of the workspace",
		Description: "Writes the body as the file name in the folder path (relative to /workspace; empty is /workspace itself). " +
			"A file is at most 100 MiB and is owned by the agent user, mode 0644. Its name must be one file name. " +
			"conflict decides a name already taken: fail (the default) answers 409 and writes nothing, replace overwrites a regular file, " +
			"and keep writes it as \"name (2).ext\", the next free name. A folder or a link under the name is never replaced. " +
			"The upload is written under a hidden name and renamed into place, so readers never see half a file.",
		Parameters: []*huma.Param{
			param("env", "path", "Environment ID", true),
			param("id", "path", "Sandbox ID", true),
			param("path", "query", "Folder, relative to /workspace; empty for /workspace itself", false),
			param("name", "query", "The file's name", true),
			{Name: "conflict", In: "query", Description: "What to do when the name is taken", Schema: &huma.Schema{Type: "string", Enum: []any{"fail", "replace", "keep"}}},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: binary},
		Responses: map[string]*huma.Response{
			"201": {Description: "The file was written", Content: map[string]*huma.MediaType{
				"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(UploadedFile{}), true, "")},
			}},
			"default": problem,
		},
	})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != exportMediaType {
		s.writeProblem(w, "", huma.Error415UnsupportedMediaType("the body must be "+exportMediaType))
		return
	}
	switch {
	case r.ContentLength < 0:
		s.writeProblem(w, "", huma.NewError(http.StatusLengthRequired, "the upload needs a Content-Length"))
		return
	case r.ContentLength > agentproto.MaxUploadBytes:
		s.writeProblem(w, "", huma.NewError(http.StatusRequestEntityTooLarge, fmt.Sprintf("a file is at most %d MiB", agentproto.MaxUploadBytes>>20)))
		return
	}
	q := r.URL.Query()
	req := agentproto.UploadRequest{Dir: q.Get("path"), Name: q.Get("name"), Size: r.ContentLength, Conflict: q.Get("conflict")}
	ctx, cancel := context.WithTimeout(r.Context(), uploadTimeout)
	defer cancel()
	reply, err := s.workspaceUpload(ctx, r.PathValue("env"), r.PathValue("id"), req, r.Body)
	if err != nil {
		s.writeProblem(w, "upload failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(UploadedFile{Path: path.Join(req.Dir, reply.Name), Name: reply.Name, Size: reply.Size})
}

// workspaceUpload streams a file to the guest. Its refusals (a taken name, a bad name or folder)
// are the caller's mistakes.
func (s *Server) workspaceUpload(ctx context.Context, envID, id string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	chats, err := s.chats()
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	if _, err := s.sessionSandbox(ctx, envID, id); err != nil {
		return agentproto.UploadReply{}, err
	}
	reply, err := chats.Upload(ctx, id, req, body)
	var taken agentchan.NameTakenError
	switch {
	case err == nil:
		return reply, nil
	case errors.As(err, &taken):
		return reply, huma.Error409Conflict(err.Error())
	case errors.Is(err, agentchan.ErrNotConnected), ctx.Err() != nil:
		return reply, apiError(err)
	}
	return reply, huma.Error422UnprocessableEntity(err.Error())
}
