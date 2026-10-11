#!/bin/sh
# Installs Sandbox Studio's `studio` binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/lukaskoebe/sandbox-studio/main/install.sh | sh
#
# Environment:
#   SANDBOX_STUDIO_VERSION      release tag to install, e.g. v0.1.0 (default: the latest release)
#   SANDBOX_STUDIO_INSTALL_DIR  directory for the binary (default: ~/.local/bin)
#
# The archive is verified against the release's checksums.txt, and with
# `gh attestation verify` when the GitHub CLI is installed and logged in. Nothing runs with
# sudo, and no third-party software is installed: missing prerequisites are reported with
# the steps to fix them.
set -eu

REPO=lukaskoebe/sandbox-studio

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed."
}

# fetch URL FILE downloads URL to FILE. Only HTTPS is allowed, except for the file://
# releases that scripts/test-install.sh serves.
fetch() {
	curl -fsSL --proto "$proto" --retry 3 -o "$2" "$1" || die "download failed: $1"
}

detect_platform() {
	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN*) die "on Windows, download the .zip from https://github.com/$REPO/releases (see README.md)." ;;
	*) die "unsupported operating system: $(uname -s). Studio runs on Linux, macOS (Apple Silicon) and Windows." ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported CPU architecture: $(uname -m). Studio needs amd64 or arm64." ;;
	esac
	if [ "$os" = darwin ]; then
		# uname reports x86_64 for a shell running under Rosetta, so ask the hardware.
		if [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
			arch=arm64
		else
			die "Intel Macs are not supported; microsandbox needs Apple Silicon."
		fi
	fi
}

resolve_version() {
	version=${SANDBOX_STUDIO_VERSION:-}
	if [ -z "$version" ]; then
		[ -z "${SANDBOX_STUDIO_DOWNLOAD_BASE:-}" ] || die "set SANDBOX_STUDIO_VERSION together with SANDBOX_STUDIO_DOWNLOAD_BASE."
		# /releases/latest redirects to /releases/tag/<tag> of the newest non-prerelease.
		latest=$(curl -fsSLI --proto '=https' -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
			die "could not look up the latest release."
		case $latest in
		*/releases/tag/*) version=${latest##*/} ;;
		*) die "no stable release yet. To install a pre-release, set SANDBOX_STUDIO_VERSION=vX.Y.Z-rc.N." ;;
		esac
	fi
	case $version in
	v[0-9]*) ;;
	*) die "invalid version '$version': expected a tag such as v0.1.0." ;;
	esac
	case $version in
	*[!A-Za-z0-9.-]*) die "invalid version '$version'." ;;
	esac
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "sha256sum or shasum is required to verify the download."
	fi
}

verify_checksum() {
	# checksums.txt is `sha256sum` output: "<hash>  <name>" (or "<hash> *<name>").
	expected=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
	[ -n "$expected" ] || die "checksums.txt has no entry for $asset."
	[ "$(printf '%s\n' "$expected" | wc -l | tr -d ' ')" = 1 ] || die "checksums.txt lists $asset more than once."
	actual=$(sha256 "$tmp/$asset")
	expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
	[ "$actual" = "$expected" ] || die "checksum mismatch for $asset: expected $expected, got $actual. Nothing was installed."
	say "Checksum OK."
}

verify_attestation() {
	if [ -n "${SANDBOX_STUDIO_DOWNLOAD_BASE:-}" ]; then
		return # a local test release has no attestation
	fi
	if ! command -v gh >/dev/null 2>&1; then
		say "Skipping the provenance check: the GitHub CLI (gh) is not installed."
		return
	fi
	if ! gh auth status >/dev/null 2>&1; then
		say "Skipping the provenance check: gh is not logged in (gh auth login)."
		return
	fi
	if ! gh attestation verify --help >/dev/null 2>&1; then
		say "Skipping the provenance check: this gh has no 'attestation' command; update gh."
		return
	fi
	gh attestation verify "$tmp/$asset" --repo "$REPO" >/dev/null ||
		die "gh attestation verify failed for $asset. Nothing was installed."
	say "Provenance attestation OK."
}

install_binary() {
	mkdir -p "$tmp/x"
	tar -xzf "$tmp/$asset" -C "$tmp/x" || die "cannot unpack $asset."
	bin="$tmp/x/${asset%.tar.gz}/studio"
	if [ ! -f "$bin" ] || [ -L "$bin" ]; then
		die "$asset does not contain ${asset%.tar.gz}/studio."
	fi
	chmod 755 "$bin"
	# Catches a binary that cannot run here (e.g. a too old glibc) before replacing anything.
	out=$("$bin" version 2>&1) || die "the downloaded studio does not run on this host: $out"

	mkdir -p "$dir" || die "cannot create $dir; set SANDBOX_STUDIO_INSTALL_DIR to a directory you can write."
	# Copy next to the target, then rename, so a running studio is never overwritten in place.
	cp "$bin" "$dir/.studio.$$"
	mv -f "$dir/.studio.$$" "$dir/studio"
	say "Installed $(printf '%s' "$out" | head -n 1) to $dir/studio."
}

check_path() {
	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		warn "$dir is not on your PATH. Add this line to your shell profile (e.g. ~/.profile):"
		# shellcheck disable=SC2016 # $PATH is meant literally
		printf '  export PATH="%s:$PATH"\n' "$dir" >&2
		;;
	esac
}

# check_prerequisites reports what `studio doctor` will check in detail later.
check_prerequisites() {
	say ""
	say "Prerequisites:"
	if [ "$os" = linux ]; then
		if [ ! -e /dev/kvm ]; then
			problems=1
			say "  [!] /dev/kvm does not exist, so this host cannot run microVMs."
			say "      Enable hardware virtualization (VT-x or AMD-V) in the firmware and load kvm_intel"
			say "      or kvm_amd. Inside a VM, enable nested virtualization."
		elif [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
			say "  [ok] /dev/kvm is readable and writable."
		else
			problems=1
			say "  [!] /dev/kvm exists, but you cannot read and write it."
			if id -nG "$(id -un)" 2>/dev/null | tr ' ' '\n' | grep -qx kvm; then
				say "      You are in the kvm group, but this session started before that: log out and back in."
			else
				say "      Run: sudo usermod -aG kvm \"\$USER\"   then log out and back in."
				say "      (A desktop session at the machine itself usually gets access without this.)"
			fi
		fi
	else
		if [ "$(sysctl -n kern.hv_support 2>/dev/null || true)" = 1 ]; then
			say "  [ok] Hypervisor.framework is available."
		else
			problems=1
			say "  [!] Hypervisor.framework is not available (kern.hv_support is not 1)."
			say "      Run Studio on the Mac itself, not in a VM without nested virtualization."
		fi
	fi
	if [ -x "$HOME/.microsandbox/bin/msb" ]; then
		say "  [ok] microsandbox is installed in ~/.microsandbox; studio doctor checks its version."
	else
		say "  [--] microsandbox is not installed yet. Studio installs it (msb and libkrunfw) into"
		say "       ~/.microsandbox the first time it starts; that needs network access."
	fi
}

main() {
	need curl
	need tar
	need awk
	need mktemp
	detect_platform

	base=${SANDBOX_STUDIO_DOWNLOAD_BASE:-https://github.com/$REPO/releases/download}
	case $base in
	https://*) proto='=https' ;;
	file://*) proto='=file' ;;
	*) die "SANDBOX_STUDIO_DOWNLOAD_BASE must be an https:// or file:// URL." ;;
	esac
	dir=${SANDBOX_STUDIO_INSTALL_DIR:-$HOME/.local/bin}
	resolve_version
	asset="sandbox-studio_${version}_${os}_${arch}.tar.gz"

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t sandbox-studio)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' HUP INT TERM

	say "Installing Sandbox Studio $version ($os/$arch)."
	fetch "$base/$version/checksums.txt" "$tmp/checksums.txt"
	fetch "$base/$version/$asset" "$tmp/$asset"
	verify_checksum
	verify_attestation
	install_binary
	check_path

	problems=0
	check_prerequisites
	say ""
	say "Next steps:"
	if [ "$problems" = 1 ]; then
		say "  Fix the problems above first."
	fi
	say "  studio doctor   # check this host in detail"
	say "  studio          # start Studio; it prints a one-time login link for http://127.0.0.1:7878"
}

# Everything runs from here, so a download cut short in `curl | sh` executes nothing.
main "$@"
