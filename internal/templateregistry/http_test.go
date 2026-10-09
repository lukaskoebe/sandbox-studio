package templateregistry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const (
	httpTestAddr     = "127.0.0.1:8123"
	httpTestEnvID    = "aaaaaaaaaaaaa"
	httpTestOtherEnv = "ccccccccccccc"
	httpTestTemplate = "bbbbbbbbbbbbb"
	httpTestDigest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	httpTestMedia    = "application/vnd.oci.image.manifest.v1+json"
	httpTestUser     = "registry-test-user"
	httpTestPassword = "registry-test-password"
)

type httpBackendStub struct {
	username     string
	password     string
	environment  string
	authErr      error
	open         func(envID, templateID, kind, digest string) (*os.File, templateimage.Descriptor, error)
	authCalls    int
	openCalls    int
	openedFiles  []*os.File
	openedScopes []artifactRoute
}

func (b *httpBackendStub) Authenticate(_ context.Context, username, password string) (string, error) {
	b.authCalls++
	if b.authErr != nil {
		return "", b.authErr
	}
	if username != b.username || password != b.password {
		return "", errors.New("invalid registry credentials")
	}
	return b.environment, nil
}

func (b *httpBackendStub) OpenArtifact(_ context.Context, envID, templateID, kind, digest string) (*os.File, templateimage.Descriptor, error) {
	b.openCalls++
	b.openedScopes = append(b.openedScopes, artifactRoute{
		environmentID: envID,
		templateID:    templateID,
		kind:          kind,
		digest:        digest,
	})
	if b.open == nil {
		return nil, templateimage.Descriptor{}, errors.New("unexpected artifact open")
	}
	file, descriptor, err := b.open(envID, templateID, kind, digest)
	if file != nil {
		b.openedFiles = append(b.openedFiles, file)
	}
	return file, descriptor, err
}

func newHTTPBackendStub() *httpBackendStub {
	return &httpBackendStub{
		username:    httpTestUser,
		password:    httpTestPassword,
		environment: httpTestEnvID,
	}
}

func newHTTPHandler(backend Backend) *Handler {
	return &Handler{Backend: backend, Addr: httpTestAddr}
}

func httpRegistryRequest(method, target string, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = httpTestAddr
	if authenticated {
		r.SetBasicAuth(httpTestUser, httpTestPassword)
	}
	return r
}

func checkHTTPRegistryHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Docker-Distribution-API-Version"); got != registryAPIVersion {
		t.Errorf("Docker-Distribution-API-Version = %q, want %q", got, registryAPIVersion)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
	for name := range response.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("unexpected CORS header %q", name)
		}
	}
}

func TestValidID(t *testing.T) {
	for _, test := range []struct {
		id   string
		want bool
	}{
		{id: "aaaaaaaaaaaaa", want: true},
		{id: "zzzzzzzzzzzzz", want: true},
		{id: "aaaaaaaaaaaa", want: false},
		{id: "AAAAAAAAAAAAA", want: false},
		{id: "aaaaaaaaaaa0a", want: false},
		{id: "aaaaaaaaaaa8a", want: false},
	} {
		if got := validID(test.id); got != test.want {
			t.Errorf("validID(%q) = %t, want %t", test.id, got, test.want)
		}
	}
}

func TestHandlerPingRequiresBasicAuthentication(t *testing.T) {
	for _, test := range []struct {
		name    string
		auth    func(*http.Request)
		authErr error
		want    int
		secret  string
	}{
		{
			name: "missing credentials",
			want: http.StatusUnauthorized,
		},
		{
			name: "unknown user",
			auth: func(r *http.Request) { r.SetBasicAuth("unknown-user", "credential-secret") },
			want: http.StatusUnauthorized, secret: "credential-secret",
		},
		{
			name: "wrong password",
			auth: func(r *http.Request) { r.SetBasicAuth(httpTestUser, "credential-secret") },
			want: http.StatusUnauthorized, secret: "credential-secret",
		},
		{
			name:    "credential has no environment",
			authErr: store.ErrNotFound,
			want:    http.StatusUnauthorized,
		},
		{
			name: "bearer credentials",
			auth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer credential-secret") },
			want: http.StatusUnauthorized, secret: "credential-secret",
		},
		{
			name: "browser cookie is not registry authentication",
			auth: func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "studio_session", Value: "cookie-secret"}) },
			want: http.StatusUnauthorized, secret: "cookie-secret",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newHTTPBackendStub()
			backend.authErr = test.authErr
			recorder := httptest.NewRecorder()
			recorder.Header().Set("Access-Control-Allow-Origin", "*")
			recorder.Header().Set("Access-Control-Allow-Credentials", "true")
			req := httpRegistryRequest(http.MethodGet, "/v2/", false)
			if test.auth != nil {
				test.auth(req)
			} else if test.authErr != nil {
				req.SetBasicAuth(httpTestUser, httpTestPassword)
			}

			newHTTPHandler(backend).ServeHTTP(recorder, req)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body %q", recorder.Code, test.want, recorder.Body.String())
			}
			if got := recorder.Header().Get("WWW-Authenticate"); got != authRealm {
				t.Errorf("WWW-Authenticate = %q, want %q", got, authRealm)
			}
			if test.secret != "" && strings.Contains(recorder.Body.String()+recorder.Header().Get("WWW-Authenticate"), test.secret) {
				t.Errorf("response echoed credential %q", test.secret)
			}
			if recorder.Body.Len() == 0 {
				t.Error("unauthorized response unexpectedly has an empty body")
			}
			if backend.openCalls != 0 {
				t.Errorf("OpenArtifact calls = %d, want 0", backend.openCalls)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run("authenticated ping "+method, func(t *testing.T) {
			backend := newHTTPBackendStub()
			recorder := httptest.NewRecorder()
			newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(method, "/v2/", true))
			if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Length") != "0" {
				t.Errorf("authenticated ping = status %d, body %q, content-length %q; want 200, empty body, length 0", recorder.Code, recorder.Body.String(), recorder.Header().Get("Content-Length"))
			}
			if backend.authCalls != 1 || backend.openCalls != 0 {
				t.Errorf("ping calls: Authenticate=%d OpenArtifact=%d, want 1 and 0", backend.authCalls, backend.openCalls)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}
}

func TestHandlerHostGuardAndAddressValidation(t *testing.T) {
	t.Run("host must exactly match configured address", func(t *testing.T) {
		backend := newHTTPBackendStub()
		req := httpRegistryRequest(http.MethodGet, "/v2/", true)
		req.Host = "localhost:8123"
		recorder := httptest.NewRecorder()
		newHTTPHandler(backend).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", recorder.Code)
		}
		if backend.authCalls != 0 || backend.openCalls != 0 {
			t.Errorf("host mismatch reached backend: Authenticate=%d OpenArtifact=%d", backend.authCalls, backend.openCalls)
		}
		checkHTTPRegistryHeaders(t, recorder)
	})

	for _, addr := range []string{"localhost:8123", "0.0.0.0:8123", "127.0.0.1:0", "127.000.0.1:8123", "127.0.0.1:08123"} {
		t.Run("invalid address "+addr, func(t *testing.T) {
			backend := newHTTPBackendStub()
			req := httpRegistryRequest(http.MethodGet, "/v2/", true)
			recorder := httptest.NewRecorder()
			(&Handler{Backend: backend, Addr: addr}).ServeHTTP(recorder, req)
			if recorder.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", recorder.Code)
			}
			if backend.authCalls != 0 {
				t.Errorf("invalid configured address reached Authenticate %d times", backend.authCalls)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}
}

func TestHandlerMethodsAndRequestTargetValidation(t *testing.T) {
	t.Run("unsupported method", func(t *testing.T) {
		backend := newHTTPBackendStub()
		recorder := httptest.NewRecorder()
		newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodPost, "/v2/", true))
		if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("status = %d, Allow = %q; want 405 and GET, HEAD", recorder.Code, recorder.Header().Get("Allow"))
		}
		if backend.authCalls != 0 || backend.openCalls != 0 {
			t.Errorf("unsupported method reached backend: Authenticate=%d OpenArtifact=%d", backend.authCalls, backend.openCalls)
		}
		checkHTTPRegistryHeaders(t, recorder)
	})

	for _, target := range []string{
		"/v2/?debug=true",
		"/v2/studio/aaaaaaaaaaaaa/bbbbbbbbbbbbb/blobs%2fsha256%3a" + strings.Repeat("a", 64),
		"/v2/studio/aaaaaaaaaaaaa/bbbbbbbbbbbbb/blobs%5csha256%3a" + strings.Repeat("a", 64),
		"/v2/%73tudio/aaaaaaaaaaaaa/bbbbbbbbbbbbb/blobs/" + httpTestDigest,
		"/v2/studio/../aaaaaaaaaaaaa/bbbbbbbbbbbbb/blobs/" + httpTestDigest,
	} {
		t.Run(target, func(t *testing.T) {
			backend := newHTTPBackendStub()
			recorder := httptest.NewRecorder()
			newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %q", recorder.Code, recorder.Body.String())
			}
			if backend.openCalls != 0 {
				t.Errorf("invalid target reached OpenArtifact %d times", backend.openCalls)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}

	t.Run("empty query marker", func(t *testing.T) {
		backend := newHTTPBackendStub()
		req := httpRegistryRequest(http.MethodGet, "/v2/", true)
		req.URL.ForceQuery = true
		recorder := httptest.NewRecorder()
		newHTTPHandler(backend).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", recorder.Code)
		}
	})
}

func TestHandlerUnknownRoutesAndEnvironmentScope(t *testing.T) {
	for _, target := range []string{
		"/v2/_catalog",
		"/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/manifests/latest",
		"/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/blobs/sha512:" + strings.Repeat("a", 128),
		"/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/blobs/uploads/",
		"/v2/" + httpTestDigest,
		"/v2/studio/UPPERCASEID/" + httpTestTemplate + "/blobs/" + httpTestDigest,
	} {
		t.Run(target, func(t *testing.T) {
			backend := newHTTPBackendStub()
			recorder := httptest.NewRecorder()
			newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
			if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"NAME_UNKNOWN"`) {
				t.Errorf("status/body = %d %q, want OCI NAME_UNKNOWN 404", recorder.Code, recorder.Body.String())
			}
			if backend.openCalls != 0 {
				t.Errorf("unknown route reached OpenArtifact %d times", backend.openCalls)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}

	backend := newHTTPBackendStub()
	backend.open = func(string, string, string, string) (*os.File, templateimage.Descriptor, error) {
		return nil, templateimage.Descriptor{}, errors.New("must not open cross-environment artifact")
	}
	target := "/v2/studio/" + httpTestOtherEnv + "/" + httpTestTemplate + "/blobs/" + httpTestDigest
	recorder := httptest.NewRecorder()
	newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"NAME_UNKNOWN"`) {
		t.Errorf("cross-environment status/body = %d %q, want OCI NAME_UNKNOWN 404", recorder.Code, recorder.Body.String())
	}
	if backend.openCalls != 0 {
		t.Errorf("cross-environment request reached OpenArtifact %d times", backend.openCalls)
	}
	checkHTTPRegistryHeaders(t, recorder)
}

func TestHandlerServesArtifactsWithHeadAndRanges(t *testing.T) {
	content := []byte("0123456789")
	backend := newHTTPBackendStub()
	backend.open = func(_, _, _, digest string) (*os.File, templateimage.Descriptor, error) {
		file := writeHTTPArtifactFile(t, content)
		return file, templateimage.Descriptor{MediaType: httpTestMedia, Digest: digest, Size: int64(len(content))}, nil
	}
	handler := newHTTPHandler(backend)
	target := "/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/blobs/" + httpTestDigest

	for _, test := range []struct {
		name        string
		method      string
		rangeHeader string
		wantStatus  int
		wantBody    string
		wantLength  string
		wantRange   string
	}{
		{name: "get", method: http.MethodGet, wantStatus: http.StatusOK, wantBody: string(content), wantLength: "10"},
		{name: "head", method: http.MethodHead, wantStatus: http.StatusOK, wantLength: "10"},
		{name: "range", method: http.MethodGet, rangeHeader: "bytes=2-5", wantStatus: http.StatusPartialContent, wantBody: "2345", wantLength: "4", wantRange: "bytes 2-5/10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httpRegistryRequest(test.method, target, true)
			if test.rangeHeader != "" {
				req.Header.Set("Range", test.rangeHeader)
			}
			recorder := httptest.NewRecorder()
			opensBeforeRequest := backend.openCalls
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.wantStatus || recorder.Body.String() != test.wantBody {
				t.Errorf("status/body = %d %q, want %d %q", recorder.Code, recorder.Body.String(), test.wantStatus, test.wantBody)
			}
			if got := recorder.Header().Get("Content-Type"); got != httpTestMedia {
				t.Errorf("Content-Type = %q, want %q", got, httpTestMedia)
			}
			if got := recorder.Header().Get("Content-Length"); got != test.wantLength {
				t.Errorf("Content-Length = %q, want %q", got, test.wantLength)
			}
			if got := recorder.Header().Get("Docker-Content-Digest"); got != httpTestDigest {
				t.Errorf("Docker-Content-Digest = %q, want %q", got, httpTestDigest)
			}
			if got := recorder.Header().Get("ETag"); got != `"`+httpTestDigest+`"` {
				t.Errorf("ETag = %q, want quoted digest", got)
			}
			if got := recorder.Header().Get("Content-Range"); got != test.wantRange {
				t.Errorf("Content-Range = %q, want %q", got, test.wantRange)
			}
			if got := recorder.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("Accept-Ranges = %q, want bytes", got)
			}
			if backend.openCalls != opensBeforeRequest+1 {
				t.Fatalf("OpenArtifact calls = %d, want %d", backend.openCalls, opensBeforeRequest+1)
			}
			lastFile := backend.openedFiles[len(backend.openedFiles)-1]
			if _, err := lastFile.Stat(); err == nil {
				t.Error("opened artifact file remains open after response")
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}

	if backend.openCalls != 3 {
		t.Errorf("OpenArtifact calls = %d, want 3", backend.openCalls)
	}
	for i, call := range backend.openedScopes {
		if call.environmentID != httpTestEnvID || call.templateID != httpTestTemplate || call.kind != "blobs" || call.digest != httpTestDigest {
			t.Errorf("OpenArtifact call %d scope = %+v, want request scope", i, call)
		}
	}
}

func TestHandlerArtifactErrorsAreSanitized(t *testing.T) {
	for _, test := range []struct {
		name       string
		kind       string
		open       func(*testing.T) (*os.File, templateimage.Descriptor, error)
		wantStatus int
		wantCode   string
		wantSecret string
	}{
		{
			name: "store not found maps to manifest unknown",
			kind: "manifests",
			open: func(*testing.T) (*os.File, templateimage.Descriptor, error) {
				return nil, templateimage.Descriptor{}, store.ErrNotFound
			},
			wantStatus: http.StatusNotFound, wantCode: "MANIFEST_UNKNOWN",
		},
		{
			name: "missing blob maps to blob unknown",
			kind: "blobs",
			open: func(*testing.T) (*os.File, templateimage.Descriptor, error) {
				return nil, templateimage.Descriptor{}, fs.ErrNotExist
			},
			wantStatus: http.StatusNotFound, wantCode: "BLOB_UNKNOWN",
		},
		{
			name: "missing source maps to manifest unknown",
			kind: "manifests",
			open: func(*testing.T) (*os.File, templateimage.Descriptor, error) {
				return nil, templateimage.Descriptor{}, fmt.Errorf("/private/data/secret: %w", fs.ErrNotExist)
			},
			wantStatus: http.StatusNotFound, wantCode: "MANIFEST_UNKNOWN", wantSecret: "/private/data/secret",
		},
		{
			name: "other backend errors become generic server errors",
			kind: "blobs",
			open: func(*testing.T) (*os.File, templateimage.Descriptor, error) {
				return nil, templateimage.Descriptor{}, errors.New("/private/data/secret: permission denied")
			},
			wantStatus: http.StatusInternalServerError, wantSecret: "/private/data/secret",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newHTTPBackendStub()
			backend.open = func(_, _, _, _ string) (*os.File, templateimage.Descriptor, error) {
				return test.open(t)
			}
			target := "/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/" + test.kind + "/" + httpTestDigest
			recorder := httptest.NewRecorder()
			newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d; body %q", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.wantCode != "" && !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Errorf("body %q does not contain OCI code %q", recorder.Body.String(), test.wantCode)
			}
			if test.wantSecret != "" && strings.Contains(recorder.Body.String(), test.wantSecret) {
				t.Errorf("response exposed backend detail %q", test.wantSecret)
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}
}

func TestHandlerRejectsInvalidOpenedArtifactsAndClosesFiles(t *testing.T) {
	content := []byte("artifact")
	for _, test := range []struct {
		name       string
		file       func(*testing.T) *os.File
		descriptor func(*os.File) templateimage.Descriptor
	}{
		{
			name: "digest mismatch",
			file: func(t *testing.T) *os.File { return writeHTTPArtifactFile(t, content) },
			descriptor: func(*os.File) templateimage.Descriptor {
				return templateimage.Descriptor{MediaType: httpTestMedia, Digest: "sha256:" + strings.Repeat("b", 64), Size: int64(len(content))}
			},
		},
		{
			name: "size mismatch",
			file: func(t *testing.T) *os.File { return writeHTTPArtifactFile(t, content) },
			descriptor: func(*os.File) templateimage.Descriptor {
				return templateimage.Descriptor{MediaType: httpTestMedia, Digest: httpTestDigest, Size: int64(len(content) + 1)}
			},
		},
		{
			name: "media type rejects trailing CRLF whitespace",
			file: func(t *testing.T) *os.File { return writeHTTPArtifactFile(t, content) },
			descriptor: func(*os.File) templateimage.Descriptor {
				return templateimage.Descriptor{
					MediaType: "application/json\r\n",
					Digest:    httpTestDigest,
					Size:      int64(len(content)),
				}
			},
		},
		{
			name: "media type rejects CRLF header injection",
			file: func(t *testing.T) *os.File { return writeHTTPArtifactFile(t, content) },
			descriptor: func(*os.File) templateimage.Descriptor {
				return templateimage.Descriptor{
					MediaType: "application/json\r\nX-Injected: yes",
					Digest:    httpTestDigest,
					Size:      int64(len(content)),
				}
			},
		},
		{
			name: "media type rejects NUL",
			file: func(t *testing.T) *os.File { return writeHTTPArtifactFile(t, content) },
			descriptor: func(*os.File) templateimage.Descriptor {
				return templateimage.Descriptor{
					MediaType: "application/json\x00",
					Digest:    httpTestDigest,
					Size:      int64(len(content)),
				}
			},
		},
		{
			name: "nonregular file",
			file: func(t *testing.T) *os.File {
				file, err := os.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				return file
			},
			descriptor: func(file *os.File) templateimage.Descriptor {
				info, err := file.Stat()
				if err != nil {
					t.Fatal(err)
				}
				return templateimage.Descriptor{MediaType: httpTestMedia, Digest: httpTestDigest, Size: info.Size()}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newHTTPBackendStub()
			var opened *os.File
			backend.open = func(_, _, _, _ string) (*os.File, templateimage.Descriptor, error) {
				opened = test.file(t)
				return opened, test.descriptor(opened), nil
			}
			target := "/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/blobs/" + httpTestDigest
			recorder := httptest.NewRecorder()
			newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
			if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != "internal server error\n" {
				t.Errorf("status/body = %d %q, want generic 500", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("X-Injected"); got != "" {
				t.Errorf("invalid media type injected an X-Injected header: %q", got)
			}
			if opened == nil {
				t.Fatal("OpenArtifact did not return a file")
			}
			if _, err := opened.Stat(); err == nil {
				t.Error("invalid artifact file remains open after response")
			}
			checkHTTPRegistryHeaders(t, recorder)
		})
	}

	t.Run("file returned with an error is still closed", func(t *testing.T) {
		backend := newHTTPBackendStub()
		var opened *os.File
		backend.open = func(_, _, _, _ string) (*os.File, templateimage.Descriptor, error) {
			opened = writeHTTPArtifactFile(t, content)
			return opened, templateimage.Descriptor{}, errors.New("backend failed after opening")
		}
		target := "/v2/studio/" + httpTestEnvID + "/" + httpTestTemplate + "/blobs/" + httpTestDigest
		recorder := httptest.NewRecorder()
		newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, target, true))
		if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != "internal server error\n" {
			t.Errorf("status/body = %d %q, want generic 500", recorder.Code, recorder.Body.String())
		}
		if opened == nil {
			t.Fatal("OpenArtifact did not return a file")
		}
		if _, err := opened.Stat(); err == nil {
			t.Error("file returned with an error remains open")
		}
	})
}

func TestHandlerAuthenticationErrorIsSanitizedUnauthorized(t *testing.T) {
	backend := newHTTPBackendStub()
	backend.authErr = errors.New("credential database path /private/auth.db is unavailable")
	recorder := httptest.NewRecorder()
	newHTTPHandler(backend).ServeHTTP(recorder, httpRegistryRequest(http.MethodGet, "/v2/", true))
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), "/private/auth.db") {
		t.Errorf("status/body = %d %q, want generic 401", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("WWW-Authenticate"); got != authRealm {
		t.Errorf("WWW-Authenticate = %q, want %q", got, authRealm)
	}
	checkHTTPRegistryHeaders(t, recorder)
}

func writeHTTPArtifactFile(t *testing.T, content []byte) *os.File {
	t.Helper()
	path := t.TempDir() + "/artifact"
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
