package main

import (
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestExportDstUploads(t *testing.T) {
	var got string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		if r.URL.Path == "/full" {
			http.Error(w, "workspace does not fit the target disk", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	t.Setenv(agentproto.TransferCAEnv, string(ca))

	t.Setenv(agentproto.TransferURLEnv, srv.URL+"/ok")
	w, finish, err := exportDst()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "archive"); err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil || got != "archive" {
		t.Fatalf("err %v, server got %q", err, got)
	}

	t.Setenv(agentproto.TransferURLEnv, srv.URL+"/full")
	w, finish, err = exportDst()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "archive")
	if err := finish(nil); err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatalf("err = %v", err)
	}

	// An export that fails sends no complete body, and its error is the one returned.
	t.Setenv(agentproto.TransferURLEnv, srv.URL+"/ok")
	w, finish, err = exportDst()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "arch")
	broken := errors.New("tar broke")
	if err := finish(broken); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}

	t.Setenv(agentproto.TransferCAEnv, "")
	if _, _, err := exportDst(); err == nil {
		t.Fatal("it uploaded without a CA")
	}
}
