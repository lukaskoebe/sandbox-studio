package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The messages the commands return. Each one is applied by app.Update.
type (
	clockMsg   time.Time
	envsLoaded struct {
		envs []store.Environment
		err  error
	}
	sandboxesLoaded struct {
		env       string
		list      []sandboxes.View
		teammates []store.Teammate
		err       error
	}
	approvalsLoaded struct {
		list []api.ApprovalView
		err  error
	}
	subscribed struct {
		ch  <-chan events.Event
		err error
	}
	eventReceived struct{ ev events.Event }
	streamEnded   struct{}
	reconnectDue  struct{}
	refreshDue    struct{}
	actionDone    struct {
		done string
		err  error
	}
)

// clock ticks once a minute, so the uptimes stay current.
func clock() tea.Cmd {
	return tea.Every(time.Minute, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// bell rings the terminal bell once. tea.Raw writes it through the renderer, so it never lands
// in the middle of a frame.
func bell() tea.Cmd { return tea.Raw("\a") }

// refreshAfter debounces a burst of change events into one refresh.
func refreshAfter() tea.Cmd {
	return tea.Tick(refreshDelay, func(time.Time) tea.Msg { return refreshDue{} })
}

// reconnect retries the event stream after a while.
func reconnect() tea.Cmd {
	return tea.Tick(reconnectDelay, func(time.Time) tea.Msg { return reconnectDue{} })
}

// requestContext bounds one read of the API.
func requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, requestTimeout)
}

func (m *app) loadEnvs() tea.Cmd {
	ctx, client := m.ctx, m.client
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		envs, err := client.Environments(rctx)
		return envsLoaded{envs: envs, err: err}
	}
}

// loadSandboxes reads the sandboxes and teammates of the environment on show.
func (m *app) loadSandboxes() tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Sandboxes(rctx, env)
		if err != nil {
			return sandboxesLoaded{env: env, err: err}
		}
		people, err := client.Teammates(rctx, env)
		return sandboxesLoaded{env: env, list: list, teammates: people, err: err}
	}
}

func (m *app) loadApprovals() tea.Cmd {
	ctx, client := m.ctx, m.client
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.PendingApprovals(rctx)
		return approvalsLoaded{list: list, err: err}
	}
}

// subscribe opens the event stream. It lives as long as the program.
func (m *app) subscribe() tea.Cmd {
	ctx, client := m.ctx, m.client
	return func() tea.Msg {
		ch, err := client.Subscribe(ctx)
		return subscribed{ch: ch, err: err}
	}
}

// listen waits for the next event of the stream.
func listen(ch <-chan events.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamEnded{}
		}
		return eventReceived{ev: ev}
	}
}

// lifecycle starts, stops, suspends or resumes a sandbox. A start returns when the VM is up, so
// the command has no timeout of its own: it ends with the program.
func (m *app) lifecycle(sb sandboxes.View, action, verb string) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		if _, err := client.Lifecycle(ctx, env, sb.ID, action); err != nil {
			return actionDone{err: fmt.Errorf("%s %s: %w", verb, sb.Name, err)}
		}
		return actionDone{done: fmt.Sprintf("%s %s", pastTense(verb), sb.Name)}
	}
}

// remove deletes a sandbox.
func (m *app) remove(sb sandboxes.View) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		if err := client.DeleteSandbox(rctx, env, sb.ID); err != nil {
			return actionDone{err: fmt.Errorf("delete %s: %w", sb.Name, err)}
		}
		return actionDone{done: "Deleted " + sb.Name}
	}
}

// pastTense turns a verb of the lifecycle into the word for its result.
func pastTense(verb string) string {
	switch verb {
	case "stop":
		return "Stopped"
	case "resume":
		return "Resumed"
	case "suspend":
		return "Suspended"
	default:
		return "Started"
	}
}
