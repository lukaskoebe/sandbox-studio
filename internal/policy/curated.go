package policy

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// The curated list is development infrastructure that Studio ships as data: package registries,
// toolchain downloads, container registries, forge read access and the CDNs they use. The guarded
// and assisted levels allow it when no rule matches, and the Rules page shows it. Changes are made
// to curated.json and shipped with Studio (docs/network.md).
//
//go:embed curated.json
var curatedJSON []byte

// CuratedEntry is one entry of the curated list: a service, its host names and its ports.
type CuratedEntry struct {
	ID    string   `json:"id" doc:"A stable identifier; environments turn entries off by it"`
	Name  string   `json:"name"`
	Group string   `json:"group"`
	Hosts []string `json:"hosts" doc:"Host names, each matched exactly"`
	Ports []int    `json:"ports"`
}

// curated is the parsed list. It is checked by the tests, so a bad file fails them, not Studio.
var curated = parseCurated(curatedJSON)

type curatedFile struct {
	Version string         `json:"version"`
	Entries []CuratedEntry `json:"entries"`
}

func parseCurated(src []byte) curatedFile {
	var f curatedFile
	if err := json.Unmarshal(src, &f); err != nil {
		panic("curated list: " + err.Error())
	}
	return f
}

// CuratedList returns the version of the curated list and its entries, in the order of the file.
func CuratedList() (version string, entries []CuratedEntry) {
	return curated.Version, slices.Clone(curated.Entries)
}

// curatedMatch returns the enabled entry that covers host:port, if one does. off lists the IDs of
// the entries the environment turned off.
func curatedMatch(host string, port int, off []string) (CuratedEntry, bool) {
	host = Normalize(host)
	for _, e := range curated.Entries {
		if slices.Contains(off, e.ID) || !slices.Contains(e.Hosts, host) || !slices.Contains(e.Ports, port) {
			continue
		}
		return e, true
	}
	return CuratedEntry{}, false
}
