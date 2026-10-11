package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
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
			"409 for the same reasons as startSession, and for Claude subscription providers, which chats don't support.",
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
		c, err := s.Store.Chat(ctx, in.Env, in.Chat)
		if err != nil {
			return nil, apiError(err)
		}
		sb, err := s.sessionSandbox(ctx, in.Env, c.SandboxID)
		if err != nil {
			return nil, err
		}
		launch, err := s.chatLaunch(ctx, sb)
		if err != nil {
			return nil, err
		}
		t, err := chats.Send(context.WithoutCancel(ctx), in.Env, in.Chat, launch, in.Body.prompt())
		return &struct{ Body chat.Turn }{chat.TurnView(t)}, apiError(err)
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

// chatLaunch plans a chat's agent like an agent session. Claude subscriptions are
// refused: Anthropic doesn't allow claude.ai login in third-party agents.
func (s *Server) chatLaunch(ctx context.Context, sb store.Sandbox) (chat.Launch, error) {
	if sb.TeammateID != "" {
		if p, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID); err == nil {
			if prov, err := s.Store.Provider(ctx, sb.EnvironmentID, p.ProviderID); err == nil && prov.Kind == teammates.KindClaudeSubscription {
				return chat.Launch{}, huma.Error409Conflict(fmt.Sprintf("%s uses a Claude subscription, which chats can't use: "+
					"give the teammate an Anthropic API key provider, or open a terminal session instead", p.Name))
			}
		}
	}
	plan, err := s.planSession(ctx, sb)
	if err != nil {
		return chat.Launch{}, err
	}
	return chat.Launch{Harness: plan.harness, Files: plan.files, Env: harness.ACPEnvironment(plan.harness, plan.provider)}, nil
}

// --- streaming ------------------------------------------------------------------------

// streamChat sends a chat's changes as server-sent events: every turn and item changed
// after the rev in ?after= or Last-Event-ID, then live changes. Events are `chat`
// (store.Chat, first and whenever its state, title or session changes), `turn`
// (chat.Turn) and `item` (chat.Item); each carries its rev as the event ID, so a
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
	var sent *store.Chat
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
			sent = &c
		}
		after = max(after, c.Rev)
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
