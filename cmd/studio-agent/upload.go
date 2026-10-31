package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// export runs one of the tree exports into exportDst, compressed with zstd if --zstd is given.
func export(run func(dst, warn io.Writer, zstd bool) error) error {
	dst, finish, err := exportDst()
	if err != nil {
		return err
	}
	return finish(run(dst, os.Stderr, slices.Contains(os.Args[2:], "--zstd")))
}

// exportDst is where an export writes its archive: stdout, or Studio's transfer host when
// Studio names one (agentproto.TransferURLEnv), which a fork, rebase or resize does since
// the guest's writes to msb exec are slow. finish ends the archive with the export's error
// and returns what became of it.
func exportDst() (w io.Writer, finish func(error) error, err error) {
	url := os.Getenv(agentproto.TransferURLEnv)
	if url == "" {
		return os.Stdout, func(err error) error { return err }, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(os.Getenv(agentproto.TransferCAEnv))) {
		return nil, nil, errors.New("no CA certificate for Studio's transfer host")
	}
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		WriteBufferSize: 1 << 20,
	}}
	sent := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			pr.CloseWithError(err)
			sent <- fmt.Errorf("upload: %w", err)
			return
		}
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode/100 != 2 {
			err = fmt.Errorf("upload: Studio answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
			pr.CloseWithError(err)
		}
		sent <- err
	}()
	return pw, func(err error) error {
		pw.CloseWithError(err) // nil ends the body
		if up := <-sent; err == nil {
			err = up
		}
		return err
	}, nil
}
