package webauth

import (
	"crypto/hmac"
	"encoding/base32"
	"strings"
)

// A webview such as the desktop app's keeps no cookie for a framed preview: WebKitGTK's
// libsoup refuses SameSite=None without Secure, and Secure over http, also on *.localhost,
// and macOS's WKWebView blocks third-party cookies. So the app's preview panel frames a
// host that carries the preview's key in its first label instead, <port>-<id>-<key>.localhost,
// and the gate lets in what comes to it with the right key. The key stays the same for a
// preview host, so the preview keeps its origin, and its storage, across loads; it is as good
// as a login for that preview alone, for whoever learns the address.

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// hostKey is the key of a preview host, port included: 128 bits of a MAC of it.
func (a *Auth) hostKey(host string) string {
	return strings.ToLower(keyEncoding.EncodeToString(mac(a.key, "preview-host/v1", []byte(host))[:16]))
}

// KeyedHost is a preview host, with its port, carrying its key.
func (a *Auth) KeyedHost(host string) (string, error) {
	host = strings.ToLower(host)
	if !a.previewHost(host) {
		return "", errInvalidPreviewHost
	}
	if keyed, _ := a.keyedHost(host); keyed {
		return "", errInvalidPreviewHost
	}
	first, rest, _ := strings.Cut(host, ".")
	keyedHost := first + "-" + a.hostKey(host) + "." + rest
	if !a.previewHost(keyedHost) {
		return "", errInvalidPreviewHost
	}
	return keyedHost, nil
}

// keyedHost reports whether host, lowercase, carries a key, and whether it is the host's.
func (a *Auth) keyedHost(host string) (keyed, valid bool) {
	first, rest, _ := strings.Cut(host, ".")
	i := strings.LastIndexByte(first, '-')
	if i < 0 || strings.Count(first, "-") < 2 {
		return false, false
	}
	key, base := first[i+1:], first[:i]+"."+rest
	return true, hmac.Equal([]byte(key), []byte(a.hostKey(base)))
}
