// Package webauth provides host-bound authentication for the Studio web UI and
// short-lived credentials for preview hosts.
package webauth

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// SessionCookie stores the host-bound Studio session.
	SessionCookie = "studio_session"
	// PreviewCookie stores the host-bound preview authorization.
	PreviewCookie = "studio_preview"

	loginTicketLifetime   = 10 * time.Minute
	previewTicketLifetime = time.Minute
	sessionLifetime       = 30 * 24 * time.Hour
	previewSessionLife    = 8 * time.Hour
	maxOutstandingTickets = 1024
	maxExchangeBody       = 4096
	maxSignedCookieBytes  = 2048
)

var errInvalidPreviewHost = errors.New("not a preview host")

type ticketKind uint8

const (
	loginTicket ticketKind = iota + 1
	previewTicket
)

type ticket struct {
	kind    ticketKind
	host    string
	expires time.Time
}

// Auth owns an install key and a bounded, in-memory registry of one-time
// launch and preview tickets.
type Auth struct {
	key       []byte
	isPreview func(string) bool
	now       func() time.Time

	mu      sync.Mutex
	tickets map[string]*list.Element
	order   list.List
}

type ticketEntry struct {
	token  string
	ticket ticket
}

// New creates an authenticator using a persistent 32-byte install key.
func New(key []byte, isPreview func(string) bool) (*Auth, error) {
	if len(key) != 32 {
		return nil, errors.New("install key must be 32 bytes")
	}
	return &Auth{
		key:       append([]byte(nil), key...),
		isPreview: isPreview,
		now:       time.Now,
		tickets:   make(map[string]*list.Element),
	}, nil
}

// IssueLogin creates a one-time login token that expires after ten minutes.
// It returns an empty token only if the operating system random source fails.
func (a *Auth) IssueLogin() string {
	token, err := a.issue(loginTicket, "", loginTicketLifetime)
	if err != nil {
		return ""
	}
	return token
}

// IssuePreview creates a one-time ticket for a preview host. The supplied host
// is stored in lowercase, including its port, and must pass isPreview.
func (a *Auth) IssuePreview(host string) (string, error) {
	if a.isPreview == nil || !a.isPreview(host) {
		return "", errInvalidPreviewHost
	}
	return a.issue(previewTicket, strings.ToLower(host), previewTicketLifetime)
}

func (a *Auth) issue(kind ticketKind, host string, lifetime time.Duration) (string, error) {
	for {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(raw[:])
		if token == ControlToken(a.key) {
			continue
		}

		a.mu.Lock()
		now := a.now()
		a.pruneLocked(now)
		if _, exists := a.tickets[token]; exists {
			a.mu.Unlock()
			continue
		}
		for len(a.tickets) >= maxOutstandingTickets {
			a.removeLocked(a.order.Front())
		}
		entry := &ticketEntry{token: token, ticket: ticket{kind: kind, host: host, expires: now.Add(lifetime)}}
		a.tickets[token] = a.order.PushBack(entry)
		a.mu.Unlock()
		return token, nil
	}
}

func (a *Auth) pruneLocked(now time.Time) {
	for el := a.order.Front(); el != nil; {
		next := el.Next()
		entry := el.Value.(*ticketEntry)
		if !now.Before(entry.ticket.expires) {
			a.removeLocked(el)
		}
		el = next
	}
}

func (a *Auth) removeLocked(el *list.Element) {
	if el == nil {
		return
	}
	entry := el.Value.(*ticketEntry)
	delete(a.tickets, entry.token)
	a.order.Remove(el)
}

func (a *Auth) consume(token string, kind ticketKind, host string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneLocked(now)
	el := a.tickets[token]
	if el == nil {
		return false
	}
	entry := el.Value.(*ticketEntry)
	if entry.ticket.kind != kind || !now.Before(entry.ticket.expires) || (kind == previewTicket && entry.ticket.host != host) {
		return false
	}
	a.removeLocked(el)
	return true
}

// ControlToken returns the install-key credential accepted only by
// POST /api/auth/launch. Invalid key lengths produce an empty token.
func ControlToken(key []byte) string {
	if len(key) != 32 {
		return ""
	}
	sum := mac(key, "control-token/v1", []byte("issue-login"))
	return base64.RawURLEncoding.EncodeToString(sum)
}

// Middleware serves the exact authentication endpoints, protects every other
// /api path, and gates preview hosts with their separate preview cookie.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		apiPath := isAPIPath(path)
		if apiPath {
			noStore(w)
		}

		host := strings.ToLower(r.Host)
		if isMainHost(r.Host) {
			if a.serveAuthEndpoint(w, r, host) {
				return
			}
			if apiPath && !a.validCookie(r, SessionCookie, "session", host) {
				writeAuthRequired(w)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if a.previewHost(r.Host) {
			if path == "/__studio/preview-login" {
				a.servePreviewLogin(w, r, host)
				return
			}
			if !a.validCookie(r, PreviewCookie, "preview", host) {
				http.Error(w, "Open this preview from Sandbox Studio.", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "unknown host", http.StatusMisdirectedRequest)
	})
}

func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

func (a *Auth) previewHost(host string) bool {
	return a.isPreview != nil && a.isPreview(host)
}

func (a *Auth) serveAuthEndpoint(w http.ResponseWriter, r *http.Request, host string) bool {
	switch r.URL.Path {
	case "/api/auth/status":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		writeJSON(w, http.StatusOK, struct {
			Authenticated bool `json:"authenticated"`
		}{Authenticated: a.validCookie(r, SessionCookie, "session", host)})
		return true
	case "/api/auth/exchange":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		a.exchange(w, r, host)
		return true
	case "/api/auth/launch":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		a.launch(w, r)
		return true
	case "/api/auth/logout":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		if !a.validCookie(r, SessionCookie, "session", host) {
			writeAuthRequired(w)
			return true
		}
		http.SetCookie(w, &http.Cookie{
			Name: SessionCookie, Value: "", Path: "/", Expires: time.Unix(1, 0), MaxAge: -1,
			HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
		})
		w.WriteHeader(http.StatusNoContent)
		return true
	default:
		return false
	}
}

type exchangeBody struct {
	Token string `json:"token"`
}

func (a *Auth) exchange(w http.ResponseWriter, r *http.Request, host string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxExchangeBody))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var input exchangeBody
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Token == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !a.consume(input.Token, loginTicket, "") {
		writeAuthRequired(w)
		return
	}
	a.setSignedCookie(w, r, SessionCookie, "session", host, a.now().Add(sessionLifetime), http.SameSiteStrictMode)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) launch(w http.ResponseWriter, r *http.Request) {
	if !hasControlToken(r.Header.Get("Authorization"), ControlToken(a.key)) {
		writeAuthRequired(w)
		return
	}
	token := a.IssueLogin()
	if token == "" {
		http.Error(w, "could not issue login token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
	}{Token: token})
}

func hasControlToken(header, expected string) bool {
	if expected == "" || !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (a *Auth) servePreviewLogin(w http.ResponseWriter, r *http.Request, host string) {
	noStore(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	values, ok := r.URL.Query()["token"]
	if !ok || len(values) != 1 || !a.consume(values[0], previewTicket, host) {
		http.Error(w, "Open this preview from Sandbox Studio.", http.StatusForbidden)
		return
	}
	a.setSignedCookie(w, r, PreviewCookie, "preview", host, a.now().Add(previewSessionLife), http.SameSiteLaxMode)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *Auth) setSignedCookie(w http.ResponseWriter, r *http.Request, name, kind, host string, expires time.Time, sameSite http.SameSite) {
	payload := cookiePayload(kind, host, expires.Unix())
	signature := mac(a.key, "cookie/"+kind+"/v1", payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	maxAge := int(expires.Sub(a.now()).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", Expires: expires, MaxAge: maxAge,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: sameSite,
	})
}

func (a *Auth) validCookie(r *http.Request, name, kind, host string) bool {
	value, ok := singleCookie(r, name)
	if !ok || len(value) > maxSignedCookieBytes {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want := mac(a.key, "cookie/"+kind+"/v1", payload)
	if !hmac.Equal(signature, want) {
		return false
	}
	fields := strings.Split(string(payload), ".")
	if len(fields) != 4 || fields[0] != "v1" || fields[1] != kind {
		return false
	}
	expires, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || a.now().Unix() >= expires {
		return false
	}
	boundHost, err := base64.RawURLEncoding.DecodeString(fields[3])
	return err == nil && string(boundHost) == host
}

func singleCookie(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			value = cookie.Value
			count++
		}
	}
	return value, count == 1
}

func cookiePayload(kind, host string, expires int64) []byte {
	encodedHost := base64.RawURLEncoding.EncodeToString([]byte(host))
	return []byte("v1." + kind + "." + strconv.FormatInt(expires, 10) + "." + encodedHost)
}

func writeAuthRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, struct {
		Status int    `json:"status"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}{http.StatusUnauthorized, "Authentication required", "Open a Studio launch link to connect."})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func isMainHost(hostport string) bool {
	host := hostport
	if parsedHost, port, err := net.SplitHostPort(hostport); err == nil {
		if !validPort(port) {
			return false
		}
		host = parsedHost
	} else if strings.Contains(hostport, ":") {
		if strings.HasPrefix(hostport, "[") && strings.HasSuffix(hostport, "]") {
			host = hostport[1 : len(hostport)-1]
		} else if net.ParseIP(hostport) == nil {
			return false
		}
	}
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 1 && value <= 65535
}

func mac(key []byte, domain string, message []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = io.WriteString(h, "sandbox-studio/")
	_, _ = io.WriteString(h, domain)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(message)
	return h.Sum(nil)
}
