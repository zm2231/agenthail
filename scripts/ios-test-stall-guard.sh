#!/usr/bin/env bash
# Must run on the macOS system bash 3.2.
set -euo pipefail

limit="${AGENTHAIL_IOS_SLOW_TEST_SECONDS:-200}"
max=3
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
mkfifo "$dir/out"

# Bash reaps background jobs as they exit, so a recorded PID may already
# belong to another process; signal it only while it is still our child.
stop_child() {
	[ "$(ps -o ppid= -p "$1" 2>/dev/null | tr -d ' ')" = "$$" ] || return 0
	kill "$1" 2>/dev/null || true
}

"$@" >"$dir/out" 2>&1 &
pid=$!

# A serial run prints Test Case '-[Class test]' passed (N seconds); a parallel
# one prints Test case 'Class.test()' passed on 'Clone 1 of ...' (N seconds).
pattern="^Test [Cc]ase '(-\[([^]]+)\]|([^']+))' (passed|failed)( on '[^']*')? \(([0-9]+)(\.[0-9]+)? seconds\)"
(
	count=0
	slow=""
	while IFS= read -r line; do
		printf '%s\n' "$line"
		if [[ "$line" =~ $pattern ]] && [ "${BASH_REMATCH[6]}" -gt "$limit" ]; then
			count=$((count + 1))
			slow="$slow ${BASH_REMATCH[2]}${BASH_REMATCH[3]} (${BASH_REMATCH[6]} s);"
			if [ "$count" -ge "$max" ]; then
				printf '%s' "$slow" >"$dir/tripped"
				stop_child "$pid"
				exit 0
			fi
		fi
	done
) <"$dir/out" &
reader=$!

status=0
wait "$pid" || status=$?

# Processes the command started can keep the pipe open after it exits, so
# the reader gets a few seconds to pass on the last output, not until EOF.
for _ in 1 2 3 4 5 6 7 8 9 10; do
	kill -0 "$reader" 2>/dev/null || break
	sleep 0.5
done
stop_child "$reader"
wait "$reader" 2>/dev/null || true

if [ -f "$dir/tripped" ]; then
	echo "::error::Stopped the tests: $max tests each took over $limit s:$(cat "$dir/tripped") A UI test whose teardown waits about 300 s usually means the crash reporter is waiting on a dialog. See Crash reporter dialogs in docs/maintainers/self-hosted-ci.md."
	exit 1
fi
exit "$status"
