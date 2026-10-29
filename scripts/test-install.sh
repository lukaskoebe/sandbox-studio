#!/bin/sh
# Offline test for install.sh: builds a fake release in a temporary directory, serves it
# through SANDBOX_STUDIO_DOWNLOAD_BASE=file://..., and checks that a good archive installs
# while a bad or missing checksum installs nothing.
#
#   sh scripts/test-install.sh
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t test-install)
trap 'rm -rf "$tmp"' EXIT

version=v9.9.9
release="$tmp/release/$version"
mkdir -p "$release"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

# One archive per platform the release workflow builds for, so the test runs on any of them.
: >"$release/checksums.txt"
for platform in linux_amd64 linux_arm64 darwin_arm64; do
	name="sandbox-studio_${version}_$platform"
	mkdir -p "$tmp/build/$name"
	printf '#!/bin/sh\necho "studio %s"\n' "$version" >"$tmp/build/$name/studio"
	chmod 755 "$tmp/build/$name/studio"
	app="$tmp/build/$name/studio-app"
	if [ "$platform" = darwin_arm64 ]; then
		# macOS gets the app as a bundle.
		mkdir -p "$tmp/build/$name/Sandbox Studio.app/Contents/MacOS"
		app="$tmp/build/$name/Sandbox Studio.app/Contents/MacOS/studio-app"
	fi
	printf '#!/bin/sh\necho "studio-app %s"\n' "$version" >"$app"
	chmod 755 "$app"
	tar -czf "$release/$name.tar.gz" -C "$tmp/build" "$name"
	printf '%s  %s\n' "$(sha256 "$release/$name.tar.gz")" "$name.tar.gz" >>"$release/checksums.txt"
done
cp "$release/checksums.txt" "$tmp/good-checksums.txt"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# run_install DIR runs install.sh into DIR and leaves its output in $tmp/out.
run_install() {
	SANDBOX_STUDIO_DOWNLOAD_BASE="file://$tmp/release" \
		SANDBOX_STUDIO_VERSION=$version \
		SANDBOX_STUDIO_INSTALL_DIR="$1" \
		sh "$root/install.sh" >"$tmp/out" 2>&1
}

# A good release installs a binary that runs. On macOS an earlier release's bare app makes way
# for the bundle.
if [ "$(uname -s)" = Darwin ]; then
	mkdir -p "$tmp/good"
	printf '#!/bin/sh\necho old\n' >"$tmp/good/studio-app"
fi
run_install "$tmp/good" || {
	cat "$tmp/out" >&2
	fail "install of a good release failed"
}
[ "$("$tmp/good/studio")" = "studio $version" ] || fail "installed studio does not run"
grep -q 'Checksum OK' "$tmp/out" || fail "no checksum confirmation in the output"
if [ "$(uname -s)" = Darwin ]; then
	[ "$("$tmp/good/Sandbox Studio.app/Contents/MacOS/studio-app")" = "studio-app $version" ] || fail "the app bundle was not installed next to studio"
	[ ! -e "$tmp/good/studio-app" ] || fail "the earlier bare app was left next to the bundle"
else
	[ "$("$tmp/good/studio-app")" = "studio-app $version" ] || fail "the app was not installed next to studio"
fi
echo "ok: good release installs"

# A tampered checksum fails and installs nothing.
awk '{ c = substr($1, 1, 1); print (c == "0" ? "1" : "0") substr($1, 2) "  " $2 }' \
	"$tmp/good-checksums.txt" >"$release/checksums.txt"
cmp -s "$release/checksums.txt" "$tmp/good-checksums.txt" && fail "test setup: checksums were not changed"
if run_install "$tmp/mismatch"; then
	fail "install succeeded despite a checksum mismatch"
fi
grep -q 'checksum mismatch' "$tmp/out" || fail "no mismatch error in the output: $(cat "$tmp/out")"
[ ! -e "$tmp/mismatch/studio" ] || fail "a binary was installed despite a checksum mismatch"
echo "ok: checksum mismatch fails"

# A checksums.txt without an entry for the archive fails too.
echo "0000000000000000000000000000000000000000000000000000000000000000  other.tar.gz" >"$release/checksums.txt"
if run_install "$tmp/missing"; then
	fail "install succeeded without a checksum entry"
fi
grep -q 'has no entry' "$tmp/out" || fail "no missing-entry error in the output: $(cat "$tmp/out")"
[ ! -e "$tmp/missing/studio" ] || fail "a binary was installed without a checksum entry"
echo "ok: missing checksum entry fails"
