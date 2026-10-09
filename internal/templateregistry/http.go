// Package templateregistry exposes read-only OCI Distribution access to
// templates owned by a single environment.
package templateregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const (
	registryAPIVersion = "registry/2.0"
	authRealm          = `Basic realm="Sandbox Studio templates"`
	maxPort            = 65535
)

// Backend supplies credentials and opens one already-authorized OCI artifact.
// Implementations must scope all artifact lookups to envID.
type Backend interface {
	Authenticate(ctx context.Context, username, password string) (environmentID string, err error)
	OpenArtifact(ctx context.Context, envID, templateID, kind, digest string) (*os.File, templateimage.Descriptor, error)
}

// Handler serves the read-only OCI Distribution API on Addr. Addr must be the
// canonical IPv4 loopback address and port of the listener serving this handler.
type Handler struct {
	Backend Backend
	Addr    string
}

type artifactRoute struct {
	environmentID string
	templateID    string
	kind          string
	digest        string
}

// ServeHTTP implements the small read-only subset of OCI Distribution used by
// the template client.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setBaseHeaders(w.Header())

	if !canonicalLoopbackAddr(h.Addr) {
		writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		return
	}
	if r.Host != h.Addr {
		writeText(w, r, http.StatusBadRequest, "bad request\n")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeText(w, r, http.StatusMethodNotAllowed, "method not allowed\n")
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		writeUnauthorized(w, r)
		return
	}
	if h.Backend == nil {
		writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		return
	}
	environmentID, err := h.Backend.Authenticate(r.Context(), username, password)
	if err != nil {
		writeUnauthorized(w, r)
		return
	}

	if r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
		writeText(w, r, http.StatusBadRequest, "bad request\n")
		return
	}
	if !canonicalRequestPath(r) {
		writeText(w, r, http.StatusBadRequest, "bad request\n")
		return
	}

	if r.URL.Path == "/v2/" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}

	route, found := parseArtifactRoute(r.URL.Path)
	if !found {
		writeOCIError(w, r, http.StatusNotFound, "NAME_UNKNOWN", "repository name unknown")
		return
	}
	if route.environmentID != environmentID {
		writeOCIError(w, r, http.StatusNotFound, "NAME_UNKNOWN", "repository name unknown")
		return
	}

	file, descriptor, err := h.Backend.OpenArtifact(r.Context(), route.environmentID, route.templateID, route.kind, route.digest)
	if err != nil {
		if file != nil {
			_ = file.Close()
		}
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			code, message := artifactUnknown(route.kind)
			writeOCIError(w, r, http.StatusNotFound, code, message)
		} else {
			writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		}
		return
	}
	if file == nil {
		writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		return
	}
	defer file.Close()

	if descriptor.Digest != route.digest || descriptor.Size < 0 || !validMediaType(descriptor.MediaType) {
		writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		return
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.Size {
		writeText(w, r, http.StatusInternalServerError, "internal server error\n")
		return
	}

	w.Header().Set("Content-Type", descriptor.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(descriptor.Size, 10))
	w.Header().Set("Docker-Content-Digest", descriptor.Digest)
	w.Header().Set("ETag", `"`+descriptor.Digest+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, "", time.Time{}, file)
}

func setBaseHeaders(header http.Header) {
	header.Set("Docker-Distribution-API-Version", registryAPIVersion)
	header.Set("Cache-Control", "private, no-store")
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			header.Del(name)
		}
	}
}

func canonicalLoopbackAddr(addr string) bool {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !ip.IsLoopback() || ip.String() != host {
		return false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	return err == nil && port > 0 && port <= maxPort && strconv.FormatUint(port, 10) == portText
}

func canonicalRequestPath(r *http.Request) bool {
	if r.URL.Opaque != "" || r.URL.RawPath != "" || strings.ContainsRune(r.URL.Path, '\\') {
		return false
	}
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	if strings.Contains(r.RequestURI, "%") {
		return false
	}
	if r.RequestURI != "" {
		rawPath := r.RequestURI
		if cut := strings.IndexAny(rawPath, "?#"); cut >= 0 {
			rawPath = rawPath[:cut]
		}
		if rawPath != r.URL.Path {
			return false
		}
	}
	return true
}

func parseArtifactRoute(path string) (artifactRoute, bool) {
	segments := strings.Split(path, "/")
	if len(segments) != 7 || segments[0] != "" || segments[1] != "v2" || segments[2] != "studio" {
		return artifactRoute{}, false
	}
	if !validID(segments[3]) || !validID(segments[4]) {
		return artifactRoute{}, false
	}
	kind := segments[5]
	if kind != "manifests" && kind != "blobs" {
		return artifactRoute{}, false
	}
	digest := segments[6]
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		return artifactRoute{}, false
	}
	for _, c := range digest[len("sha256:"):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return artifactRoute{}, false
		}
	}
	return artifactRoute{
		environmentID: segments[3],
		templateID:    segments[4],
		kind:          kind,
		digest:        digest,
	}, true
}

func validID(id string) bool {
	if len(id) != 13 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

func validMediaType(mediaType string) bool {
	if !validHeaderFieldValue(mediaType) {
		return false
	}
	parsed, _, err := mime.ParseMediaType(mediaType)
	return err == nil && parsed != ""
}

func validHeaderFieldValue(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == 0 || b == '\r' || b == '\n' || b == 0x7f || (b < 0x20 && b != '\t') {
			return false
		}
	}
	return true
}

func artifactUnknown(kind string) (code, message string) {
	if kind == "manifests" {
		return "MANIFEST_UNKNOWN", "manifest unknown"
	}
	return "BLOB_UNKNOWN", "blob unknown"
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", authRealm)
	writeText(w, r, http.StatusUnauthorized, "unauthorized\n")
}

func writeText(w http.ResponseWriter, r *http.Request, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = fmt.Fprint(w, body)
	}
}

type ociErrorResponse struct {
	Errors []ociError `json:"errors"`
}

type ociError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeOCIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	body, _ := json.Marshal(ociErrorResponse{Errors: []ociError{{Code: code, Message: message}}})
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
