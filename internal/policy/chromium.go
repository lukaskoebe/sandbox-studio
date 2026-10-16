package policy

import "strings"

// chromiumServices are requests Chromium makes on its own, for its own services, that the
// browser VM's managed policies don't turn off (internal/guest/browser_policy.go turns off
// the rest). No page needs them, so a browser's are denied without asking.
var chromiumServices = []struct{ host, path, reason string }{
	{"accounts.google.com", "/ListAccounts", "Chromium's check for signed-in Google accounts"},
	{"android.clients.google.com", "/checkin", "Chromium's check-in for push messages"},
	{"update.googleapis.com", "/service/update2/", "Chromium's component updates"},
}

// ChromiumService says why a browser's request is one Chromium makes for itself, or "" when
// it isn't. Only a request's path tells, so a connection is never one. A report upload goes to
// whichever host a site names, so it is told by the request (HTTPRequest.Report), not the host.
func ChromiumService(host string, r *HTTPRequest) string {
	if r == nil {
		return ""
	}
	if r.Report {
		return "a report Chromium sends the site, such as Network Error Logging"
	}
	h := Normalize(host)
	for _, s := range chromiumServices {
		if h == s.host && strings.HasPrefix(r.Path, s.path) {
			return s.reason
		}
	}
	return ""
}
