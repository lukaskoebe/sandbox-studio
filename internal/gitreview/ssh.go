package gitreview

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/crypto/ssh"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// SSH forges: Studio signs with the host's SSH agent (agent.go) or with an ed25519 key it
// generated, sealed in the vault, and pins the forge's host key when the forge is added.
// Studio is the SSH client; sandboxes speak smart HTTP to the virtual remote as for every
// forge, and Studio bridges each request to a git-upload-pack or git-receive-pack run over
// SSH. Studio never forwards an agent, opens no other channels and sets no environment
// variable other than GIT_PROTOCOL.

// sshTimeout bounds connecting and the SSH handshake.
const sshTimeout = 30 * time.Second

// hostKeyAlgorithms are the host key types Studio accepts, most preferred first.
var hostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

// algorithmsFor are the host key algorithms that verify a key of the pinned type.
func algorithmsFor(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{keyType}
}

// GenerateKey makes an ed25519 key for an SSH forge. It returns the private key as an
// OpenSSH PEM block, for the vault, and the public key in authorized_keys form with
// comment, for the forge's deploy keys.
func GenerateKey(comment string) (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return string(pem.EncodeToMemory(block)), authorized, nil
}

// HostKey is a forge's SSH host key as scanned.
type HostKey struct {
	Key         string `json:"hostKey" doc:"The host key in authorized_keys form"`
	Fingerprint string `json:"fingerprint" doc:"Its SHA256 fingerprint, to compare with the forge's published one"`
	Algorithm   string `json:"algorithm"`
	PublishedBy string `json:"publishedBy,omitempty" doc:"The forge that publishes this key, such as GitHub, when Studio has its published keys built in"`
}

// CheckHostKey parses a host key in authorized_keys form and returns it normalized.
func CheckHostKey(s string) (HostKey, error) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return HostKey{}, fmt.Errorf("%w: the host key is not an SSH public key", ErrInvalid)
	}
	switch k.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSA:
	default:
		return HostKey{}, fmt.Errorf("%w: host keys of type %s are not supported", ErrInvalid, k.Type())
	}
	return hostKey(k), nil
}

func hostKey(k ssh.PublicKey) HostKey {
	return HostKey{Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))), Fingerprint: ssh.FingerprintSHA256(k), Algorithm: k.Type()}
}

var errScanned = errors.New("host key scanned")

func (s *Service) dial(ctx context.Context, addr string) (net.Conn, error) {
	d := s.Dial
	if d == nil {
		d = gateway.DialPublic
	}
	ctx, cancel := context.WithTimeout(ctx, sshTimeout)
	defer cancel()
	return d(ctx, "tcp", addr)
}

// handshake runs the SSH handshake on conn, bounded by sshTimeout and ctx.
func handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	conn.SetDeadline(time.Now().Add(sshTimeout))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	stop()
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// ScanHostKey connects to an SSH forge and returns its host key, without authenticating.
// The user confirms the fingerprint before Studio pins it.
func (s *Service) ScanHostKey(ctx context.Context, baseURL string) (ForgeURL, HostKey, error) {
	u, err := ParseSSHURL(baseURL)
	if err != nil {
		return u, HostKey{}, err
	}
	addr := net.JoinHostPort(u.Host, strconv.Itoa(u.Port))
	conn, err := s.dial(ctx, addr)
	if err != nil {
		return u, HostKey{}, fmt.Errorf("could not reach %s: %w", addr, err)
	}
	var got ssh.PublicKey
	_, err = handshake(ctx, conn, addr, &ssh.ClientConfig{
		User:              u.User,
		HostKeyAlgorithms: hostKeyAlgorithms,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = k
			return errScanned
		},
	})
	if got == nil {
		return u, HostKey{}, fmt.Errorf("%s did not offer a host key: %w", addr, err)
	}
	hk, err := CheckPublished(u, hostKey(got))
	return u, hk, err
}

// HostKeyChangedError is a forge offering another host key than the pinned one.
type HostKeyChangedError struct {
	Host, Pinned, Offered string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("the host key of %s changed (pinned %s, offered %s); check with the host's admin, then re-pin it in Settings → Git",
		e.Host, e.Pinned, e.Offered)
}

// sshClient connects to f and authenticates with key, or with the host's SSH agent for
// auth agent, accepting only the pinned host key.
// A different host key is recorded on the forge, so Settings → Git can offer to re-pin it.
func (s *Service) sshClient(ctx context.Context, f store.Forge, key string) (*ssh.Client, error) {
	u, err := ParseSSHURL(f.BaseURL)
	if err != nil {
		return nil, err
	}
	pinned, _, _, _, err := ssh.ParseAuthorizedKey([]byte(f.HostKey))
	if err != nil {
		return nil, fmt.Errorf("forge %s has no valid pinned host key; remove and add it again", f.Name)
	}
	var auth ssh.AuthMethod
	if f.Auth == store.ForgeAuthAgent {
		var done func()
		if auth, done, err = s.agentAuth(ctx, f); err != nil {
			return nil, err
		}
		defer done()
	} else {
		signer, err := ssh.ParsePrivateKey([]byte(key))
		if err != nil {
			return nil, fmt.Errorf("forge %s: Studio's key: %w", f.Name, err)
		}
		auth = ssh.PublicKeys(signer)
	}
	addr := net.JoinHostPort(u.Host, strconv.Itoa(u.Port))
	conn, err := s.dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", addr, err)
	}
	var changed *HostKeyChangedError
	c, err := handshake(ctx, conn, addr, &ssh.ClientConfig{
		User:              u.User,
		Auth:              []ssh.AuthMethod{auth},
		HostKeyAlgorithms: algorithmsFor(pinned.Type()),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if bytes.Equal(k.Marshal(), pinned.Marshal()) {
				return nil
			}
			changed = &HostKeyChangedError{Host: u.Host, Pinned: ssh.FingerprintSHA256(pinned), Offered: ssh.FingerprintSHA256(k)}
			return changed
		},
	})
	switch {
	case changed != nil:
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.Store.SetForgeHostKeyOffered(rctx, f.EnvironmentID, f.ID, changed.Offered); err != nil && s.Log != nil {
			s.Log.Error("git remote: record offered host key", "forge", f.Name, "err", err)
		}
		return nil, changed
	case err != nil && strings.Contains(err.Error(), "no common algorithm for host key"):
		return nil, fmt.Errorf("%s no longer offers a %s host key; check with the host's admin, then re-pin it in Settings → Git", u.Host, pinned.Type())
	case err != nil && refusedAuth(err) && f.Auth == store.ForgeAuthAgent:
		return nil, fmt.Errorf("%s refused the keys in your SSH agent: add your public key to your account on the host, with write access to push", u.Host)
	case err != nil && refusedAuth(err):
		return nil, fmt.Errorf("%s refused Studio's key: add the public key shown in Settings → Git to the repository or account, with write access to push", u.Host)
	case err != nil:
		return nil, fmt.Errorf("SSH to %s: %w", addr, err)
	}
	return c, nil
}

// refusedAuth reports whether the host refused the keys offered. A server past its attempt
// limit (OpenSSH's MaxAuthTries, six by default) disconnects instead of saying so.
func refusedAuth(err error) bool {
	return strings.Contains(err.Error(), "unable to authenticate") || strings.Contains(err.Error(), "too many authentication failures")
}

// sshCommand is the command line that runs service for path, quoted as git quotes it.
func sshCommand(service, path string) string {
	// splitPath allows no quotes in path, so single quotes need no escaping.
	return service + " '" + path + "'"
}

// tail keeps the last bytes written to it, for the remote's error messages.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4<<10 {
		t.buf = t.buf[len(t.buf)-4<<10:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// sshProcess is a git service running on the forge over SSH.
type sshProcess struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
	stderr *tail
	stop   func() bool
}

// start runs service for path on f. protocol is the GIT_PROTOCOL to ask for, if any; a
// server that doesn't accept the variable speaks protocol v0.
func (s *Service) startSSH(ctx context.Context, f store.Forge, key, service, path, protocol string) (*sshProcess, error) {
	c, err := s.sshClient(ctx, f, key)
	if err != nil {
		return nil, err
	}
	p := &sshProcess{client: c, stderr: &tail{}}
	fail := func(err error) (*sshProcess, error) {
		c.Close()
		return nil, err
	}
	if p.sess, err = c.NewSession(); err != nil {
		return fail(err)
	}
	if protocol != "" {
		_ = p.sess.Setenv("GIT_PROTOCOL", protocol)
	}
	if p.stdin, err = p.sess.StdinPipe(); err != nil {
		return fail(err)
	}
	if p.stdout, err = p.sess.StdoutPipe(); err != nil {
		return fail(err)
	}
	p.sess.Stderr = p.stderr
	if err := p.sess.Start(sshCommand(service, path)); err != nil {
		return fail(err)
	}
	p.stop = context.AfterFunc(ctx, func() { c.Close() })
	return p, nil
}

func (p *sshProcess) Close() error {
	p.stop()
	return p.client.Close()
}

// failure explains why the remote service ended early, from what it wrote to stderr.
func (p *sshProcess) failure(path string) error {
	done := make(chan struct{})
	go func() { _ = p.sess.Wait(); close(done) }() // stderr tells why, not its exit status
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	return remoteError(p.stderr.String(), path)
}

func remoteError(msg, path string) error {
	lower := strings.ToLower(msg)
	switch {
	case msg == "":
		return fmt.Errorf("the forge closed the connection for %s without saying why", path)
	case strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "does not appear to be a git repository") || strings.Contains(lower, "no such"):
		return fmt.Errorf("%w: %s", transport.ErrRepositoryNotFound, msg)
	case strings.Contains(lower, "denied") || strings.Contains(lower, "permission") || strings.Contains(lower, "not allowed"):
		return fmt.Errorf("%w: %s", transport.ErrAuthorizationFailed, msg)
	}
	return fmt.Errorf("the forge said: %s", msg)
}

// --- go-git transport, for the staging repositories -----------------------------------------

// sshTransport is a go-git transport to one repository of an SSH forge, speaking protocol
// v0 as go-git does. It lets fetchMirror and pushUpstream work the same for every kind.
type sshTransport struct {
	s    *Service
	f    store.Forge
	key  string
	path string
}

func (s *Service) sshRemote(t target, key string) (*remote, error) {
	ep, err := transport.NewEndpoint(t.forge.BaseURL + "/" + t.path())
	if err != nil {
		return nil, err
	}
	return &remote{ep: ep, tr: &sshTransport{s: s, f: t.forge, key: key, path: t.path()}}, nil
}

func (tr *sshTransport) NewUploadPackSession(*transport.Endpoint, transport.AuthMethod) (transport.UploadPackSession, error) {
	return &sshSession{tr: tr, service: transport.UploadPackServiceName}, nil
}

func (tr *sshTransport) NewReceivePackSession(*transport.Endpoint, transport.AuthMethod) (transport.ReceivePackSession, error) {
	return &sshSession{tr: tr, service: transport.ReceivePackServiceName}, nil
}

// sshSession is go-git's stateful session over one SSH exec channel. It connects when
// the advertisement is first asked for, with that call's context.
type sshSession struct {
	tr      *sshTransport
	service string
	p       *sshProcess
	ar      *packp.AdvRefs
	packRun bool
}

func (ss *sshSession) AdvertisedReferences() (*packp.AdvRefs, error) {
	return ss.AdvertisedReferencesContext(context.Background())
}

func (ss *sshSession) AdvertisedReferencesContext(ctx context.Context) (*packp.AdvRefs, error) {
	if ss.ar != nil {
		return ss.ar, nil
	}
	if ss.p == nil {
		p, err := ss.tr.s.startSSH(ctx, ss.tr.f, ss.tr.key, ss.service, ss.tr.path, "")
		if err != nil {
			return nil, err
		}
		ss.p = p
	}
	receive := ss.service == transport.ReceivePackServiceName
	ar := packp.NewAdvRefs()
	err := ar.Decode(ss.p.stdout)
	var errLine *pktline.ErrorLine
	switch {
	case err == nil:
	case errors.Is(err, packp.ErrEmptyAdvRefs) && receive:
	case errors.Is(err, packp.ErrEmptyAdvRefs):
		return nil, transport.ErrEmptyRemoteRepository
	case errors.As(err, &errLine):
		return nil, remoteError(errLine.Text, ss.tr.path)
	case errors.Is(err, packp.ErrEmptyInput):
		ss.packRun = true // nothing to say goodbye to
		return nil, ss.p.failure(ss.tr.path)
	default:
		return nil, err
	}
	if !receive && ar.IsEmpty() {
		return nil, transport.ErrEmptyRemoteRepository
	}
	transport.FilterUnsupportedCapabilities(ar.Capabilities)
	ss.ar = ar
	return ar, nil
}

func (ss *sshSession) UploadPack(ctx context.Context, req *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	if req.IsEmpty() {
		return nil, transport.ErrEmptyUploadPackRequest
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if _, err := ss.AdvertisedReferencesContext(ctx); err != nil {
		return nil, err
	}
	ss.packRun = true
	if err := req.UploadRequest.Encode(ss.p.stdin); err != nil {
		return nil, err
	}
	if err := req.UploadHaves.Encode(ss.p.stdin, true); err != nil {
		return nil, err
	}
	if err := pktline.NewEncoder(ss.p.stdin).Encodef("done\n"); err != nil {
		return nil, err
	}
	if err := ss.p.stdin.Close(); err != nil {
		return nil, err
	}
	resp := packp.NewUploadPackResponse(req)
	if err := resp.Decode(io.NopCloser(ss.p.stdout)); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ss.p.failure(ss.tr.path)
		}
		return nil, fmt.Errorf("upload-pack response: %w", err)
	}
	return resp, nil
}

func (ss *sshSession) ReceivePack(ctx context.Context, req *packp.ReferenceUpdateRequest) (*packp.ReportStatus, error) {
	if _, err := ss.AdvertisedReferencesContext(ctx); err != nil {
		return nil, err
	}
	ss.packRun = true
	if err := req.Encode(ss.p.stdin); err != nil {
		return nil, err
	}
	if err := ss.p.stdin.Close(); err != nil {
		return nil, err
	}
	if !req.Capabilities.Supports(capability.ReportStatus) {
		return nil, ss.p.sess.Wait()
	}
	r := ss.p.stdout
	switch {
	case req.Capabilities.Supports(capability.Sideband64k):
		r = sideband.NewDemuxer(sideband.Sideband64k, r)
	case req.Capabilities.Supports(capability.Sideband):
		r = sideband.NewDemuxer(sideband.Sideband, r)
	}
	report := packp.NewReportStatus()
	if err := report.Decode(r); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ss.p.failure(ss.tr.path)
		}
		return nil, err
	}
	return report, report.Error()
}

func (ss *sshSession) Close() error {
	if ss.p == nil {
		return nil
	}
	if !ss.packRun {
		// Goodbye without a request: a flush, as git does.
		_, _ = ss.p.stdin.Write(pktline.FlushPkt)
		ss.p.stdin.Close()
	}
	return ss.p.Close()
}

// --- the sandbox's fetches ----------------------------------------------------------------

// sshUploadPack bridges one smart HTTP request of a fetch to git-upload-pack over SSH.
// Smart HTTP is stateless: every request starts a new upload-pack, as git-http-backend
// does. With protocol v2 (the sandbox's git asks for it in the Git-Protocol header) each
// command is self-contained, so the bridge discards the capability advertisement and
// relays the command. Protocol v0 works the same way, because the stateless client resends
// its wants and common haves in every round; upload-pack answers the round and then ends
// on the closed input as a stateless upload-pack would.
func (s *Service) sshUploadPack(w http.ResponseWriter, r *http.Request, t target, key string) {
	ctx := r.Context()
	protocol := ""
	if strings.Contains(r.Header.Get("Git-Protocol"), "version=2") {
		protocol = "version=2"
	}
	p, err := s.startSSH(ctx, t.forge, key, transport.UploadPackServiceName, t.path(), protocol)
	if err != nil {
		var changed *HostKeyChangedError
		code := http.StatusBadGateway
		if errors.As(err, &changed) {
			code = http.StatusForbidden
		}
		refuse(w, code, "Sandbox Studio could not connect to the forge: "+err.Error())
		return
	}
	defer p.Close()
	adv, v2, err := readAdvertisement(p.stdout)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			err = p.failure(t.path())
		}
		code := http.StatusBadGateway
		if errors.Is(err, transport.ErrRepositoryNotFound) {
			code = http.StatusNotFound
		}
		refuse(w, code, "The forge refused the fetch: "+strings.TrimPrefix(err.Error(), transport.ErrRepositoryNotFound.Error()+": "))
		return
	}
	if protocol != "" && !v2 && t.op != "info/refs" {
		// The advertisement over HTTP said v2, so this server takes GIT_PROTOCOL on some
		// connections only. It can't be bridged half-way.
		refuse(w, http.StatusBadGateway, "The forge stopped speaking git protocol v2 between requests. Fetch again, or set protocol.version=0 in the sandbox.")
		return
	}
	if t.op == "info/refs" {
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if !v2 {
			e := pktline.NewEncoder(w)
			_ = e.EncodeString("# service=git-upload-pack\n")
			e.Flush()
		}
		_, _ = w.Write(adv)
		_, _ = p.stdin.Write(pktline.FlushPkt) // ends upload-pack, which is closed next anyway
		p.stdin.Close()
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, s.Limits.MaxRequest)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			refuse(w, http.StatusBadRequest, "The fetch request is not valid gzip.")
			return
		}
		body = io.LimitReader(zr, s.Limits.MaxRequest)
	}
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(p.stdin, body)
		p.stdin.Close()
		copied <- err
	}()
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	wrote := false
	for {
		n, err := p.stdout.Read(buf)
		if n > 0 {
			if !wrote {
				w.WriteHeader(http.StatusOK)
				wrote = true
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			rc.Flush()
		}
		if err != nil {
			break
		}
	}
	if !wrote {
		if err := <-copied; err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				refuse(w, http.StatusRequestEntityTooLarge, "The fetch request is too large.")
				return
			}
		}
		refuse(w, http.StatusBadGateway, "The forge ended the fetch: "+p.failure(t.path()).Error())
	}
}

// readAdvertisement reads upload-pack's advertisement up to its flush and returns it
// pkt-line encoded, and whether it is protocol v2's capability advertisement.
func readAdvertisement(r io.Reader) ([]byte, bool, error) {
	var out bytes.Buffer
	sc := pktline.NewScanner(r)
	e := pktline.NewEncoder(&out)
	first, v2 := true, false
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			e.Flush()
			return out.Bytes(), v2, nil
		}
		if first {
			v2 = string(bytes.TrimSuffix(line, []byte("\n"))) == "version 2"
			if bytes.HasPrefix(line, []byte("ERR ")) {
				return nil, false, remoteError(string(bytes.TrimSpace(line[4:])), "")
			}
			first = false
		}
		if err := e.Encode(line); err != nil {
			return nil, false, err
		}
		if out.Len() > 16<<20 {
			return nil, false, errors.New("the advertisement is too large")
		}
	}
	if err := sc.Err(); err != nil {
		var errLine *pktline.ErrorLine
		if errors.As(err, &errLine) {
			return nil, false, remoteError(errLine.Text, "")
		}
		return nil, false, err
	}
	return nil, false, io.ErrUnexpectedEOF
}
