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
	"strings"
)

// ControlPrefix is where the control API is served, on Studio's own loopback listener.
const ControlPrefix = "/__e2e/"

// Control serves the control API under ControlPrefix and passes everything else to next.
// It needs no login: it exists only in the e2e build, which only listens on loopback.
//
//	GET  /__e2e/sandboxes/{id}          the guest's recorded State
//	POST /__e2e/sandboxes/{id}/connect  {"host": "example.com", "port": 80}: an HTTP request
//	                                    through the gateway, in the background
//	PUT  /__e2e/sandboxes/{id}/ports    {"ports": [3000]}: the listening ports
func (r *Runtime) Control(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__e2e/sandboxes/{id}", func(w http.ResponseWriter, req *http.Request) {
		g, ok := r.Guest(req.PathValue("id"))
		if !ok {
			http.NotFound(w, req)
			return
		}
		writeJSON(w, http.StatusOK, g.State())
	})
	mux.HandleFunc("POST /__e2e/sandboxes/{id}/connect", func(w http.ResponseWriter, req *http.Request) {
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
	mux.HandleFunc("PUT /__e2e/sandboxes/{id}/ports", func(w http.ResponseWriter, req *http.Request) {
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
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, ControlPrefix) {
			mux.ServeHTTP(w, req)
			return
		}
		next.ServeHTTP(w, req)
	})
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
