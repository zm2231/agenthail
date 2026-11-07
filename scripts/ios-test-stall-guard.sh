#!/usr/bin/env bash
# Must run on the macOS system bash 3.2.
set -euo pipefail

limit="${AGENTHAIL_IOS_SLOW_TEST_SECONDS:-200}"
max=3
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
mkfifo "$dir/out"

"$@" >"$dir/out" 2>&1 &
pid=$!

pattern="^Test Case '-\[([^]]+)\]' (passed|failed) \(([0-9]+)(\.[0-9]+)? seconds\)"
count=0
slow=""
while IFS= read -r line; do
	printf '%s\n' "$line"
	if [[ "$line" =~ $pattern ]] && [ "${BASH_REMATCH[3]}" -gt "$limit" ]; then
		count=$((count + 1))
		slow="$slow ${BASH_REMATCH[1]} (${BASH_REMATCH[3]} s);"
		if [ "$count" -ge "$max" ]; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
			echo "::error::Stopped the tests: $max tests each took over $limit s:$slow A UI test whose teardown waits about 300 s usually means the crash reporter is waiting on a dialog. See Crash reporter dialogs in docs/maintainers/self-hosted-ci.md."
			exit 1
		fi
	fi
done <"$dir/out"

wait "$pid"
