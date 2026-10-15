//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ControlPrefix is where the control API is served, on Studio's own loopback listener.
const ControlPrefix = "/api/e2e/"

// Requests decides a browser request of a sandbox the way the gateway does (POST
// /api/e2e/requests). Studio sets it: the request needs no browser VM, only the policy.
var Requests func(ctx context.Context, sandboxID, host string, port int, req policy.HTTPRequest) (store.Rule, error)

// Control returns the control API, which Studio mounts under ControlPrefix on its API mux:
// it gets the same login and Host and Origin checks as the rest of the API. It exists only
// in the e2e build.
//
//	GET  /api/e2e/sandboxes/{id}          the guest's recorded State
//	POST /api/e2e/sandboxes/{id}/connect  {"host": "example.com", "port": 80}: an HTTP request
//	                                    through the gateway, in the background
//	PUT  /api/e2e/sandboxes/{id}/ports    {"ports": [3000]}: the listening ports
//	POST /api/e2e/sandboxes/{id}/push     a Push: the guest clones through the gateway, commits one
//	                                    file and pushes; answers with Pushed once git is done
//	POST /api/e2e/requests                {"sandboxId", "host", "port", "method", "path", "navigation",
//	                                    "site"}: decides one browser request, as the gateway would, and
//	                                    answers with the outcome once it is decided
//	PUT  /api/e2e/upstream/{repo...}      (re)creates the upstream bare repository <host>/<path> with
//	                                    one commit on main, and lists its branches
//	GET  /api/e2e/upstream/{repo...}      the upstream repository's branches
func (r *Runtime) Control() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/e2e/sandboxes/{id}/push", func(w http.ResponseWriter, req *http.Request) {
		g, ok := r.Guest(req.PathValue("id"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		var p Push
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&p); err != nil || p.URL == "" || p.File == "" || p.Message == "" {
			http.Error(w, "want {url, branch, file, content, message}", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, g.Push(req.Context(), p))
	})
	upstream := func(w http.ResponseWriter, req *http.Request, list func(string) ([]Branch, error)) {
		if r.Upstream == nil {
			http.Error(w, "no upstream", http.StatusNotImplemented)
			return
		}
		branches, err := list(req.PathValue("repo"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Branches []Branch `json:"branches"`
		}{branches})
	}
	mux.HandleFunc("PUT /api/e2e/upstream/{repo...}", func(w http.ResponseWriter, req *http.Request) {
		upstream(w, req, r.Upstream.Create)
	})
	mux.HandleFunc("GET /api/e2e/upstream/{repo...}", func(w http.ResponseWriter, req *http.Request) {
		upstream(w, req, r.Upstream.Branches)
	})
	mux.HandleFunc("GET /api/e2e/sandboxes/{id}", func(w http.ResponseWriter, req *http.Request) {
		g, ok := r.Guest(req.PathValue("id"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		writeJSON(w, http.StatusOK, g.State())
	})
	mux.HandleFunc("POST /api/e2e/sandboxes/{id}/connect", func(w http.ResponseWriter, req *http.Request) {
		g, ok := r.Guest(req.PathValue("id"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		var body struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil || body.Host == "" {
			http.Error(w, "want {host, port}", http.StatusBadRequest)
			return
		}
		if body.Port == 0 {
			body.Port = 80
		}
		g.Connect(body.Host, body.Port)
		writeJSON(w, http.StatusAccepted, struct{}{})
	})
	mux.HandleFunc("POST /api/e2e/requests", func(w http.ResponseWriter, req *http.Request) {
		if Requests == nil {
			http.Error(w, "no policy to decide with", http.StatusNotImplemented)
			return
		}
		var body struct {
			SandboxID  string `json:"sandboxId"`
			Host       string `json:"host"`
			Port       int    `json:"port"`
			Method     string `json:"method"`
			Path       string `json:"path"`
			Navigation bool   `json:"navigation"`
			Site       string `json:"site"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil || body.SandboxID == "" || body.Host == "" {
			http.Error(w, "want {sandboxId, host, port, method, path, navigation, site}", http.StatusBadRequest)
			return
		}
		rq := policy.HTTPRequest{Method: body.Method, Path: body.Path, Navigation: body.Navigation, Site: body.Site}
		rule, err := Requests(req.Context(), body.SandboxID, body.Host, body.Port, rq)
		outcome := "allowed"
		switch {
		case errors.Is(err, policy.ErrDenied), errors.Is(err, policy.ErrDismissed), err == nil && rule.Action == store.ActionDeny:
			outcome = "denied"
		case err != nil:
			outcome = "failed: " + err.Error()
		}
		writeJSON(w, http.StatusOK, struct {
			Outcome string `json:"outcome"`
		}{outcome})
	})
	mux.HandleFunc("PUT /api/e2e/sandboxes/{id}/ports", func(w http.ResponseWriter, req *http.Request) {
		g, ok := r.Guest(req.PathValue("id"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		var body struct {
			Ports []int `json:"ports"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "want {ports}", http.StatusBadRequest)
			return
		}
		g.SetPorts(body.Ports)
		writeJSON(w, http.StatusOK, g.State())
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Dial stands in for the internet behind the gateway: every host answers HTTP requests
// with "hello from <host>". No connection leaves the machine.
func Dial(_ context.Context, _, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		br := bufio.NewReader(server)
		for {
			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}
			io.Copy(io.Discard, req.Body)
			body := "hello from " + host + "\n"
			if _, err := fmt.Fprintf(server, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body); err != nil || req.Close {
				return
			}
		}
	}()
	return client, nil
}
