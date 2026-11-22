#!/usr/bin/env bash
# Usage: ci-darwin-tests.sh
# The macOS verify job's tests, run concurrently: the packages with macOS code
# paths, internal/daemon as SHARDS shards of one test binary, the dashboard
# readability guard and, with FULL=true, the native helper tests and the
# macOS Textual table tests.
# With SELECT_RESULT=success, only what SELECTED names runs: the packages it
# lists, the daemon shards and guard when it lists internal/daemon, and the
# suites suite:native-helpers and suite:textual. Otherwise everything runs.
# GUARD_CACHE is where the guard keeps Chrome for Testing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
shards="${SHARDS:-3}"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ci-darwin-tests.XXXXXX")"
trap 'rm -rf "$work"' EXIT

module="$(go list -m)"
if [ "${SELECT_RESULT:-}" = success ]; then
	tr ' ' '\n' <<<"${SELECTED:-}" >"$work/selected"
else
	echo "the selection did not run, so everything runs"
	echo all >"$work/selected"
fi
selected() {
	grep -qxF -e all -e "$1" "$work/selected"
}

set --
packages="$(go run ./scripts/go-darwin-packages ./... | grep -vx "$module/internal/daemon" | while read -r package; do
	if selected "$package"; then echo "$package"; fi
done | tr '\n' ' ')"
if [ -n "$packages" ]; then
	set -- "packages other than internal/daemon" "go test $packages -count=1 -timeout 25m"
fi

if selected "$module/internal/daemon"; then
	bin="$work/daemon.test"
	go test -c -o "$bin" ./internal/daemon
	"$bin" -test.list '.*' | grep -E '^(Test|Example|Fuzz)' | sort >"$work/listed"
	for ((shard = 0; shard < shards; shard++)); do
		go run ./scripts/go-test-shard "$shard" "$shards" ./internal/daemon >"$work/shard-$shard"
		go run ./scripts/go-test-shard -names "$shard" "$shards" ./internal/daemon
	done | sort >"$work/sharded"
	diff "$work/listed" "$work/sharded"
	for ((shard = 0; shard < shards; shard++)); do
		set -- "$@" "internal/daemon shard $shard" "cd internal/daemon && '$bin' -test.run \"\$(cat '$work/shard-$shard')\" -test.count=1 -test.timeout 25m"
	done
	set -- "$@" "dashboard readability guard" \
		"cd internal/daemon && AGENTHAIL_BROWSER_GUARD=1 AGENTHAIL_BROWSER_CACHE='${GUARD_CACHE:?}' '$bin' -test.run '^TestDashboardTextIsReadableOverEveryBackground\$' -test.count=1 -test.timeout 25m -test.v"
fi

if [ "${FULL:-}" = true ]; then
	if selected suite:native-helpers; then
		set -- "$@" "native helper tests" "scripts/test-native-helpers.sh"
	fi
	if selected suite:textual; then
		set -- "$@" "Textual table tests" "swift test --package-path native/Packages/Textual --scratch-path '$work/textual' --filter WholeWordTableTests"
	fi
fi

if [ $# -eq 0 ]; then
	echo "nothing this change can reach runs here"
	exit 0
fi
scripts/ci-concurrently.sh "$@"
