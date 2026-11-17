#!/usr/bin/env bash
# Checks that an upgrade keeps what the previous release made. It runs twice against the
# running Studio, with the same data directory:
#
#   before  under the previous release: adds a provider, which brings the default teammate,
#           and, where VMs boot, creates a sandbox and writes a file into its workspace
#   after   under the new build: the teammate is still there; the sandbox still runs and its
#           agent, reconnected, reads the file; it stops and starts again with the file; a
#           fork of it has the file too; both are deleted
#
# Each phase first checks that the running Studio is the version of $STUDIO, so an upgrade
# that didn't restart Studio fails here. The smoke workflow runs it around the upgrade on each
# host OS; macOS runners can't boot VMs, so there BOOT=false and only the teammate is checked.
#
#   STUDIO=path/to/studio [ADDR=127.0.0.1:7878] [BOOT=true] scripts/upgrade-test.sh before|after
set -euo pipefail

studio=${STUDIO:?set STUDIO to the studio binary}
addr=${ADDR:-127.0.0.1:7878}
boot=${BOOT:-true}
work=${UPGRADE_DIR:-${RUNNER_TEMP:-/tmp}/upgrade-test}
base="http://$addr"
jar="$work/cookies"
mkdir -p "$work"

say() { printf '%s\n' "$*"; }
fail() {
	printf '::error::%s\n' "$*" >&2
	exit 1
}

# Every call names the address, so the test only ever reaches the Studio it is meant for.
st() { "$studio" -addr "$addr" "$@"; }

# The native jq.exe of the Windows runners ends its lines with CRLF.
jq() { command jq "$@" | tr -d '\r'; }

# api METHOD PATH [curl arguments] calls Studio's API with the session login made. A script's
# request has no Origin header, which the API's cross-site guard lets through.
api() {
	local method=$1 path=$2
	shift 2
	curl -fsS -b "$jar" -X "$method" "$@" "$base$path"
}

# retry DESCRIPTION COMMAND... runs the command until it works, for up to five minutes: a
# sandbox's agent takes a while to connect after a boot or a Studio restart.
retry() {
	local what=$1
	shift
	for _ in $(seq 1 60); do
		"$@" 2>/dev/null && return 0
		sleep 5
	done
	fail "Couldn't $what in five minutes."
}

# login trades a one-time link for a session cookie, as the web UI does with the link's
# fragment, and checks that the running Studio is $STUDIO's version.
login() {
	local link want running
	link=$(st login-url)
	curl -fsS -c "$jar" -H 'Content-Type: application/json' \
		-d "{\"token\":\"${link##*#studio-login=}\"}" "$base/api/auth/exchange"
	want=$(st version | awk 'NR == 1 { print $2 }')
	running=$(api GET /api/system/updates | jq -r .current)
	[ "$running" = "$want" ] || fail "Studio $running is running, not $want of $studio."
	say "Studio $running is running."
	env=$(api GET /api/environments | jq -r '.[0].id // empty')
	[ -n "$env" ] || fail "Studio lists no environment."
}

sandbox_id() {
	api GET "/api/environments/$env/sandboxes" | jq -r --arg n "$1" '.[] | select(.name == $n) | .id'
}

# shellcheck disable=SC2317 # run through retry
upload_marker() {
	api POST "/api/environments/$env/sandboxes/$1/files?name=upgrade-marker.txt&conflict=replace" \
		-H 'Content-Type: application/octet-stream' --data-binary "@$work/marker" >/dev/null
}

# shellcheck disable=SC2317 # run through retry
read_marker() {
	api GET "/api/environments/$env/sandboxes/$1/files/content?path=upgrade-marker.txt" | jq -er .data | base64 -d >"$work/read"
}

# check_marker SANDBOX NAME checks that the sandbox's workspace has the file before wrote.
check_marker() {
	retry "read upgrade-marker.txt of $2" read_marker "$1"
	[ "$(cat "$work/read")" = "$(cat "$work/marker")" ] ||
		fail "upgrade-marker.txt of $2 says \"$(cat "$work/read")\", not \"$(cat "$work/marker")\"."
	say "$2 has the file the previous release wrote."
}

before() {
	login
	api POST "/api/environments/$env/providers" -H 'Content-Type: application/json' \
		-d '{"name":"Anthropic","kind":"anthropic_api","apiKey":"sk-ant-upgrade-test"}' >/dev/null
	api GET "/api/environments/$env/teammates" | jq -r '.[0].name // empty' >"$work/teammate"
	[ -s "$work/teammate" ] || fail "The provider brought no teammate."
	say "Teammate: $(cat "$work/teammate")"
	[ "$boot" = true ] || return 0

	st create upgrade --cpus 2 --memory 2
	local id
	id=$(sandbox_id upgrade)
	printf 'written by studio %s at %s\n' "$(st version | awk 'NR == 1 { print $2 }')" "$(date -u +%FT%TZ)" >"$work/marker"
	retry "write upgrade-marker.txt into the sandbox" upload_marker "$id"
	check_marker "$id" "The sandbox"
}

after() {
	login
	api GET "/api/environments/$env/teammates" | jq -e --arg n "$(cat "$work/teammate")" 'any(.[]; .name == $n)' >/dev/null ||
		fail "The teammate $(cat "$work/teammate") is gone after the upgrade."
	say "The teammate is still there."
	[ "$boot" = true ] || return 0

	local id fork
	id=$(sandbox_id upgrade)
	[ -n "$id" ] || fail "The sandbox is gone after the upgrade."
	st show upgrade
	check_marker "$id" "The sandbox, running through the upgrade,"

	st stop upgrade
	st start upgrade
	check_marker "$id" "The sandbox, stopped and started again by the new build,"

	st fork upgrade upgrade-fork
	fork=$(sandbox_id upgrade-fork)
	[ -n "$fork" ] || fail "The fork isn't listed."
	check_marker "$fork" "Its fork"

	st rm upgrade-fork --yes
	st rm upgrade --yes
}

case "${1:-}" in
before) before ;;
after) after ;;
*)
	printf 'usage: %s before|after\n' "$0" >&2
	exit 2
	;;
esac
