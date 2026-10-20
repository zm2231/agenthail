#!/usr/bin/env bash
set -euo pipefail

source_app="${1:?usage: scripts/test-mac-window-layout.sh <Agenthail.app>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SECONDS_ALIVE="${AGENTHAIL_LAYOUT_SECONDS:-15}"
WIDTHS="${AGENTHAIL_LAYOUT_WIDTHS:-700 900 1000 1440}"
work="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-layout.XXXXXX")"
daemon_pid=""
app_pid=""
cleanup() {
	[ -z "$app_pid" ] || kill "$app_pid" 2>/dev/null || true
	[ -z "$daemon_pid" ] || { kill "$daemon_pid" 2>/dev/null; wait "$daemon_pid" 2>/dev/null; } || true
	rm -rf "$work"
}
trap cleanup EXIT

binary="$source_app/Contents/MacOS/Agenthail"
sdk="$(otool -l "$binary" | awk '/LC_BUILD_VERSION/ {found=1} found && $1 == "sdk" {print $2; exit}')"
if ! [[ "$sdk" =~ ^[0-9]+(\.[0-9]+)*$ ]] || [ "${sdk%%.*}" -lt 26 ]; then
	echo "error: $binary is linked against macOS SDK '${sdk:-unknown}'; the split view layout under test only applies from SDK 26" >&2
	exit 1
fi

app="$work/Agenthail.app"
ditto "$source_app" "$app"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier com.agenthail.layouttest" -c "Delete :CFBundleURLTypes" -c "Delete :NSServices" "$app/Contents/Info.plist"
codesign --force --deep --sign - "$app" >/dev/null 2>&1

start_daemon() {
	[ -z "$daemon_pid" ] || { kill "$daemon_pid" 2>/dev/null; wait "$daemon_pid" 2>/dev/null || true; }
	rm -f "$work/port"
	python3 "$ROOT/scripts/fixtures/layout-daemon.py" "$@" >"$work/port" &
	daemon_pid=$!
	for _ in $(seq 1 50); do [ -s "$work/port" ] && break; sleep 0.1; done
	port="$(cat "$work/port")"
	if [ -z "$port" ] || ! kill -0 "$daemon_pid" 2>/dev/null ||
		! curl -fsS "http://127.0.0.1:$port/api/v1/version" >/dev/null || ! curl -fsS "http://127.0.0.1:$port/api/v1/snapshot" >/dev/null; then
		echo "error: the synthetic daemon did not start" >&2
		exit 1
	fi
}

stays_up() {
	local name="$1" width="$2"
	local home="$work/home-$name-$width"
	mkdir -p "$home"
	env HOME="$home" CFFIXED_USER_HOME="$home" defaults write com.agenthail.layouttest "NSWindow Frame main" "100 40 $width 900 0 0 1920 1050 "
	env -i HOME="$home" CFFIXED_USER_HOME="$home" PATH=/usr/bin:/bin TMPDIR="$work" AGENTHAIL_CLI=/usr/bin/false \
		AGENTHAIL_API_URL="http://127.0.0.1:$port" AGENTHAIL_API_TOKEN=fixture "$app/Contents/MacOS/Agenthail" >"$work/app-$name-$width.log" 2>&1 &
	app_pid=$!
	for _ in $(seq 1 "$SECONDS_ALIVE"); do
		sleep 1
		if ! kill -0 "$app_pid" 2>/dev/null; then
			echo "error: the main window aborted with $name selected at a saved width of $width pt" >&2
			tail -20 "$work/app-$name-$width.log" >&2
			exit 1
		fi
	done
	kill "$app_pid"
	wait "$app_pid" 2>/dev/null || true
	app_pid=""
	echo "Main window stayed up for ${SECONDS_ALIVE}s with $name selected at a saved width of $width pt"
}

start_daemon
for width in $WIDTHS; do
	stays_up "a working session" "$width"
done
start_daemon waiting
for width in $WIDTHS; do
	stays_up "a session waiting on a prompt Agenthail can't answer" "$width"
done
