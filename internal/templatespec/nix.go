package templatespec

import (
	"maps"
	"slices"
	"strings"
)

// NixTool is the tool name that asks for a single-user Nix install in a template.
const NixTool = "nix"

// nixReleases pins the Nix releases a spec can ask for. Each holds the sha256 of the release
// tarball for x86_64 and aarch64 Linux, as releases.nixos.org publishes it beside the tarball.
// A new release is a new entry with both checksums.
var nixReleases = map[string]nixRelease{
	"2.35.1": {
		x86_64:  "c3fe29778acaa93b5095ee66e36f11ec7c6a284c40970a24cc83ac4f04809db3",
		aarch64: "79b739996f1751573b4d2b56e4ae607855184c711f2cc1274fa0952a13d4bfc9",
	},
}

type nixRelease struct {
	x86_64, aarch64 string
}

// NixSums returns the sha256 sums of the pinned Nix release version for x86_64 and aarch64
// Linux, and whether version is pinned.
func NixSums(version string) (x86_64, aarch64 string, ok bool) {
	release, ok := nixReleases[version]
	return release.x86_64, release.aarch64, ok
}

// nixVersions lists the pinned Nix releases for error messages.
func nixVersions() string {
	return strings.Join(slices.Sorted(maps.Keys(nixReleases)), ", ")
}
