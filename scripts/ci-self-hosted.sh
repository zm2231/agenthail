#!/usr/bin/env bash
set -euo pipefail

: "${RUNNER_TEMP:?}" "${RUNNER_TOOL_CACHE:?}" "${GITHUB_WORKSPACE:?}" "${RUNNER_NAME:?}"
case "$RUNNER_TOOL_CACHE" in /*) ;; *) echo "RUNNER_TOOL_CACHE must be an absolute path" >&2; exit 1 ;; esac
case "$RUNNER_TOOL_CACHE/" in */./* | */../*) echo "RUNNER_TOOL_CACHE must not contain . or .. components" >&2; exit 1 ;; esac

# The job temp directory lives in the user's own temp directory, as it does
# on hosted runners and users' Macs: /var/folders resolves to /private/var,
# and tests must see that symlink. Its name stays short because tests put
# Unix sockets in it, and macOS limits a socket path to 104 bytes. A marker
# per runner records the directory so the next job can remove it even when
# the runner was killed before cleanup.
user_tmp="$(getconf DARWIN_USER_TEMP_DIR)"
marker="${user_tmp:?}agenthail-ci-$(printf '%s' "$RUNNER_NAME" | shasum -a 256 | cut -c1-16)"
tmp=""
[ ! -f "$marker" ] || tmp="$(cat "$marker")"
case "$tmp" in "$user_tmp"[A-Za-z0-9][A-Za-z0-9][A-Za-z0-9][A-Za-z0-9]) ;; *) tmp="" ;; esac

remove_tmp() {
	[ -z "$tmp" ] || rm -rf "$tmp"
	rm -f "$marker"
}

prepare() {
	local home="$RUNNER_TEMP/home" cache="$RUNNER_TOOL_CACHE/agenthail-cache"
	[ -z "$tmp" ] || stop_pids "$tmp/" >&2
	remove_tmp
	tmp="$(mktemp -d "${user_tmp}XXXX")"
	echo "$tmp" >"$marker"
	mkdir -p "$home" "$cache/go-build" "$cache/go-mod" "$cache/npm"
	cat <<EOF
HOME=$home
CFFIXED_USER_HOME=$home
TMPDIR=$tmp/
GOTMPDIR=$tmp
GOCACHE=$cache/go-build
GOMODCACHE=$cache/go-mod
npm_config_cache=$cache/npm
EOF
}

ancestors() {
	local pid=$$
	while [ "$pid" -gt 1 ]; do
		echo "$pid"
		pid="$(ps -o ppid= -p "$pid" | tr -d ' ')"
	done
}

pids_matching() {
	local keep pattern pid
	keep=" $(ancestors | tr '\n' ' ') "
	for pattern in "$@"; do
		pgrep -u "$(id -u)" -f "$pattern" || true
	done | sort -u |
		while read -r pid; do
			[[ "$keep" == *" $pid "* ]] || echo "$pid"
		done
}

stop_pids() {
	local pids
	pids="$(pids_matching "$@" | xargs)"
	if [ -n "$pids" ]; then
		echo "Stopping leftover job processes: $pids"
		kill $pids 2>/dev/null || true
		sleep 2
		pids="$(pids_matching "$@" | xargs)"
		[ -z "$pids" ] || kill -9 $pids 2>/dev/null || true
	fi
}

cleanup() {
	stop_pids "$RUNNER_TEMP/" "$GITHUB_WORKSPACE/" ${tmp:+"$tmp/"}
	if [ -n "${SIMULATOR_UDID:-}" ]; then
		xcrun simctl shutdown "$SIMULATOR_UDID" 2>/dev/null || true
		xcrun simctl erase "$SIMULATOR_UDID"
	fi
	if [ -d "$GITHUB_WORKSPACE/.git" ]; then
		git -C "$GITHUB_WORKSPACE" reset --hard -q
		git -C "$GITHUB_WORKSPACE" clean -ffdxq
	fi
	rm -rf "${RUNNER_TEMP:?}/home"
	remove_tmp
}

# Each runner keeps its own Go build cache between jobs. Go trims entries
# unused for five days, which is slower than a busy runner fills it, so a
# cache over the limit is cleared with Go's own command. The path must
# resolve to exactly the runner's tool cache plus agenthail-cache/go-build,
# so a symlink cannot point the clean anywhere else.
trim_go_cache() {
	local max_kb=8388608 tools cache size
	tools="$(cd "$RUNNER_TOOL_CACHE" && pwd -P)"
	cache="$RUNNER_TOOL_CACHE/agenthail-cache/go-build"
	[ -d "$cache" ] || return 0
	if [ "$(cd "$cache" && pwd -P)" != "$tools/agenthail-cache/go-build" ]; then
		echo "Not trimming $cache: it does not resolve to $tools/agenthail-cache/go-build" >&2
		return 1
	fi
	size="$(du -sk "$cache" | cut -f1)"
	[ "$size" -gt "$max_kb" ] || return 0
	echo "Go build cache is $((size / 1024)) MB, over $((max_kb / 1024)) MB; clearing it"
	GOCACHE="$tools/agenthail-cache/go-build" go clean -cache
}

case "${1:-}" in
	prepare) prepare ;;
	cleanup) cleanup ;;
	trim-go-cache) trim_go_cache ;;
	*) echo "usage: $0 prepare|cleanup|trim-go-cache" >&2; exit 2 ;;
esac
