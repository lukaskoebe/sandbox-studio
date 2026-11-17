//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The fake's sessions serve their DevTools on devToolsPort+slot, as Chromium serves its
// debugging protocol on the port --remote-debugging-port=0 picked: the list of tabs, a
// frontend page and the tab's socket, which answers every message with the page's URL.
// With STUDIO_E2E_DEVTOOLS set to the address of a real Chromium's debugging server, the
// port relays to that instead, for its real frontend.
const devToolsPort = 9400

// devToolsTarget is the fake tab's DevTools target ID.
const devToolsTarget = "E2E00000000000000000000000000001"

// devToolsPortOf answers devtools-port for a started session.
func devToolsPortOf(slot int) json.RawMessage {
	out, _ := json.Marshal(map[string]any{"success": true, "data": map[string]int{"port": devToolsPort + slot}, "error": nil})
	return out
}

// devToolsTab returns the started session whose DevTools listen on port, if any.
func (g *Guest) devToolsTab(port int) (*browserVM, *tab) {
	g.mu.Lock()
	b := g.browser
	g.mu.Unlock()
	slot := port - devToolsPort
	if b == nil || slot < 0 || slot > agentproto.MaxBrowserEphemeral {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.tabs[slot]; t != nil {
		return b, t
	}
	return nil, nil
}

// serveDevTools serves a session's debugging server on one tcp stream.
func (g *Guest) serveDevTools(st net.Conn, br *bufio.Reader, b *browserVM, t *tab, port int) error {
	if addr := os.Getenv("STUDIO_E2E_DEVTOOLS"); addr != "" {
		up, err := net.Dial("tcp", addr)
		if err != nil {
			return err
		}
		defer up.Close()
		go func() {
			io.Copy(up, br)
			up.(*net.TCPConn).CloseWrite()
		}()
		_, err = io.Copy(st, up)
		return err
	}
	conn := &streamConn{Conn: st, r: br, closed: make(chan struct{})}
	page := func() (string, string) {
		b.mu.Lock()
		defer b.mu.Unlock()
		return t.url(), t.title()
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Chromium's own checks: a Host that is an address or localhost, and no Origin on a
		// socket it wasn't told to allow.
		if h, _, _ := net.SplitHostPort(r.Host); h != "127.0.0.1" && h != "localhost" {
			http.Error(w, "Host header is specified and is not an IP address or localhost.", http.StatusInternalServerError)
			return
		}
		switch {
		case r.URL.Path == "/json/list":
			u, title := page()
			json.NewEncoder(w).Encode([]map[string]string{{
				"id": devToolsTarget, "type": "page", "title": title, "url": u,
				"webSocketDebuggerUrl": "ws://127.0.0.1:" + strconv.Itoa(port) + "/devtools/page/" + devToolsTarget,
			}})
		case r.URL.Path == "/devtools/devtools_app.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, fakeFrontend)
		case r.URL.Path == "/devtools/page/"+devToolsTarget:
			if r.Header.Get("Origin") != "" {
				http.Error(w, "Rejected an incoming WebSocket connection from the "+r.Header.Get("Origin")+" origin.", http.StatusForbidden)
				return
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			for {
				_, msg, err := c.Read(r.Context())
				if err != nil {
					return
				}
				var call struct {
					ID int `json:"id"`
				}
				json.Unmarshal(msg, &call)
				u, title := page()
				reply, _ := json.Marshal(map[string]any{"id": call.ID, "result": map[string]string{"url": u, "title": title}})
				if c.Write(r.Context(), websocket.MessageText, reply) != nil {
					return
				}
			}
		case strings.HasPrefix(r.URL.Path, "/json"), strings.HasPrefix(r.URL.Path, "/devtools/browser"):
			// What Studio must never pass on.
			http.Error(w, "the fake's browser-wide endpoints were reached", http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	})}
	srv.Serve(&oneListener{conn: conn})
	<-conn.closed
	return nil
}

// fakeFrontend stands in for Chromium's DevTools frontend: it connects to the socket its ws
// parameter names, as the real one does, and shows what the page's socket answered.
const fakeFrontend = `<!doctype html><meta charset="utf-8"><title>DevTools</title>
<body style="font:13px system-ui;margin:0;padding:12px;background:#fff;color:#222">
<h1 style="font-size:14px;margin:0 0 8px">Elements · Console · Network</h1>
<p id="state">Connecting…</p>
<script>
const q = new URLSearchParams(location.search)
const ws = new WebSocket((q.has("wss") ? "wss://" + q.get("wss") : "ws://" + q.get("ws")))
const state = document.getElementById("state")
ws.onopen = () => ws.send(JSON.stringify({ id: 1, method: "Page.getNavigationHistory" }))
ws.onmessage = (e) => { const r = JSON.parse(e.data).result; state.textContent = "Inspecting " + r.title + " at " + r.url }
ws.onerror = () => { state.textContent = "The page's socket failed" }
</script>`
