package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type chatPath struct {
	Env  string `path:"env" doc:"Environment ID"`
	Chat string `path:"chat" doc:"Chat ID"`
}

// PromptFile is an attachment of a prompt.
type PromptFile struct {
	Name     string `json:"name" minLength:"1" maxLength:"255"`
	MimeType string `json:"mimeType,omitempty" maxLength:"255" doc:"Detected from the content when empty"`
	Data     []byte `json:"data" doc:"The file, base64-encoded"`
}

// PromptRequest is what the user sends to a chat.
type PromptRequest struct {
	Text        string       `json:"text,omitempty" maxLength:"262144"`
	Attachments []PromptFile `json:"attachments,omitempty" maxItems:"20" doc:"At most 10 MiB in total"`
}

func (p PromptRequest) prompt() chat.Prompt {
	out := chat.Prompt{Text: p.Text}
	for _, f := range p.Attachments {
		out.Attachments = append(out.Attachments, chat.File{Name: f.Name, MimeType: f.MimeType, Data: f.Data})
	}
	return out
}

// Prompts carry base64 attachments: 10 MiB of files is about 13.4 MiB of JSON.
const maxPromptBytes = 15 << 20

// registerChats adds chats (docs/design/chat-mode.md): conversations with a sandbox's
// owner teammate, whose harness runs over ACP in the sandbox.
func (s *Server) registerChats(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listChats", Method: http.MethodGet, Path: "/api/environments/{env}/chats", Tags: []string{"chats"},
		Description: "All chats of the environment, most recently active first.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Chat }, error) {
		list, err := s.Store.Chats(ctx, in.Env, "")
		return &struct{ Body []store.Chat }{list}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listSandboxChats", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/chats", Tags: []string{"chats"},
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []store.Chat }, error) {
		if _, err := s.sessionSandbox(ctx, in.Env, in.ID); err != nil {
			return nil, err
		}
		list, err := s.Store.Chats(ctx, in.Env, in.ID)
		return &struct{ Body []store.Chat }{list}, apiError(err)
	})

	type createIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Body *PromptRequest
	}
	huma.Register(api, huma.Operation{
		OperationID: "createChat", Method: http.MethodPost, Path: "/api/environments/{env}/sandboxes/{id}/chats", Tags: []string{"chats"},
		DefaultStatus: http.StatusCreated, MaxBodyBytes: maxPromptBytes,
		Description: "Starts a chat with the sandbox's owner teammate, sending the prompt if there is one. " +
			"409 for the same reasons as startSession.",
	}, func(ctx context.Context, in *createIn) (*struct{ Body store.Chat }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		sb, err := s.sessionSandbox(ctx, in.Env, in.ID)
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunch(ctx, sb)
		if err != nil {
			return nil, err
		}
		c, err := chats.Create(ctx, sb, launch.Harness.Name())
		if err != nil || in.Body == nil || (in.Body.Text == "" && len(in.Body.Attachments) == 0) {
			return &struct{ Body store.Chat }{c}, apiError(err)
		}
		// A chat whose first prompt fails is no use to anyone.
		if _, err := chats.Send(context.WithoutCancel(ctx), in.Env, c.ID, launch, in.Body.prompt()); err != nil {
			_ = chats.Delete(context.WithoutCancel(ctx), in.Env, c.ID)
			return nil, apiError(err)
		}
		c, err = s.Store.Chat(ctx, in.Env, c.ID)
		return &struct{ Body store.Chat }{c}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "getChat", Method: http.MethodGet, Path: "/api/environments/{env}/chats/{chat}", Tags: []string{"chats"},
		Description: "The chat with its turns and items. Follow it with the chat's stream from chat.rev.",
	}, func(ctx context.Context, in *chatPath) (*struct{ Body chat.Log }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		log, err := chats.Get(ctx, in.Env, in.Chat)
		return &struct{ Body chat.Log }{log}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteChat", Method: http.MethodDelete, Path: "/api/environments/{env}/chats/{chat}", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Stops the chat's agent and deletes the chat. The harness keeps its history in the agent user's home.",
	}, func(ctx context.Context, in *chatPath) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		return nil, apiError(chats.Delete(ctx, in.Env, in.Chat))
	})

	type promptIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Chat string `path:"chat" doc:"Chat ID"`
		Body PromptRequest
	}
	huma.Register(api, huma.Operation{
		OperationID: "sendPrompt", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/prompt", Tags: []string{"chats"},
		DefaultStatus: http.StatusAccepted, MaxBodyBytes: maxPromptBytes,
		Description: "Starts a turn, starting the chat's agent first if it isn't running. The turn's items arrive on the " +
			"chat's stream. 409 while a turn runs.",
	}, func(ctx context.Context, in *promptIn) (*struct{ Body chat.Turn }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunchFor(ctx, in.Env, in.Chat)
		if err != nil {
			return nil, err
		}
		t, err := chats.Send(context.WithoutCancel(ctx), in.Env, in.Chat, launch, in.Body.prompt())
		return &struct{ Body chat.Turn }{chat.TurnView(t)}, apiError(err)
	})

	type queueOut struct {
		Queued bool               `json:"queued" doc:"The prompt waits for the running turn; otherwise it started a turn"`
		Prompt *chat.QueuedPrompt `json:"prompt,omitempty" doc:"The waiting prompt, when it waits"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "queuePrompt", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/queue", Tags: []string{"chats"},
		DefaultStatus: http.StatusAccepted, MaxBodyBytes: maxPromptBytes,
		Description: "Sends the prompt like sendPrompt, or, while a turn runs, queues it to go once the turn ends, " +
			"after the prompts already waiting. The chat's stream shows the queue. It lives in memory: prompts still " +
			"waiting when Studio stops are not sent.",
	}, func(ctx context.Context, in *promptIn) (*struct{ Body queueOut }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunchFor(ctx, in.Env, in.Chat)
		if err != nil {
			return nil, err
		}
		q, queued, err := chats.Enqueue(context.WithoutCancel(ctx), in.Env, in.Chat, launch, in.Body.prompt(), nil)
		if err != nil {
			return nil, apiError(err)
		}
		out := queueOut{Queued: queued}
		if queued {
			out.Prompt = &q
		}
		return &struct{ Body queueOut }{out}, nil
	})

	type queuedPath struct {
		Env  string `path:"env" doc:"Environment ID"`
		Chat string `path:"chat" doc:"Chat ID"`
		ID   string `path:"id" doc:"Queued prompt ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "unqueuePrompt", Method: http.MethodDelete, Path: "/api/environments/{env}/chats/{chat}/queue/{id}", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Takes a waiting prompt off the chat's queue, so it never goes. 404 once it went.",
	}, func(ctx context.Context, in *queuedPath) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		if _, err := s.Store.Chat(ctx, in.Env, in.Chat); err != nil {
			return nil, apiError(err)
		}
		return nil, apiError(chats.Unqueue(in.Env, in.Chat, in.ID))
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancelChat", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/cancel", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Stops the running turn, if any. The turn ends on the stream.",
	}, func(ctx context.Context, in *chatPath) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		return nil, apiError(chats.Cancel(ctx, in.Env, in.Chat))
	})

	huma.Register(api, huma.Operation{
		OperationID: "openChatInTerminal", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/terminal", Tags: []string{"chats"},
		Description: "Hands the chat's agent session to its harness's TUI in a tmux session tagged with the chat. The chat " +
			"is read-only in state terminal until returnToChat. Calling it again restarts the TUI if it exited. " +
			"409 while a turn runs, and before the chat's first prompt.",
	}, func(ctx context.Context, in *chatPath) (*struct{ Body ChatTerminal }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunchFor(ctx, in.Env, in.Chat)
		if err != nil {
			return nil, err
		}
		name, err := chats.OpenInTerminal(context.WithoutCancel(ctx), in.Env, in.Chat, launch)
		var he agentchan.HarnessError
		if errors.As(err, &he) {
			return nil, huma.Error409Conflict(he.Error())
		}
		return &struct{ Body ChatTerminal }{ChatTerminal{Session: name}}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "returnToChat", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/return", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description: "Ends the chat's TUI and reattaches the agent; turns made in the terminal arrive on the stream with " +
			"origin terminal. Does nothing for a chat that isn't in the terminal.",
	}, func(ctx context.Context, in *chatPath) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunchFor(ctx, in.Env, in.Chat)
		if err != nil {
			return nil, err
		}
		return nil, apiError(chats.ReturnToChat(context.WithoutCancel(ctx), in.Env, in.Chat, launch))
	})

	type answerIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Chat string `path:"chat" doc:"Chat ID"`
		Item string `path:"item" doc:"Question item ID"`
		Body QuestionAnswer
	}
	huma.Register(api, huma.Operation{
		OperationID: "answerQuestion", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/items/{item}/answer", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description:   "Answers a question item the agent waits on. 409 once the agent no longer waits.",
	}, func(ctx context.Context, in *answerIn) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		return nil, apiError(chats.Answer(ctx, in.Env, in.Chat, in.Item, in.Body.Action, in.Body.Content))
	})

	type turnPath struct {
		Env  string `path:"env" doc:"Environment ID"`
		Chat string `path:"chat" doc:"Chat ID"`
		Turn string `path:"turn" doc:"Turn ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "undoTurn", Method: http.MethodPost, Path: "/api/environments/{env}/chats/{chat}/turns/{turn}/undo", Tags: []string{"chats"},
		DefaultStatus: http.StatusNoContent,
		Description: "Puts the sandbox's workspace back as it was before the turn, undoing it and every later turn " +
			"(.git and ignored files stay). The turns get undoneAt and the next prompt tells the agent. " +
			"409 while a turn runs, in the terminal, and for turns that aren't undoable.",
	}, func(ctx context.Context, in *turnPath) (*struct{}, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		return nil, apiError(chats.Undo(context.WithoutCancel(ctx), in.Env, in.Chat, in.Turn))
	})

	huma.Register(api, huma.Operation{
		OperationID: "getTurnChanges", Method: http.MethodGet, Path: "/api/environments/{env}/chats/{chat}/turns/{turn}/changes", Tags: []string{"chats"},
		Description: "The files the turn changed, with unified diffs of text files (at most 128 KiB each and 512 KiB in all; " +
			"truncated marks the rest). Without diffs once the turn's snapshots are gone.",
	}, func(ctx context.Context, in *turnPath) (*struct{ Body chat.TurnChanges }, error) {
		chats, err := s.chats()
		if err != nil {
			return nil, err
		}
		out, err := chats.Changes(ctx, in.Env, in.Chat, in.Turn)
		return &struct{ Body chat.TurnChanges }{out}, apiError(err)
	})

	type filesIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Sandbox ID"`
		Path string `query:"path" maxLength:"4096" doc:"Relative to /workspace; empty for /workspace itself"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listFiles", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/files", Tags: []string{"chats"},
		Description: "Lists a directory of the sandbox's workspace, directories first, at most 1,000 entries. " +
			"Symlinks are listed, never followed.",
	}, func(ctx context.Context, in *filesIn) (*struct{ Body FileList }, error) {
		reply, err := s.workspaceFiles(ctx, in.Env, in.ID, agentproto.FilesRequest{Op: agentproto.FilesList, Path: in.Path})
		if err != nil {
			return nil, err
		}
		return &struct{ Body FileList }{FileList{Entries: orEmpty(reply.Entries), Truncated: reply.Truncated}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "readFile", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/files/content", Tags: []string{"chats"},
		Description: "Reads a file of the sandbox's workspace: text and PNG, JPEG, GIF and WebP images of at most 1 MiB come " +
			"with their data; otherwise tooLarge, binary or unreadable (the agent user can't read it) says why not.",
	}, func(ctx context.Context, in *filesIn) (*struct{ Body FileContent }, error) {
		if in.Path == "" {
			return nil, huma.Error422UnprocessableEntity("path is required")
		}
		r, err := s.workspaceFiles(ctx, in.Env, in.ID, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: in.Path})
		if err != nil {
			return nil, err
		}
		return &struct{ Body FileContent }{FileContent{Size: r.Size, MimeType: r.MimeType, Data: r.Data, TooLarge: r.TooLarge, Binary: r.Binary, Unreadable: r.Unreadable}}, nil
	})
}

// QuestionAnswer answers an agent's question.
type QuestionAnswer struct {
	Action  string          `json:"action" enum:"accept,decline,cancel"`
	Content json.RawMessage `json:"content,omitempty" doc:"The form's values, for accept: a JSON object matching the question's schema"`
}

func (s *Server) chats() (*chat.Service, error) {
	if s.Chats == nil {
		return nil, huma.Error503ServiceUnavailable("chats are not available")
	}
	return s.Chats, nil
}

// ChatLaunch plans a chat's agent in a sandbox, as sending a chat prompt does. Heartbeats use it.
func (s *Server) ChatLaunch(ctx context.Context, sb store.Sandbox) (chat.Launch, error) {
	return s.chatLaunch(ctx, sb)
}

// chatLaunch plans a chat's agent like an agent session. The command depends on the provider:
// a Claude subscription's agent runs Claude Code itself (harness.Claude.ACPCommand).
func (s *Server) chatLaunch(ctx context.Context, sb store.Sandbox) (chat.Launch, error) {
	plan, err := s.planSession(ctx, sb)
	if err != nil {
		return chat.Launch{}, err
	}
	return chat.Launch{Harness: plan.harness, Files: plan.files, Command: plan.harness.ACPCommand(plan.provider),
		Env: harness.ACPEnvironment(plan.harness, plan.provider), TUIEnv: plan.env}, nil
}

// chatLaunchFor plans the agent of an existing chat.
func (s *Server) chatLaunchFor(ctx context.Context, envID, chatID string) (chat.Launch, error) {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return chat.Launch{}, apiError(err)
	}
	sb, err := s.sessionSandbox(ctx, envID, c.SandboxID)
	if err != nil {
		return chat.Launch{}, err
	}
	return s.chatLaunch(ctx, sb)
}

// FileList is a directory of a sandbox's workspace.
type FileList struct {
	Entries   []agentproto.FileEntry `json:"entries"`
	Truncated bool                   `json:"truncated,omitempty" doc:"More than 1,000 entries"`
}

// FileContent is a file of a sandbox's workspace.
type FileContent struct {
	Size       int64  `json:"size"`
	MimeType   string `json:"mimeType,omitempty"`
	Data       []byte `json:"data,omitempty" doc:"The file, base64-encoded"`
	TooLarge   bool   `json:"tooLarge,omitempty" doc:"Larger than 1 MiB"`
	Binary     bool   `json:"binary,omitempty" doc:"Neither text nor a shown image type"`
	Unreadable bool   `json:"unreadable,omitempty" doc:"The sandbox's agent user can't read it"`
}

// workspaceFiles asks the guest for a directory or file. Its refusals (a missing path, a
// symlink, an invalid path) are the caller's mistakes.
func (s *Server) workspaceFiles(ctx context.Context, envID, id string, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	chats, err := s.chats()
	if err != nil {
		return agentproto.FilesReply{}, err
	}
	if _, err := s.sessionSandbox(ctx, envID, id); err != nil {
		return agentproto.FilesReply{}, err
	}
	reply, err := chats.Files(ctx, id, req)
	switch {
	case err == nil:
		return reply, nil
	case errors.Is(err, agentchan.ErrNotConnected), ctx.Err() != nil:
		return reply, apiError(err)
	}
	return reply, huma.Error422UnprocessableEntity(err.Error())
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ChatTerminal is the tmux session a chat continues in.
type ChatTerminal struct {
	Session string `json:"session" doc:"The tmux session's name; attach to it like any agent session"`
}

// --- streaming ------------------------------------------------------------------------

// streamChat sends a chat's changes as server-sent events: every turn and item changed
// after the rev in ?after= or Last-Event-ID, then live changes. Events are `chat`
// (store.Chat, first and whenever its state, title or session changes), `turn`
// (chat.Turn), `item` (chat.Item) and `queue` ([]chat.QueuedPrompt, first and whenever the
// prompts waiting for the running turn change); each carries a rev as the event ID, so a
// reconnect resumes where it left off. The stream ends when the chat is deleted.
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request) {
	chats, err := s.chats()
	if err != nil {
		http.Error(w, "chats are not available", http.StatusServiceUnavailable)
		return
	}
	envID, chatID := r.PathValue("env"), r.PathValue("chat")
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = strconv.ParseInt(v, 10, 64)
	} else if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, err = strconv.ParseInt(v, 10, 64)
	}
	if err != nil || after < 0 {
		http.Error(w, "after must be a rev", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	wake, stop := chats.Watch(chatID)
	defer stop()
	c, turns, items, err := s.Store.ChatChanges(ctx, envID, chatID, after)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such chat", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	fmt.Fprint(w, "retry: 2000\n\n")
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	var sent *store.Chat // a copy: c is overwritten by each read
	var queue []chat.QueuedPrompt
	for {
		// Turns and items in rev order, so the last ID sent is a safe place to resume;
		// the chat comes last, since its rev covers everything read with it.
		for len(turns) > 0 || len(items) > 0 {
			if len(items) == 0 || (len(turns) > 0 && turns[0].Rev <= items[0].Rev) {
				writeEvent(w, "turn", turns[0].Rev, chat.TurnView(turns[0]))
				turns = turns[1:]
			} else {
				writeEvent(w, "item", items[0].Rev, chat.ItemView(items[0]))
				items = items[1:]
			}
		}
		if sent == nil || c.State != sent.State || c.Title != sent.Title || c.ACPSessionID != sent.ACPSessionID {
			writeEvent(w, "chat", c.Rev, c)
			last := c
			sent = &last
		}
		after = max(after, c.Rev)
		if q := chats.Queued(chatID); queue == nil || !slices.EqualFunc(q, queue, func(a, b chat.QueuedPrompt) bool { return a.ID == b.ID }) {
			writeEvent(w, "queue", after, q)
			queue = q
		}
		if rc.Flush() != nil {
			return
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				if rc.Flush() != nil {
					return
				}
			case <-wake:
				break wait
			}
		}
		if c, turns, items, err = s.Store.ChatChanges(ctx, envID, chatID, after); err != nil {
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, event string, rev int64, v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event, rev, data)
}
