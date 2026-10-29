#!/bin/sh
# Builds Sandbox Studio.app around a studio-app binary, in out-dir. macOS shows the system's
# notifications only for an app bundle with an identifier (docs/app.md), and `studio app`
# finds the bundle next to studio. Without a signing identity the bundle is signed ad hoc,
# so its signature covers its Info.plist; the release signs it again with its identity.
#
#   scripts/macos-app.sh <studio-app> <out-dir> <version> [identity]
set -eu

[ $# -ge 3 ] || {
	echo "usage: $0 <studio-app> <out-dir> <version> [identity]" >&2
	exit 2
}
bin=$1
out=$2
identity=${4:-}
root=$(cd "$(dirname "$0")/.." && pwd)
# The bundle's version is numbers only: v0.12.0-rc.1 is 0.12.0, and a dev build 0.0.0.
version=$(printf '%s' "$3" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')
[ -n "$version" ] || version=0.0.0

app="$out/Sandbox Studio.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/studio-app"
chmod 755 "$app/Contents/MacOS/studio-app"
sed "s/@VERSION@/$version/g" "$root/packaging/macos/Info.plist" >"$app/Contents/Info.plist"

if command -v iconutil >/dev/null 2>&1; then
	set=$(mktemp -d)/icons.iconset
	mkdir -p "$set"
	for size in 16 32 128 256 512; do
		sips -z "$size" "$size" "$root/web/public/icon-512.png" --out "$set/icon_${size}x${size}.png" >/dev/null
		sips -z $((size * 2)) $((size * 2)) "$root/web/public/icon-512.png" --out "$set/icon_${size}x${size}@2x.png" >/dev/null
	done
	iconutil -c icns "$set" -o "$app/Contents/Resources/icons.icns"
	rm -rf "$(dirname "$set")"
fi
if command -v plutil >/dev/null 2>&1; then
	plutil -lint "$app/Contents/Info.plist" >/dev/null
fi
if command -v codesign >/dev/null 2>&1; then
	if [ -n "$identity" ]; then
		codesign --force --timestamp --options runtime --sign "$identity" "$app"
	else
		codesign --force --sign - "$app"
	fi
	codesign --verify --strict --verbose=2 "$app"
fi
echo "$app"
