#!/usr/bin/env bash
set -euo pipefail

: "${RUNNER_TEMP:?}" "${RUNNER_TOOL_CACHE:?}" "${GITHUB_WORKSPACE:?}"

prepare() {
	local home="$RUNNER_TEMP/home" tmp="$RUNNER_TEMP/tmp" cache="$RUNNER_TOOL_CACHE/agenthail-cache"
	mkdir -p "$home" "$tmp" "$cache/go-build" "$cache/go-mod" "$cache/npm"
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

job_pids() {
	local keep pid
	keep=" $(ancestors | tr '\n' ' ') "
	{ pgrep -u "$(id -u)" -f "$RUNNER_TEMP/" || true; pgrep -u "$(id -u)" -f "$GITHUB_WORKSPACE/" || true; } | sort -u |
		while read -r pid; do
			[[ "$keep" == *" $pid "* ]] || echo "$pid"
		done
}

cleanup() {
	local pids
	pids="$(job_pids | xargs)"
	if [ -n "$pids" ]; then
		echo "Stopping leftover job processes: $pids"
		kill $pids 2>/dev/null || true
		sleep 2
		pids="$(job_pids | xargs)"
		[ -z "$pids" ] || kill -9 $pids 2>/dev/null || true
	fi
	if [ -n "${SIMULATOR_UDID:-}" ]; then
		xcrun simctl shutdown "$SIMULATOR_UDID" 2>/dev/null || true
		xcrun simctl erase "$SIMULATOR_UDID"
	fi
	if [ -d "$GITHUB_WORKSPACE/.git" ]; then
		git -C "$GITHUB_WORKSPACE" reset --hard -q
		git -C "$GITHUB_WORKSPACE" clean -ffdxq
	fi
	rm -rf "$RUNNER_TEMP/home" "$RUNNER_TEMP/tmp"
}

case "${1:-}" in
	prepare) prepare ;;
	cleanup) cleanup ;;
	*) echo "usage: $0 prepare|cleanup" >&2; exit 2 ;;
esac
