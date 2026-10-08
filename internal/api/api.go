// Package api implements the Studio HTTP API under /api.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// Routes registers the API handlers on mux.
func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
