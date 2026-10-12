//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// ControlPrefix is where the control API is served, on Studio's own loopback listener.
const ControlPrefix = "/api/e2e/"

// Control returns the control API, which Studio mounts under ControlPrefix on its API mux:
// it gets the same login and Host and Origin checks as the rest of the API. It exists only
// in the e2e build.
//
//	GET  /api/e2e/sandboxes/{id}          the guest's recorded State
//	POST /api/e2e/sandboxes/{id}/connect  {"host": "example.com", "port": 80}: an HTTP request
//	                                    through the gateway, in the background
//	PUT  /api/e2e/sandboxes/{id}/ports    {"ports": [3000]}: the listening ports
func (r *Runtime) Control() http.Handler {
	mux := http.NewServeMux()
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
