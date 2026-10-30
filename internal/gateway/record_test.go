package gateway

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// A recording keeps what the sandbox sent over its intercepted connections and what it got
// back: placeholders, not the values Studio put in, the headers Studio changed by name, and
// the secrets it sent. Studio's own refusals say so.
func TestRecordingKeepsInterceptedRequests(t *testing.T) {
	h := newInterceptHarness(t)
	h.gw.Records = &Recorder{}
	c := h.client(t, h.envCA)

	get(t, c, "https://example.com/before")
	if n := len(h.gw.Records.List("sb1")); n != 0 {
		t.Fatalf("recorded %d requests before recording", n)
	}
	st := h.gw.Records.Start("sb1", RecordFor)
	if !st.Recording || st.Until == nil || st.Expires == nil || !st.Expires.After(*st.Until) {
		t.Fatalf("state %+v", st)
	}

	req, _ := http.NewRequest("POST", "https://example.com/v1/x?key="+apiPlaceholder, strings.NewReader(`{"token":"`+apiPlaceholder+`"}`))
	req.Header.Set("Authorization", "Bearer "+apiPlaceholder)
	req.Header.Set("Accept-Encoding", "gzip")
	do(t, c, req)
	get(t, c, "https://upgradable.example/gzip", "Accept-Encoding", "gzip")
	get(t, c, "https://proxied.example/headers")
	get(t, c, "https://badref.example/x")

	list := h.gw.Records.List("sb1")
	if len(list) != 4 {
		t.Fatalf("recorded %d requests: %+v", len(list), list)
	}
	post := list[3]
	if post.Method != "POST" || post.URL != "https://example.com/v1/x?key="+apiPlaceholder || post.Status != 200 || post.Ended == nil || post.ConnID == 0 {
		t.Errorf("post %+v", post)
	}
	if v := header(post.Request, "Authorization"); v != "Bearer "+apiPlaceholder {
		t.Errorf("request Authorization recorded as %q", v)
	}
	if post.ReqBody.Text != `{"token":"`+apiPlaceholder+`"}` || post.ReqBody.Size != int64(len(post.ReqBody.Text)) {
		t.Errorf("request body %+v", post.ReqBody)
	}
	if !slices.Equal(post.Secrets, []string{"API_KEY"}) || !slices.Contains(post.Changed, "Authorization") || !slices.Contains(post.Changed, "Accept-Encoding") {
		t.Errorf("secrets %v, changed %v", post.Secrets, post.Changed)
	}
	// The echo carries the value back, masked: the recording has what the sandbox got.
	for _, x := range list {
		for _, s := range []string{x.URL, x.ReqBody.Text, x.RespBody.Text, fmt.Sprint(x.Request), fmt.Sprint(x.Response)} {
			if strings.Contains(s, apiValue) || strings.Contains(s, headerValue) {
				t.Errorf("%s %s: the recording leaks a value: %q", x.Method, x.URL, s)
			}
		}
	}
	if !strings.Contains(post.RespBody.Text, "Bearer "+strings.Repeat("*", len(apiValue))) || header(post.Response, "X-Echo-Auth") != "Bearer "+strings.Repeat("*", len(apiValue)) {
		t.Errorf("response %+v %v", post.RespBody, post.Response)
	}

	// Without secrets of the host's, the response stays compressed; the recording reads it.
	gz := list[2]
	if header(gz.Response, "Content-Encoding") != "gzip" || !strings.HasPrefix(gz.RespBody.Text, "GET /gzip?") || gz.RespBody.Omitted != "" {
		t.Errorf("gzip %+v %v", gz.RespBody, gz.Response)
	}

	proxied := list[1]
	if !slices.Equal(proxied.Changed, []string{"X-Api-Key", "X-Static"}) || header(proxied.Request, "X-Api-Key") != "" {
		t.Errorf("proxied: changed %v, headers %v", proxied.Changed, proxied.Request)
	}

	refused := list[0]
	if refused.Status != http.StatusForbidden || !strings.Contains(refused.Refused, "API_KEY") || !strings.Contains(refused.RespBody.Text, refused.Refused) {
		t.Errorf("refused %+v", refused)
	}

	// Stopped, it keeps what it has but no more; forgotten, it has nothing.
	if st := h.gw.Records.Stop("sb1"); st.Recording || st.Count != 4 {
		t.Errorf("stopped: %+v", st)
	}
	get(t, c, "https://example.com/after")
	if n := len(h.gw.Records.List("sb1")); n != 4 {
		t.Errorf("recorded %d requests after stopping", n)
	}
	h.gw.Records.Forget("sb1")
	if n := len(h.gw.Records.List("sb1")); n != 0 || h.gw.Records.State("sb1").Count != 0 {
		t.Errorf("a forgotten sandbox kept %d requests", n)
	}
}

// A recording stops by itself, and what it kept goes an hour later.
func TestRecordingExpires(t *testing.T) {
	now := time.Now()
	r := &Recorder{now: func() time.Time { return now }}
	r.Start("sb1", time.Minute)
	if !r.begin("sb1", &Exchange{Method: "GET"}) {
		t.Fatal("not recording")
	}
	now = now.Add(2 * time.Minute)
	if st := r.State("sb1"); st.Recording || st.Count != 1 {
		t.Errorf("after the recording ended: %+v", st)
	}
	if r.begin("sb1", &Exchange{Method: "GET"}) {
		t.Error("recorded after the recording ended")
	}
	now = now.Add(recordKeep)
	if st, list := r.State("sb1"), r.List("sb1"); st.Count != 0 || st.Expires != nil || len(list) != 0 {
		t.Errorf("expired, it kept %+v %v", st, list)
	}
	// Restarted before it expires, it keeps what it had.
	r.Start("sb1", time.Minute)
	r.begin("sb1", &Exchange{Method: "GET"})
	r.Stop("sb1")
	r.Start("sb1", time.Minute)
	if n := r.State("sb1").Count; n != 1 {
		t.Errorf("a restarted recording kept %d", n)
	}
}

func TestRecordedBodies(t *testing.T) {
	ic := &interception{masked: [][]byte{[]byte("secret-value")}}
	body := func(data []byte, encoding string) Body {
		c := &capture{limit: ic.captureLimit()}
		c.write(data)
		return ic.body(c, encoding)
	}
	// A value the cut splits is masked as a whole: the capture reads past the cut.
	long := append(bytes.Repeat([]byte("a"), recordBody-4), []byte("secret-value")...)
	if b := body(long, ""); !b.Truncated || len(b.Text) != recordBody || strings.Contains(b.Text, "secr") || b.Size != int64(len(long)) {
		t.Errorf("long body: truncated %v, %d bytes, tail %q", b.Truncated, len(b.Text), b.Text[len(b.Text)-8:])
	}
	if b := body([]byte{0x89, 'P', 'N', 'G', 0, 1}, ""); b.Omitted != "binary" || b.Text != "" {
		t.Errorf("binary %+v", b)
	}
	if b := body([]byte("x"), "br"); !strings.Contains(b.Omitted, "br") {
		t.Errorf("brotli %+v", b)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write([]byte("hello secret-value")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if b := body(gz.Bytes(), "gzip"); b.Text != "hello ************" || b.Size != int64(gz.Len()) {
		t.Errorf("gzip %+v", b)
	}
	// A split multi-byte character at the cut is dropped, not shown broken.
	split := append(bytes.Repeat([]byte("a"), recordBody-1), []byte("é…")...)
	if b := body(split, ""); b.Omitted != "" || !b.Truncated || len(b.Text) != recordBody-1 {
		t.Errorf("split character: %+v (%d bytes)", b.Omitted, len(b.Text))
	}
}

func header(list []Header, name string) string {
	for _, h := range list {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
