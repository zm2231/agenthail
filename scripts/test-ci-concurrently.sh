#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
run="$ROOT/scripts/ci-concurrently.sh"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

wait_for() { printf 'for _ in $(seq 100); do [ -e %q ] && break; sleep 0.05; done; [ -e %q ]' "$1" "$1"; }

RUNNER_TEMP="$work" "$run" \
	first "touch '$work/a'; $(wait_for "$work/b"); echo first out" \
	second "touch '$work/b'; $(wait_for "$work/a"); echo second out" >"$work/out" ||
	fail "commands that each wait for the other did not both pass: $(cat "$work/out")"
grep -q '^::group::first (exit 0, ' "$work/out" || fail "no group for first: $(cat "$work/out")"
[ "$(grep -E '^(first|second) out$' "$work/out" | tr '\n' ' ')" = "first out second out " ] ||
	fail "output is not in the order given: $(cat "$work/out")"

if RUNNER_TEMP="$work" "$run" ok "true" broken "echo broke; exit 3" >"$work/out"; then
	fail "a failing command passed"
fi
grep -q '^::group::broken (exit 3, ' "$work/out" || fail "no exit status for broken: $(cat "$work/out")"
grep -q '^::error::broken failed$' "$work/out" || fail "no error for broken: $(cat "$work/out")"
grep -q '^::error::ok failed$' "$work/out" && fail "ok reported as failed"

RUNNER_TEMP="$work" "$run" sleeper "sh -c 'echo \$\$ >\"$work/child\"; exec sleep 60'" >/dev/null 2>&1 &
runner=$!
for _ in $(seq 100); do [ -s "$work/child" ] && break; sleep 0.05; done
[ -s "$work/child" ] || fail "the sleeper did not start"
kill -TERM "$runner"
status=0
wait "$runner" || status=$?
[ "$status" -eq 143 ] || fail "a cancelled run exited $status, want 143"
child="$(cat "$work/child")"
for _ in $(seq 100); do kill -0 "$child" 2>/dev/null || break; sleep 0.05; done
if kill -0 "$child" 2>/dev/null; then
	kill "$child"
	fail "a cancelled run left its command running"
fi
rm -f "$work/child"

if "$run" lonely >/dev/null 2>&1; then
	fail "an odd argument count passed"
fi
[ -z "$(ls -A "$work" | grep ci-concurrently || true)" ] || fail "left its work directory behind"

echo "ci-concurrently tests passed"
