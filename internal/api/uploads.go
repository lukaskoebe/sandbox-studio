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

// PastedFile is a file a terminal paste wrote into the sandbox, outside its workspace.
type PastedFile struct {
	Path string `json:"path" doc:"The file's absolute path in the sandbox, for the terminal to paste"`
	Size int64  `json:"size"`
}

// registerUploads adds the upload of a file into a folder of a sandbox's workspace, and of a
// file pasted into a terminal. A file can be up to 100 MiB, which huma would buffer, so they are
// plain handlers documented in OpenAPI, as the exports are.
func (s *Server) registerUploads(api huma.API, mux *http.ServeMux) {
	mux.HandleFunc("POST /api/environments/{env}/sandboxes/{id}/files", s.uploadFile)
	mux.HandleFunc("POST /api/environments/{env}/sandboxes/{id}/pastes", s.pasteFile)

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
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "pasteFile", Method: http.MethodPost,
		Path: "/api/environments/{env}/sandboxes/{id}/pastes", Tags: []string{"sandboxes"},
		Summary: "Upload a file pasted into a terminal",
		Description: "Writes the body as the file name in " + agentproto.PasteDir + ", outside the workspace, for a terminal to paste the path: " +
			"an image pasted into a sandbox's terminal reaches an agent, such as Claude Code, which reads the image from the path. " +
			"A taken name is kept as \"name (2).ext\". The folder is in /tmp, so a reboot of the sandbox empties it. " +
			"A sandbox whose guest agent predates pastes answers 422 until it restarts.",
		Parameters: []*huma.Param{
			param("env", "path", "Environment ID", true),
			param("id", "path", "Sandbox ID", true),
			param("name", "query", "The file's name", true),
		},
		RequestBody: &huma.RequestBody{Required: true, Content: binary},
		Responses: map[string]*huma.Response{
			"201": {Description: "The file was written", Content: map[string]*huma.MediaType{
				"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(PastedFile{}), true, "")},
			}},
			"default": problem,
		},
	})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := agentproto.UploadRequest{Dir: q.Get("path"), Name: q.Get("name"), Conflict: q.Get("conflict")}
	if reply, ok := s.upload(w, r, req); ok {
		writeCreated(w, UploadedFile{Path: path.Join(req.Dir, reply.Name), Name: reply.Name, Size: reply.Size})
	}
}

func (s *Server) pasteFile(w http.ResponseWriter, r *http.Request) {
	req := agentproto.UploadRequest{Name: r.URL.Query().Get("name"), Conflict: agentproto.ConflictKeep, Paste: true}
	if reply, ok := s.upload(w, r, req); ok {
		writeCreated(w, PastedFile{Path: path.Join(agentproto.PasteDir, reply.Name), Size: reply.Size})
	}
}

// upload checks the request's body and streams it to the guest as req, answering the problem
// itself when it fails.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, req agentproto.UploadRequest) (agentproto.UploadReply, bool) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != exportMediaType {
		s.writeProblem(w, "", huma.Error415UnsupportedMediaType("the body must be "+exportMediaType))
		return agentproto.UploadReply{}, false
	}
	switch {
	case r.ContentLength < 0:
		s.writeProblem(w, "", huma.NewError(http.StatusLengthRequired, "the upload needs a Content-Length"))
		return agentproto.UploadReply{}, false
	case r.ContentLength > agentproto.MaxUploadBytes:
		s.writeProblem(w, "", huma.NewError(http.StatusRequestEntityTooLarge, fmt.Sprintf("a file is at most %d MiB", agentproto.MaxUploadBytes>>20)))
		return agentproto.UploadReply{}, false
	}
	req.Size = r.ContentLength
	ctx, cancel := context.WithTimeout(r.Context(), uploadTimeout)
	defer cancel()
	reply, err := s.workspaceUpload(ctx, r.PathValue("env"), r.PathValue("id"), req, r.Body)
	if err != nil {
		s.writeProblem(w, "upload failed", err)
		return reply, false
	}
	return reply, true
}

func writeCreated(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(v)
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
