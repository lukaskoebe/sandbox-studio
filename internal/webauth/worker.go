package webauth

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Chromium fetches a module service worker's script, its static imports and its update checks
// without credentials, so the preview cookie never comes along and the gate refused them.
// Instead, the preview's pages load workerPatchPath, which adds a capability to the URL of a
// module worker they register, in WorkerParam. The gate takes it only on a same-origin
// service worker fetch, the kind of request only the browser can make, and removes it before
// the app sees the URL. The proxy appends it to the worker's static imports
// (internal/preview), so each of them carries it too.
const (
	// WorkerParam is the query parameter that carries the capability.
	WorkerParam = "__studio_sw"
	// WorkerPatchPath is the script the preview's pages load.
	WorkerPatchPath = "/__studio/preview-sw.js"
)

type workerTokenKey struct{}

// WorkerToken is the capability a request came in by, which the proxy gives the worker's
// imports; "" for every other request.
func WorkerToken(ctx context.Context) string {
	token, _ := ctx.Value(workerTokenKey{}).(string)
	return token
}

// workerToken is the capability of a preview host. It doesn't expire: the browser keeps the
// URL it registered and checks it for updates for as long as the worker stays.
func (a *Auth) workerToken(host string) string {
	return base64.RawURLEncoding.EncodeToString(mac(a.key, "preview-worker/v1", []byte(host)))
}

// takeWorkerToken returns r without WorkerParam in its query, so the app never sees it, valid
// or not, and whether it held host's capability on the kind of request a worker's script
// fetch makes; the returned request then carries the capability for WorkerToken.
func (a *Auth) takeWorkerToken(r *http.Request, host string) (*http.Request, bool) {
	query, token, found := withoutParam(r.URL.RawQuery, WorkerParam)
	if !found {
		return r, false
	}
	u := *r.URL
	u.RawQuery = query
	r = r.WithContext(r.Context())
	r.URL = &u
	r.RequestURI = u.RequestURI()
	if r.Method != http.MethodGet || r.Header.Get("Sec-Fetch-Dest") != "serviceworker" || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return r, false
	}
	want := a.workerToken(host)
	if !hmac.Equal([]byte(token), []byte(want)) {
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), workerTokenKey{}, want)), true
}

// withoutParam removes every name=value pair of name from a raw query, keeping the rest as it
// was written: a value-less "dev-sw" stays value-less, and the order stays.
func withoutParam(raw, name string) (query, value string, found bool) {
	if !strings.Contains(raw, name) {
		return raw, "", false
	}
	parts := strings.Split(raw, "&")
	kept := parts[:0]
	for _, part := range parts {
		key, v, _ := strings.Cut(part, "=")
		if key == name {
			if !found {
				value = v
			}
			found = true
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&"), value, found
}

// workerPatch adds the capability to the URL of a same-origin module worker the page
// registers, and keeps it out of the scriptURL the page reads back, so an app comparing it
// with the URL it registered, as Workbox does, sees its own. The parameter is appended as
// text: re-serializing the query would turn Vite's "?dev-sw" into "?dev-sw=".
const workerPatch = `(function(){var c=navigator.serviceWorker;if(!c||c.__studio)return;c.__studio=1;var p=%s,t=%s;
function strip(s){var i=s.indexOf("?");if(i<0)return s;var h=s.indexOf("#",i),q=s.slice(i+1,h<0?s.length:h).split("&").filter(function(x){return x.split("=")[0]!==p}).join("&");return s.slice(0,i)+(q?"?"+q:"")+(h<0?"":s.slice(h))}
var r=c.register;c.register=function(u,o){if(o&&o.type==="module"){try{var x=new URL(u,location.href);if(x.origin===location.origin){var h=x.hash;x.hash="";u=x.href+(x.search?"&":"?")+p+"="+t+h}}catch(e){}}var a=Array.prototype.slice.call(arguments);a[0]=u;return r.apply(this,a)};
var d=window.ServiceWorker&&Object.getOwnPropertyDescriptor(ServiceWorker.prototype,"scriptURL");if(d&&d.get){var g=d.get;Object.defineProperty(ServiceWorker.prototype,"scriptURL",{configurable:true,enumerable:d.enumerable,get:function(){return strip(g.call(this))}})}})();
`

func (a *Auth) serveWorkerPatch(w http.ResponseWriter, host string) {
	param, _ := json.Marshal(WorkerParam)
	token, _ := json.Marshal(a.workerToken(host))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = fmt.Fprintf(w, workerPatch, param, token)
}
