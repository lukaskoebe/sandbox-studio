package agentchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Browser runs one agent-browser command in the browser VM id (agentproto.KindBrowser).
func (h *Hub) Browser(ctx context.Context, id string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	var out agentproto.BrowserResult
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindBrowser}, cmd, &out)
	return out, err
}

// BrowserDownloads lists the downloads of one session of the browser VM id.
func (h *Hub) BrowserDownloads(ctx context.Context, id string, req agentproto.BrowserDownloadsRequest) (agentproto.BrowserDownloadList, error) {
	var out agentproto.BrowserDownloadList
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindBrowserDownloads}, req, &out)
	return out, err
}

// BrowserDownload reads the download req names in the browser VM id and passes it to fn with
// its body, which holds exactly the file's size. The body is only valid while fn runs: the
// stream closes when fn returns.
func (h *Hub) BrowserDownload(ctx context.Context, id string, req agentproto.BrowserDownloadsRequest,
	fn func(d agentproto.BrowserDownload, body io.Reader) error) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	c, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok {
		return ErrNotConnected
	}
	st, release, err := openLimitedStream(ctx, c.requestOpen, c.sess.Open, ErrRequestsBusy)
	if err != nil {
		return err
	}
	defer release()
	defer st.Close()
	if dl, ok := ctx.Deadline(); ok {
		st.SetDeadline(dl)
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = st.SetDeadline(time.Now())
		_ = st.Close()
	})
	defer stopCancel()
	defer func() {
		if err := ctx.Err(); err != nil {
			retErr = err
		}
	}()
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindBrowserDownloads}); err != nil {
		return err
	}
	if err := agentproto.WriteJSONLine(st, req); err != nil {
		return err
	}
	// One reader serves the header and the body: it may have read ahead into the body.
	br := bufio.NewReader(st)
	line, err := readHeaderLine(br, 64<<10)
	if err != nil {
		return err
	}
	var e agentproto.Error
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	var d agentproto.BrowserDownload
	if err := json.Unmarshal(line, &d); err != nil {
		return err
	}
	if d.Size < 0 || d.Size > agentproto.MaxUploadBytes {
		return fmt.Errorf("a download is at most %d MiB", agentproto.MaxUploadBytes>>20)
	}
	return fn(d, io.LimitReader(br, d.Size))
}

// readHeaderLine reads one line of at most max bytes. The guest is not trusted to end its
// header, so a line that runs on is refused rather than buffered.
func readHeaderLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > max {
			return nil, errors.New("guest reply line too long")
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}
