package policy

import (
	"slices"
	"testing"
)

func TestTrackerListMatchesDomainsAndSubdomains(t *testing.T) {
	for _, host := range []string{"googletagmanager.com", "www.google-analytics.com", "stats.g.doubleclick.net", "Static.Hotjar.com."} {
		if !Tracker(host) {
			t.Errorf("%s is not a tracker", host)
		}
	}
	for _, host := range []string{"example.com", "notdoubleclick.net", "doubleclick.net.example.com", "googletagmanager.com.evil.test", "com", "docs.example.org"} {
		if Tracker(host) {
			t.Errorf("%s is a tracker", host)
		}
	}
	version, domains := TrackerList()
	if version == "" || len(domains) < 100 {
		t.Fatalf("list %q with %d domains", version, len(domains))
	}
	if !slices.IsSorted(domains) {
		t.Error("the list is not sorted")
	}
}
