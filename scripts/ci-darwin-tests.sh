#!/usr/bin/env bash
# Usage: ci-darwin-tests.sh
# The macOS verify job's tests, run concurrently: the packages with macOS code
# paths, internal/daemon as SHARDS shards of one test binary, the dashboard
# readability guard and, with FULL=true, the native helper tests.
# GUARD_CACHE is where the guard keeps Chrome for Testing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
shards="${SHARDS:-3}"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ci-darwin-tests.XXXXXX")"
trap 'rm -rf "$work"' EXIT

bin="$work/daemon.test"
go test -c -o "$bin" ./internal/daemon
"$bin" -test.list '.*' | grep -E '^(Test|Example|Fuzz)' | sort >"$work/listed"
for ((shard = 0; shard < shards; shard++)); do
	go run ./scripts/go-test-shard "$shard" "$shards" ./internal/daemon >"$work/shard-$shard"
	go run ./scripts/go-test-shard -names "$shard" "$shards" ./internal/daemon
done | sort >"$work/sharded"
diff "$work/listed" "$work/sharded"

packages="$(go run ./scripts/go-darwin-packages ./... | grep -vx github.com/zm2231/agenthail/internal/daemon | tr '\n' ' ')"
set -- "packages other than internal/daemon" "go test $packages -count=1 -timeout 25m"
for ((shard = 0; shard < shards; shard++)); do
	set -- "$@" "internal/daemon shard $shard" "cd internal/daemon && '$bin' -test.run \"\$(cat '$work/shard-$shard')\" -test.count=1 -test.timeout 25m"
done
set -- "$@" "dashboard readability guard" \
	"cd internal/daemon && AGENTHAIL_BROWSER_GUARD=1 AGENTHAIL_BROWSER_CACHE='${GUARD_CACHE:?}' '$bin' -test.run '^TestDashboardTextIsReadableOverEveryBackground\$' -test.count=1 -test.v"
if [ "${FULL:-}" = true ]; then
	set -- "$@" "native helper tests" "scripts/test-native-helpers.sh"
fi
scripts/ci-concurrently.sh "$@"
