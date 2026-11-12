#!/usr/bin/env bash
# Usage: ci-concurrently.sh <name> <command> [<name> <command>]...
# Runs each command with bash at the same time, then prints each one's output
# whole, in the order given, and fails if any command failed.
set -euo pipefail

if [ $# -eq 0 ] || [ $(($# % 2)) -ne 0 ]; then
	echo "usage: $0 <name> <command> [<name> <command>]..." >&2
	exit 2
fi

work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ci-concurrently.XXXXXX")"
pids=()
stop() {
	local pid
	for pid in ${pids[@]+"${pids[@]}"}; do kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true; done
	rm -rf "$work"
	exit "$1"
}
trap 'stop 129' HUP
trap 'stop 130' INT
trap 'stop 143' TERM
trap 'rm -rf "$work"' EXIT

set -m
names=()
index=0
while [ $# -gt 0 ]; do
	names+=("$1")
	(
		start=$SECONDS
		set +e
		bash -c "$2" >"$work/$index.log" 2>&1
		status=$?
		echo "$status $((SECONDS - start))" >"$work/$index.status"
	) &
	pids+=("$!")
	index=$((index + 1))
	shift 2
done
set +m

for pid in "${pids[@]}"; do wait "$pid" || true; done

failed=()
for index in "${!names[@]}"; do
	status=1 seconds="?"
	[ ! -s "$work/$index.status" ] || read -r status seconds <"$work/$index.status"
	echo "::group::${names[$index]} (exit $status, ${seconds}s)"
	cat "$work/$index.log"
	echo "::endgroup::"
	[ "$status" -eq 0 ] || failed+=("${names[$index]}")
done
for name in ${failed[@]+"${failed[@]}"}; do
	echo "::error::$name failed"
done
[ "${#failed[@]}" -eq 0 ]
