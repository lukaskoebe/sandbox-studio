package gateway

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A recording keeps the HTTP requests a sandbox makes over the connections Studio intercepts,
// with their responses, for the user to read while finding out what a tool sends. They can
// carry what the sandbox's own tools send, such as tokens of their own, cookies and what is
// typed into a browser's pages, so a recording is short and lives in memory only: it stops
// by itself after RecordFor, and what it kept goes recordKeep after it stopped, when the user
// clears it, when the sandbox is deleted, or when Studio stops. What it keeps is what the
// sandbox sent and got: placeholders, not the values Studio put in their place, which are
// masked wherever they appear.

const (
	// RecordFor is how long a recording runs unless the user stops it sooner.
	RecordFor = 15 * time.Minute
	// recordKeep is how long a recording's requests are kept once it stopped.
	recordKeep = time.Hour
	// recordSize is how many requests a recording keeps, the newest.
	recordSize = 200
	// recordBody is how much of a body is kept.
	recordBody = 64 << 10
)

// Header is one header line of a recorded request or response.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Body is what a recording kept of a request's or response's body.
type Body struct {
	Size      int64  `json:"size" doc:"The body's length in bytes, as sent"`
	Text      string `json:"text,omitempty" doc:"The body as text, decompressed, up to 64 KiB"`
	Truncated bool   `json:"truncated,omitempty" doc:"Text is only the start of the body"`
	Omitted   string `json:"omitted,omitempty" doc:"Why the body isn't shown, such as binary content or an encoding Studio can't read"`
}

// Exchange is one recorded request and its response.
type Exchange struct {
	ID       uint64     `json:"id"`
	ConnID   uint64     `json:"connId" doc:"The connection it was made on, in the connection log"`
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended,omitempty" doc:"When the response was complete; unset while it streams"`
	Method   string     `json:"method"`
	URL      string     `json:"url" doc:"As the sandbox asked for it"`
	Request  []Header   `json:"requestHeaders"`
	ReqBody  Body       `json:"requestBody"`
	Changed  []string   `json:"changed,omitempty" doc:"The request headers Studio set, changed or removed on the way, such as a rule's headers or secrets in place of placeholders, by name"`
	Secrets  []string   `json:"secrets,omitempty" doc:"The secrets whose values Studio sent in place of their placeholders"`
	Login    string     `json:"login,omitempty" doc:"The browser login Studio sent as Basic credentials"`
	Status   int        `json:"status" doc:"The response's status; 0 until it comes"`
	Response []Header   `json:"responseHeaders"`
	RespBody Body       `json:"responseBody"`
	Refused  string     `json:"refused,omitempty" doc:"Why Studio answered the request itself instead of passing it on"`
}

// RecordingState says whether a sandbox's traffic is being recorded.
type RecordingState struct {
	Recording bool       `json:"recording"`
	Until     *time.Time `json:"until,omitempty" doc:"When the recording stops by itself"`
	Expires   *time.Time `json:"expires,omitempty" doc:"When what it kept goes, if it isn't cleared sooner"`
	Count     int        `json:"count" doc:"How many requests it kept"`
}

// Recorder keeps the recordings of each sandbox. The zero value is ready.
type Recorder struct {
	mu        sync.Mutex
	next      uint64
	sandboxes map[string]*recording
	now       func() time.Time // replaced in tests
}

type recording struct {
	until, expires time.Time
	list           []*Exchange
	timer          *time.Timer // drops the recording at expires
}

func (r *Recorder) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// Start records the sandbox's intercepted traffic for d, keeping what an earlier recording
// kept.
func (r *Recorder) Start(sandboxID string, d time.Duration) RecordingState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sandboxes == nil {
		r.sandboxes = map[string]*recording{}
	}
	rec := r.sandboxes[sandboxID]
	if rec == nil {
		rec = &recording{}
		r.sandboxes[sandboxID] = rec
	}
	rec.until = r.clock().Add(d)
	r.expireLocked(sandboxID, rec, rec.until.Add(recordKeep))
	return r.stateLocked(rec)
}

// Stop ends the sandbox's recording; what it kept stays until it expires or is cleared.
func (r *Recorder) Stop(sandboxID string) RecordingState {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.sandboxes[sandboxID]
	if rec == nil {
		return RecordingState{}
	}
	if now := r.clock(); rec.until.After(now) {
		rec.until = now
		r.expireLocked(sandboxID, rec, now.Add(recordKeep))
	}
	return r.stateLocked(rec)
}

// Forget stops the sandbox's recording and drops what it kept.
func (r *Recorder) Forget(sandboxID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.sandboxes[sandboxID]; rec != nil {
		rec.timer.Stop()
		delete(r.sandboxes, sandboxID)
	}
}

// State says whether the sandbox's traffic is being recorded.
func (r *Recorder) State(sandboxID string) RecordingState {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.liveLocked(sandboxID)
	if rec == nil {
		return RecordingState{}
	}
	return r.stateLocked(rec)
}

// List returns what the sandbox's recording kept, newest first.
func (r *Recorder) List(sandboxID string) []Exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Exchange{}
	if rec := r.liveLocked(sandboxID); rec != nil {
		for i := len(rec.list) - 1; i >= 0; i-- {
			out = append(out, *rec.list[i])
		}
	}
	return out
}

// liveLocked is the sandbox's recording, unless it expired, which the timer may not have
// dropped yet.
func (r *Recorder) liveLocked(sandboxID string) *recording {
	rec := r.sandboxes[sandboxID]
	if rec != nil && !r.clock().Before(rec.expires) {
		rec.timer.Stop()
		delete(r.sandboxes, sandboxID)
		return nil
	}
	return rec
}

func (r *Recorder) stateLocked(rec *recording) RecordingState {
	st := RecordingState{Count: len(rec.list)}
	expires := rec.expires
	st.Expires = &expires
	if rec.until.After(r.clock()) {
		until := rec.until
		st.Recording, st.Until = true, &until
	}
	return st
}

func (r *Recorder) expireLocked(sandboxID string, rec *recording, at time.Time) {
	rec.expires = at
	if rec.timer != nil {
		rec.timer.Stop()
	}
	rec.timer = time.AfterFunc(time.Until(at), func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.sandboxes[sandboxID] == rec {
			r.liveLocked(sandboxID)
		}
	})
}

// begin keeps x if the sandbox's traffic is being recorded.
func (r *Recorder) begin(sandboxID string, x *Exchange) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.liveLocked(sandboxID)
	if rec == nil || !rec.until.After(r.clock()) {
		return false
	}
	r.next++
	x.ID, x.Started = r.next, r.clock()
	if len(rec.list) >= recordSize {
		rec.list = append(rec.list[:0], rec.list[1:]...)
	}
	rec.list = append(rec.list, x)
	return true
}

// Add keeps a complete exchange, as an interception does, if the sandbox's traffic is being
// recorded. The e2e control API makes requests up with it.
func (r *Recorder) Add(sandboxID string, x Exchange) bool {
	if !r.begin(sandboxID, &x) {
		return false
	}
	r.update(func() {
		if x.Ended == nil {
			now := r.clock()
			x.Ended = &now
		}
	})
	return true
}

// update changes a kept exchange, under the lock List copies it with.
func (r *Recorder) update(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

// recordTo is where an interception records its requests: the sandbox and the connection.
type recordTo struct {
	r       *Recorder
	sandbox string
	conn    uint64
}

// tap records one request and its response as they pass, as a ResponseWriter in front of the
// sandbox's.
type tap struct {
	http.ResponseWriter
	ic       *interception
	x        *Exchange
	req      *teeBody
	resp     capture
	answered bool
	refused  string
}

// tap starts recording r, or returns nil when the sandbox's traffic isn't recorded.
func (ic *interception) tap(w http.ResponseWriter, r *http.Request) *tap {
	u := *r.URL
	u.Scheme, u.Host = "http", r.Host
	if ic.tls {
		u.Scheme = "https"
	}
	if u.Host == "" {
		u.Host = ic.host
	}
	x := &Exchange{ConnID: ic.record.conn, Method: r.Method, URL: ic.mask(u.String()), Request: ic.recordHeaders(r.Header)}
	if !ic.record.r.begin(ic.record.sandbox, x) {
		return nil
	}
	t := &tap{ResponseWriter: w, ic: ic, x: x}
	if r.Body != nil && r.Body != http.NoBody {
		t.req = &teeBody{ReadCloser: r.Body, t: t, c: capture{limit: ic.captureLimit()}}
		r.Body = t.req
	}
	t.resp.limit = ic.captureLimit()
	return t
}

// recordHeaders are h as recorded: sorted, with secret values masked.
func (ic *interception) recordHeaders(h http.Header) []Header {
	out := []Header{}
	for name, values := range h {
		for _, v := range values {
			out = append(out, Header{Name: name, Value: ic.mask(v)})
		}
	}
	slices.SortStableFunc(out, func(a, b Header) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// captureLimit is how much of a body to read: what is kept, and enough more to mask a value
// the cut would split.
func (ic *interception) captureLimit() int {
	n := recordBody
	for _, v := range ic.masked {
		n = max(n, recordBody+len(v))
	}
	return n
}

// rewritten notes what Studio changed in the request it passes on.
func (t *tap) rewritten(in, out *http.Request, sent *sentSecrets) {
	var changed []string
	for name, values := range out.Header {
		if !slices.Equal(in.Header[name], values) {
			changed = append(changed, name)
		}
	}
	for name := range in.Header {
		if _, ok := out.Header[name]; !ok {
			changed = append(changed, name)
		}
	}
	if out.Host != in.Host {
		changed = append(changed, "Host")
	}
	slices.Sort(changed)
	t.ic.record.r.update(func() {
		t.x.Changed, t.x.Secrets, t.x.Login = changed, slices.Clone(sent.names), sent.login
	})
}

func (t *tap) WriteHeader(code int) {
	// 1xx answers, such as 103 Early Hints, come before the response.
	if !t.answered && (code >= 200 || code == http.StatusSwitchingProtocols) {
		t.answered = true
		headers := t.ic.recordHeaders(t.Header())
		t.ic.record.r.update(func() { t.x.Status, t.x.Response = code, headers })
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *tap) Write(p []byte) (int, error) {
	if !t.answered {
		t.WriteHeader(http.StatusOK)
	}
	t.resp.write(p)
	return t.ResponseWriter.Write(p)
}

// Unwrap lets the proxy flush and hijack the sandbox's ResponseWriter.
func (t *tap) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// done records the bodies once the response is complete.
func (t *tap) done() {
	now := t.ic.record.r.clock()
	if t.req != nil {
		t.req.record()
	}
	encoding := ""
	if t.answered {
		encoding = t.Header().Get("Content-Encoding")
	}
	resp := t.ic.body(&t.resp, encoding)
	t.ic.record.r.update(func() {
		t.x.Ended, t.x.RespBody, t.x.Refused = &now, resp, t.refused
	})
}

// body is what a recording keeps of a captured body, decompressed if it was sent with an
// encoding of encoding.
func (ic *interception) body(c *capture, encoding string) Body {
	b := Body{Size: c.size}
	if c.size == 0 {
		return b
	}
	data, truncated := c.buf.Bytes(), c.size > int64(c.buf.Len())
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			b.Omitted = "gzip that doesn't decompress"
			return b
		}
		data, truncated = decompressed(zr, c.limit, truncated)
	case "deflate":
		data, truncated = decompressed(flate.NewReader(bytes.NewReader(data)), c.limit, truncated)
	default:
		b.Omitted = "sent as " + encoding + ", which Studio doesn't decompress"
		return b
	}
	if len(ic.masked) > 0 {
		data = maskBytes(data, ic.masked)
	}
	if len(data) > recordBody {
		data, truncated = data[:recordBody], true
	}
	if truncated {
		// The cut may split a character.
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		b.Omitted = "binary"
		return b
	}
	b.Text, b.Truncated = string(data), truncated
	return b
}

// decompressed reads up to limit bytes of r; a capture cut short ends the stream early, which
// is no more than truncated.
func decompressed(r io.Reader, limit int, truncated bool) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if len(data) > limit {
		return data[:limit], true
	}
	return data, truncated || err != nil
}

// capture keeps the first limit bytes written to it and counts the rest.
type capture struct {
	buf   bytes.Buffer
	limit int
	size  int64
}

func (c *capture) write(p []byte) {
	c.size += int64(len(p))
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
}

// teeBody captures a request body as the proxy reads it. The transport reads and closes it on
// a goroutine of its own, which may outlast the response: the body is recorded as far as it
// was read when the response ends, and again when the transport closes it.
type teeBody struct {
	io.ReadCloser
	t  *tap
	mu sync.Mutex
	c  capture
}

func (b *teeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.c.write(p[:n])
	b.mu.Unlock()
	return n, err
}

func (b *teeBody) Close() error {
	err := b.ReadCloser.Close()
	b.record()
	return err
}

// record keeps the body as far as it was read. It holds the lock while it does, so that of the
// response's end and the transport's close, the later capture is the one kept.
func (b *teeBody) record() {
	b.mu.Lock()
	defer b.mu.Unlock()
	body := b.t.ic.body(&b.c, "")
	b.t.ic.record.r.update(func() { b.t.x.ReqBody = body })
}
