package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

func loginURL(addr, token string) string {
	return (&url.URL{Scheme: "http", Host: addr, Path: "/", Fragment: "studio-login=" + token}).String()
}

// requestLoginURL authenticates as the local installation owner. The control
// credential stays in the request header; only a short-lived ticket reaches the browser.
func requestLoginURL(ctx context.Context, addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", errors.New("login-url requires a loopback server address")
	}
	p, err := paths.Default()
	if err != nil {
		return "", err
	}
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		return "", fmt.Errorf("open Studio's catalog (start Studio first): %w", err)
	}
	defer st.Close()
	key, err := st.Setting(ctx, "web-auth-key")
	if err != nil {
		return "", fmt.Errorf("read Studio's authentication key (start Studio first): %w", err)
	}
	if len(key) != 32 {
		return "", errors.New("invalid Studio authentication key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/auth/launch", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+webauth.ControlToken(key))
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to Studio (is it running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Studio refused to issue a login link (HTTP %d)", resp.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil || result.Token == "" {
		return "", errors.New("Studio returned an invalid login link")
	}
	return loginURL(addr, result.Token), nil
}
