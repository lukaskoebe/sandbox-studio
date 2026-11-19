package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Once Studio stops, a read's context ends, so an event stream lets the shutdown go on, while
// a write keeps its context and finishes.
func TestShutdownEndsReadsAndLetsWritesFinish(t *testing.T) {
	streams, end := context.WithCancel(context.Background())
	ended := make(chan string, 2)
	h := endWithShutdown(streams, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			ended <- r.Method + " ended"
		case <-time.After(300 * time.Millisecond):
			ended <- r.Method + " finished"
		}
	}))
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(m, "/api/x", nil))
	}
	time.Sleep(50 * time.Millisecond)
	end()
	got := map[string]bool{<-ended: true, <-ended: true}
	if !got["GET ended"] || !got["POST finished"] {
		t.Fatalf("got %v", got)
	}
}
