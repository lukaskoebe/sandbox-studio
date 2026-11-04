package policy

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
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
	// ReadOnly names the hosts that also take writes: a push, a publish. Their connections are
	// terminated, so that each request can be read, and a write asks as if the entry didn't cover
	// the host. The entry's other hosts only serve downloads, and their connections pass unread.
	ReadOnly []string `json:"readOnly,omitempty" doc:"Hosts of the entry that also take writes. Only their reads are allowed; a write asks"`
	// ReadPosts are the paths on those hosts where a POST only reads, such as the request that
	// fetches from a git repository (readPostMatches).
	ReadPosts []string `json:"readPosts,omitempty" doc:"Paths on the read-only hosts where a POST only reads"`
}

// allowsRequest reports whether the entry allows the request r to its host: anything on a host that
// isn't read-only, and otherwise only what reads. A path that cleaning changes could mean anything
// upstream, so it doesn't read.
func (e CuratedEntry) allowsRequest(host string, r *HTTPRequest) bool {
	if !slices.Contains(e.ReadOnly, host) {
		return true
	}
	if r.Override {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		p := cmp.Or(r.FullPath, r.Path)
		return cleanPath(p) == p && slices.ContainsFunc(e.ReadPosts, func(pattern string) bool { return readPostMatches(pattern, p) })
	}
	return false
}

// readPostMatches reports whether a path fits a readPosts pattern. A pattern with no wildcard is
// the exact path. In /*/*/x each * is a segment of a repository's path, and in /**/x the ** is two
// or more of them, as a GitLab project can be in subgroups. It has to name a repository: a path
// only ending like a git request can be a write, such as GitLab's POST
// /api/v4/projects/1/repository/files/git-upload-pack, which creates a file, or one of its /-/
// routes.
func readPostMatches(pattern, path string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == path
	}
	want, got := strings.Split(pattern[1:], "/"), strings.Split(strings.TrimPrefix(path, "/"), "/")
	if want[0] == "**" {
		n := len(got) - (len(want) - 1)
		if n < 2 || got[0] == "api" {
			return false
		}
		want = append(slices.Repeat([]string{"*"}, n), want[1:]...)
	}
	if len(want) != len(got) {
		return false
	}
	for i, w := range want {
		if w == "*" && !repoSegment(got[i]) || w != "*" && w != got[i] {
			return false
		}
	}
	return true
}

// repoSegment reports whether s can be a segment of a repository's path on a forge. An escaped or
// otherwise odd segment isn't, as the forge may read it as something else.
func repoSegment(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return s != "" && s != "-"
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

// curatedReadOnly reports whether a curated entry allows only the reads of host, on or off.
func curatedReadOnly(host string) bool {
	return slices.ContainsFunc(curated.Entries, func(e CuratedEntry) bool { return slices.Contains(e.ReadOnly, host) })
}

// curatedHost reports whether an enabled entry covers host on some port.
func curatedHost(host string, off []string) bool {
	return slices.ContainsFunc(curated.Entries, func(e CuratedEntry) bool {
		return !slices.Contains(off, e.ID) && slices.Contains(e.Hosts, host)
	})
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
