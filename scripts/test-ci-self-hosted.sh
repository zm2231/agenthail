#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
calls="$work/go-calls"

mkdir -p "$work/bin"
cat >"$work/bin/go" <<'SH'
#!/usr/bin/env bash
printf '%s %s\n' "$GOCACHE" "$*" >>"$CALLS"
SH
chmod +x "$work/bin/go"
cat >"$work/bin/du" <<'SH'
#!/usr/bin/env bash
printf '%s\t%s\n' "$CACHE_KB" "${@: -1}"
SH
chmod +x "$work/bin/du"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

trim() {
	local tools="$1" cache_kb="$2"
	mkdir -p "$work/temp" "$work/workspace"
	PATH="$work/bin:$PATH" CALLS="$calls" CACHE_KB="$cache_kb" \
		RUNNER_TEMP="$work/temp" RUNNER_TOOL_CACHE="$tools" GITHUB_WORKSPACE="$work/workspace" \
		RUNNER_NAME="agenthail-ci-self-hosted-test" "$ROOT/scripts/ci-self-hosted.sh" trim-go-cache >/dev/null 2>&1
}

fill() {
	mkdir -p "$1"
	head -c 65536 /dev/urandom >"$1/entry"
}

tools="$work/tools"
fill "$tools/agenthail-cache/go-build"
real="$(cd "$tools" && pwd -P)/agenthail-cache/go-build"

: >"$calls"
trim "$tools" 8388609 || fail "an oversized cache was not trimmed"
[ "$(cat "$calls")" = "$real clean -cache" ] || fail "go was called as: $(cat "$calls")"

: >"$calls"
trim "$tools" 8388608 || fail "a cache under the limit failed"
[ ! -s "$calls" ] || fail "a cache under the limit was trimmed"

outside="$work/outside"
fill "$outside"
linked="$work/linked-build"
mkdir -p "$linked/agenthail-cache"
ln -s "$outside" "$linked/agenthail-cache/go-build"
: >"$calls"
! trim "$linked" 8388609 || fail "a symlinked go-build directory was accepted"
[ ! -s "$calls" ] && [ -f "$outside/entry" ] || fail "a symlinked go-build directory was trimmed"

linked="$work/linked-cache"
mkdir -p "$linked" "$work/elsewhere/go-build"
fill "$work/elsewhere/go-build"
ln -s "$work/elsewhere" "$linked/agenthail-cache"
: >"$calls"
! trim "$linked" 8388609 || fail "a symlinked agenthail-cache directory was accepted"
[ ! -s "$calls" ] && [ -f "$work/elsewhere/go-build/entry" ] || fail "a symlinked agenthail-cache directory was trimmed"

: >"$calls"
mkdir -p "$work/empty"
trim "$work/empty" 8388609 || fail "a runner without a Go cache failed"
[ ! -s "$calls" ] || fail "a missing cache was trimmed"

ln -s "$tools" "$work/tools-link"
: >"$calls"
trim "$work/tools-link" 8388609 || fail "a tool cache reached through a symlink was refused"
[ "$(cat "$calls")" = "$real clean -cache" ] || fail "go was called as: $(cat "$calls")"

for bad in "tools" "$work/tools/../tools" "$work/./tools"; do
	: >"$calls"
	! trim "$bad" 8388609 || fail "RUNNER_TOOL_CACHE=$bad was accepted"
	[ ! -s "$calls" ] || fail "RUNNER_TOOL_CACHE=$bad was trimmed"
done

echo "ci-self-hosted trim-go-cache tests passed"
