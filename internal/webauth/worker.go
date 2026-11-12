package webauth

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	// WorkerPatchPath is the script the preview's pages load: the worker patch, and the
	// bridge to the preview panel's address bar (previewBridge).
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

// previewBridge lets the preview panel's address bar follow a framed preview, whose address
// Studio can't read across origins. It talks only to Studio's own page: the frame's parent, on
// a loopback host at the preview's port (the frame's ancestor, where the browser names it).
// It reports the page's path as it loads and on each pushState, replaceState, popstate and
// hashchange, says when the page starts to leave, and hands a click on a link to another
// site to Studio, which opens it in a new tab or switches to the sandbox port it names. It
// takes back, forward and go-to-a-path commands, the path on the preview's own origin.
const previewBridge = `(function(){if(window.parent===window||window.__studioNav)return;window.__studioNav=1;
var T="studio-preview-nav",s=location.protocol+"//",q=location.port?":"+location.port:"",ok=[s+"localhost"+q,s+"127.0.0.1"+q,s+"[::1]"+q],a=location.ancestorOrigins;
if(a&&a.length)ok=ok.indexOf(a[0])>=0?[a[0]]:[];
function post(m){m.type=T;for(var i=0;i<ok.length;i++)try{parent.postMessage(m,ok[i])}catch(e){}}
function report(){var n=window.navigation;post({event:"location",path:location.pathname+location.search+location.hash,back:n?n.canGoBack:null,forward:n?n.canGoForward:null})}
["pushState","replaceState"].forEach(function(k){var f=history[k];history[k]=function(){var r=f.apply(this,arguments);report();return r}});
addEventListener("popstate",report);addEventListener("hashchange",report);addEventListener("pageshow",report);
addEventListener("beforeunload",function(){post({event:"loading"})});
if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",report);else report();
addEventListener("click",function(e){if(e.defaultPrevented||e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
var p=e.composedPath?e.composedPath():[e.target],l=null;for(var i=0;i<p.length;i++){var n=p[i];if(n&&(n.tagName==="A"||n.tagName==="AREA")&&n.href){l=n;break}}
if(!l)return;var t=(l.getAttribute("target")||"").toLowerCase();if(t&&t!=="_self"&&t!=="_top"&&t!=="_parent")return;
var u;try{u=new URL(l.href,location.href)}catch(x){return}if((u.protocol!=="http:"&&u.protocol!=="https:")||u.origin===location.origin)return;
e.preventDefault();post({event:"link",href:u.href})});
addEventListener("message",function(e){var m=e.data;if(e.source!==parent||ok.indexOf(e.origin)<0||!m||m.type!==T)return;
if(m.cmd==="back")history.back();else if(m.cmd==="forward")history.forward();else if(m.cmd==="hello")report();
else if(m.cmd==="go"&&typeof m.path==="string"&&m.path.charAt(0)==="/"&&m.path.charAt(1)!=="/"&&m.path.charAt(1)!=="\\"){var u=new URL(m.path,location.origin);if(u.origin===location.origin)location.assign(u.href)}})})();
`

func (a *Auth) serveWorkerPatch(w http.ResponseWriter, host string) {
	param, _ := json.Marshal(WorkerParam)
	token, _ := json.Marshal(a.workerToken(host))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = fmt.Fprintf(w, workerPatch, param, token)
	_, _ = io.WriteString(w, previewBridge)
}
