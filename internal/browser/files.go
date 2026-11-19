package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Files from the user's computer, for a page of a teammate's browser. Headless Chromium has
// no file dialog: a page that asks for a file gets none, and nothing can be dropped on it.
// While a live view is open, Studio watches its page over Chromium's debugging protocol with
// the file chooser intercepted. When the page asks for files while the user drives, the view
// says so (studio_file_chooser) and offers the user's own. They go to the browser VM as
// pastes (PasteFile, into agentproto.PasteDir, as a terminal's do), and the view sends their
// paths back over its socket (studio_files): Studio sets them on the input that asked, or
// drops them on the page where the pointer let go (docs/browser.md#files-from-your-computer).

// ErrFilesNeedTakeover refuses files while the agent drives: they are the user's to give.
var ErrFilesNeedTakeover = errors.New("take over the browser before giving the page files")

const (
	// maxGivenFiles bounds the files one choice or drop gives the page.
	maxGivenFiles = 32
	// cdpWait bounds connecting to the page's debugging socket, and each call on it.
	cdpWait = 10 * time.Second
	// cdpReadLimit bounds one message of the page's debugging socket: its events are small.
	cdpReadLimit = 8 << 20
	// dragOperations offers a drop as a copy, a link or a move, as a file manager's drag does.
	dragOperations = 1 | 2 | 16
)

// PasteFile writes a file of the user's into the browser VM, for a page of the session to be
// given, and returns its path there. The user must be driving the session.
func (s *Service) PasteFile(ctx context.Context, envID, teammateID, session, name string, size int64, body io.Reader) (string, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return "", err
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	vm := st.vm
	driving := pg != nil && pg.takeover
	s.mu.Unlock()
	switch {
	case pg == nil:
		return "", ErrNoSession
	case vm == "":
		return "", ErrNotLive
	case !driving:
		return "", ErrFilesNeedTakeover
	}
	reply, err := s.Runner.Upload(ctx, vm, agentproto.UploadRequest{Name: name, Size: size, Conflict: agentproto.ConflictKeep, Paste: true}, body)
	if err != nil {
		return "", err
	}
	return path.Join(agentproto.PasteDir, reply.Name), nil
}

// viewFiles is a live view's side of the files: its watch of the page, while connected.
type viewFiles struct {
	mu sync.Mutex
	w  *pageWatch
	// retarget is signalled when the stream's tabs change: the view may show another tab.
	retarget chan struct{}
}

func newViewFiles() *viewFiles { return &viewFiles{retarget: make(chan struct{}, 1)} }

func (v *viewFiles) set(w *pageWatch) {
	v.mu.Lock()
	v.w = w
	v.mu.Unlock()
}

func (v *viewFiles) get() *pageWatch {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.w
}

func (v *viewFiles) tabsChanged() {
	select {
	case v.retarget <- struct{}{}:
	default:
	}
}

// pageWatch is a connection to a page's debugging socket.
type pageWatch struct {
	conn  *websocket.Conn
	mu    sync.Mutex
	next  int64
	calls map[int64]chan cdpReply
	// chooser is the page's last request for files while the user drove, until answered.
	chooser *fileChooser
	asked   int
}

// fileChooser is a page's request for files: the input that asked, and whether it takes several.
type fileChooser struct {
	id       string
	node     int64
	multiple bool
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

// watchFiles keeps a watch on the view's page until ctx ends, telling the view when the
// page asks for files. A browser whose debugging port can't be reached (a guest agent from
// before DevTools) leaves the view without files and otherwise working.
func (s *Service) watchFiles(ctx context.Context, st *state, pg *page, envID, vm, session string, client *websocket.Conn, vf *viewFiles) {
	failures := 0
	for ctx.Err() == nil && failures < 5 {
		started := s.now()
		err := s.watchPageOnce(ctx, st, pg, envID, vm, session, client, vf)
		if ctx.Err() != nil {
			return
		}
		if s.now().Sub(started) > 30*time.Second {
			failures = 0
		}
		failures++
		if err != nil {
			s.log().Debug("watching a browser page for files", "teammate", st.teammate, "err", err)
		}
		if errors.Is(err, ErrNoDevTools) {
			// An older guest agent: it won't answer until the VM restarts.
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(failures) * time.Second):
		}
	}
}

// watchPageOnce connects to the page the view shows and watches it until the socket ends,
// the view shows another tab, or ctx ends.
func (s *Service) watchPageOnce(ctx context.Context, st *state, pg *page, envID, vm, session string, client *websocket.Conn, vf *viewFiles) error {
	cctx, cancel := context.WithTimeout(ctx, cdpWait)
	defer cancel()
	port, err := s.devToolsPort(cctx, st, pg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	want := pg.url
	s.mu.Unlock()
	target, err := s.devToolsTarget(cctx, vm, port, want)
	if err != nil {
		return err
	}
	conn, err := s.dialWS(cctx, vm, port, "/devtools/page/"+target.ID)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(cdpReadLimit)
	w := &pageWatch{conn: conn, calls: map[int64]chan cdpReply{}}
	done := make(chan error, 1)
	go func() {
		done <- w.read(ctx, func(method string, params json.RawMessage) {
			s.pageEvent(ctx, st, pg, envID, vm, session, client, w, method, params)
		})
	}()
	select {
	case <-vf.retarget:
		// A tab that changed before the watch began: this one is already the view's.
	default:
	}
	for _, c := range []struct {
		method string
		params any
	}{{"Page.enable", nil}, {"Page.setInterceptFileChooserDialog", map[string]bool{"enabled": true}}} {
		if _, err := w.call(cctx, c.method, c.params); err != nil {
			return err
		}
	}
	vf.set(w)
	defer vf.set(nil)
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return nil
		case <-vf.retarget:
			// The view may show another tab now. The same tab keeps its socket, and with it
			// the page's request for files.
			lctx, lcancel := context.WithTimeout(ctx, cdpWait)
			s.mu.Lock()
			want := pg.url
			s.mu.Unlock()
			next, err := s.devToolsTarget(lctx, vm, port, want)
			lcancel()
			if err != nil || next.ID != target.ID {
				return nil
			}
		}
	}
}

// devToolsPort asks the guest for the port of the session's debugging server.
func (s *Service) devToolsPort(ctx context.Context, st *state, pg *page) (int, error) {
	data, _, err := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: []string{agentproto.BrowserDevToolsPort}})
	switch {
	case err == nil:
	case errors.Is(err, ErrNotLive), ctx.Err() != nil:
		return 0, err
	case strings.Contains(err.Error(), "is not allowed"):
		// A VM started before this Studio runs the guest agent it started with.
		return 0, fmt.Errorf("%w: the browser runs an older guest agent; stop it and open it again", ErrNoDevTools)
	default:
		return 0, fmt.Errorf("%w: %v", ErrNoDevTools, err)
	}
	var reply struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Port < 1 || reply.Port > 65535 {
		return 0, errors.New("the browser's debugging port was not understood")
	}
	return reply.Port, nil
}

// read hands replies to their calls and events to on, until the socket ends.
func (w *pageWatch) read(ctx context.Context, on func(method string, params json.RawMessage)) error {
	defer w.fail(errors.New("the page's debugging socket closed"))
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			return err
		}
		var m struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch {
		case m.ID != 0:
			w.mu.Lock()
			ch := w.calls[m.ID]
			delete(w.calls, m.ID)
			w.mu.Unlock()
			if ch == nil {
				continue
			}
			r := cdpReply{result: m.Result}
			if m.Error != nil {
				r.err = fmt.Errorf("the browser refused: %s", clip(m.Error.Message, 300))
			}
			ch <- r
		case m.Method == "Inspector.detached", m.Method == "Inspector.targetCrashed":
			return errors.New("the page's tab went away")
		case m.Method != "":
			on(m.Method, m.Params)
		}
	}
}

// fail ends every call still waiting.
func (w *pageWatch) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, ch := range w.calls {
		ch <- cdpReply{err: err}
		delete(w.calls, id)
	}
}

// call sends one command and waits for its reply.
func (w *pageWatch) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	w.mu.Lock()
	w.next++
	id := w.next
	ch := make(chan cdpReply, 1)
	w.calls[id] = ch
	w.mu.Unlock()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if err := w.conn.Write(ctx, websocket.MessageText, b); err != nil {
		w.mu.Lock()
		delete(w.calls, id)
		w.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		w.mu.Lock()
		delete(w.calls, id)
		w.mu.Unlock()
		return nil, ctx.Err()
	}
}

// take returns the page's request for files with the given ID, and forgets it.
func (w *pageWatch) take(id string) *fileChooser {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.chooser
	if c == nil || c.id != id {
		return nil
	}
	w.chooser = nil
	return c
}

// pageEvent tells the view that the page asks for files, while the user drives. An agent's
// click on a file input gets nothing, as it did before.
func (s *Service) pageEvent(ctx context.Context, st *state, pg *page, envID, vm, session string, client *websocket.Conn, w *pageWatch, method string, params json.RawMessage) {
	if method != "Page.fileChooserOpened" {
		return
	}
	var p struct {
		Mode          string `json:"mode"`
		BackendNodeID int64  `json:"backendNodeId"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	driving := s.streamCurrentLocked(st, pg, envID, vm, session) && pg.takeover
	s.mu.Unlock()
	if !driving {
		return
	}
	msg := map[string]any{"type": "studio_file_chooser", "multiple": p.Mode == "selectMultiple"}
	if p.BackendNodeID == 0 {
		// A picker the page opened from script (showOpenFilePicker) has no input to set.
		msg["unsupported"] = true
	} else {
		w.mu.Lock()
		w.asked++
		c := &fileChooser{id: strconv.Itoa(w.asked), node: p.BackendNodeID, multiple: p.Mode == "selectMultiple"}
		w.chooser = c
		w.mu.Unlock()
		msg["id"] = c.id
	}
	b, _ := json.Marshal(msg)
	_ = client.Write(ctx, websocket.MessageText, b)
}

// filesMessage is the view's answer: the paths of files it pasted into the browser VM, for
// the request named chooser (none cancels it) or dropped at a point of the page.
type filesMessage struct {
	Type    string   `json:"type"`
	Chooser string   `json:"chooser"`
	Drop    *point   `json:"drop"`
	Paths   []string `json:"paths"`
}

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// parseFilesMessage reads a message of the view's that gives the page files.
func parseFilesMessage(data []byte) (filesMessage, bool) {
	var m filesMessage
	if json.Unmarshal(data, &m) != nil || m.Type != "studio_files" {
		return filesMessage{}, false
	}
	return m, true
}

// giveFiles answers the view's studio_files message, and tells it how that went.
func (s *Service) giveFiles(ctx context.Context, st *state, pg *page, envID, vm, session string, client *websocket.Conn, vf *viewFiles, m filesMessage) {
	err := s.applyFiles(ctx, st, pg, envID, vm, session, vf, m)
	reply := map[string]any{"type": "studio_files", "ok": err == nil, "given": len(m.Paths), "dropped": m.Drop != nil}
	if err != nil {
		reply["error"] = err.Error()
	}
	b, _ := json.Marshal(reply)
	_ = client.Write(ctx, websocket.MessageText, b)
}

func (s *Service) applyFiles(ctx context.Context, st *state, pg *page, envID, vm, session string, vf *viewFiles, m filesMessage) error {
	s.mu.Lock()
	current := s.streamCurrentLocked(st, pg, envID, vm, session)
	driving := pg.takeover
	s.mu.Unlock()
	switch {
	case !current:
		return ErrNotLive
	case !driving:
		return ErrFilesNeedTakeover
	}
	if err := checkPastePaths(m.Paths); err != nil {
		return err
	}
	w := vf.get()
	if w == nil {
		return errors.New("Studio can't reach this page's debugging port; stop the browser and open it again")
	}
	ctx, cancel := context.WithTimeout(ctx, cdpWait)
	defer cancel()
	if m.Drop != nil {
		if len(m.Paths) == 0 {
			return errors.New("nothing was dropped")
		}
		if m.Drop.X < 0 || m.Drop.Y < 0 || m.Drop.X > 100000 || m.Drop.Y > 100000 {
			return ErrBadPoint
		}
		data := map[string]any{"items": []any{}, "files": m.Paths, "dragOperationsMask": dragOperations}
		for _, typ := range []string{"dragEnter", "dragOver", "drop"} {
			if _, err := w.call(ctx, "Input.dispatchDragEvent", map[string]any{"type": typ, "x": m.Drop.X, "y": m.Drop.Y, "data": data}); err != nil {
				return err
			}
		}
		s.recordFiles(ctx, st, pg, m.Paths, "dropped")
		return nil
	}
	c := w.take(m.Chooser)
	switch {
	case c == nil:
		return errors.New("the page no longer waits for these files; click its button again")
	case len(m.Paths) == 0:
		return nil
	case !c.multiple && len(m.Paths) > 1:
		return errors.New("the page takes one file")
	}
	if _, err := w.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": m.Paths, "backendNodeId": c.node}); err != nil {
		return err
	}
	s.recordFiles(ctx, st, pg, m.Paths, "chosen")
	return nil
}

// checkPastePaths keeps the files given to a page to pastes: one name in PasteDir each.
func checkPastePaths(paths []string) error {
	if len(paths) > maxGivenFiles {
		return fmt.Errorf("a page is given at most %d files at once", maxGivenFiles)
	}
	for _, p := range paths {
		name, ok := strings.CutPrefix(p, agentproto.PasteDir+"/")
		if !ok || name == "" || len(name) > agentproto.MaxUploadName || strings.Contains(name, "/") || name == "." || name == ".." {
			return errors.New("a file must be one Studio pasted into the browser")
		}
	}
	return nil
}

// recordFiles logs the files the user gave the page, which the agent's handback note names.
func (s *Service) recordFiles(ctx context.Context, st *state, pg *page, paths []string, how string) {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = path.Base(p)
	}
	s.mu.Lock()
	url := pg.url
	s.mu.Unlock()
	s.record(ctx, st, pg, entry{actor: "user", action: "give files", target: strings.Join(names, ", "), url: url, outcome: "ok", detail: how})
}
