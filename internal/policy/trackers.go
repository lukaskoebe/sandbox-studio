package policy

import (
	_ "embed"
	"slices"
	"strings"
)

// trackers is the built-in tracker list (trackers.txt): one domain per line, with # comments
// and a "version:" line that names the list.
//
//go:embed trackers.txt
var trackers string

// trackerSet holds the listed domains; trackerVersion is the list's version.
var trackerSet, trackerVersion, trackerDomains = parseTrackers(trackers)

func parseTrackers(src string) (set map[string]bool, version string, domains []string) {
	set = map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "# version:"); ok {
			version = strings.TrimSpace(v)
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[Normalize(line)] = true
		domains = append(domains, Normalize(line))
	}
	return set, version, domains
}

// Tracker reports whether host is on the tracker list, or below a domain that is.
func Tracker(host string) bool {
	h := Normalize(host)
	for h != "" {
		if trackerSet[h] {
			return true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			return false
		}
		h = h[i+1:]
	}
	return false
}

// TrackerList returns the version of the built-in tracker list and its domains, sorted.
func TrackerList() (version string, domains []string) {
	domains = append([]string(nil), trackerDomains...)
	slices.Sort(domains)
	return trackerVersion, domains
}
