#!/usr/bin/env bash
# Usage: ios-simulator-clones.sh delete <udid>
# Deletes the clones a parallel xcodebuild test run made of the simulator
# <udid>. Xcode keeps them in its testing device set, named "Clone N of <name>",
# and deletes them when a run ends, but a killed run can leave them behind.
set -euo pipefail

[ "${1:-}" = delete ] && [ -n "${2:-}" ] || {
	echo "usage: $0 delete <udid>" >&2
	exit 2
}
udid="$2"
name="$(xcrun simctl list devices --json | jq -r --arg udid "$udid" '[.devices[][] | select(.udid == $udid)] | first | .name // empty')"
[ -n "$name" ] || {
	echo "$0: no simulator $udid" >&2
	exit 1
}
xcrun simctl --set testing list devices --json |
	jq -r --arg name "$name" '.devices[][] | select(.name | test("^Clone [0-9]+ of ") and sub("^Clone [0-9]+ of "; "") == $name) | .udid' |
	while read -r clone; do
		echo "Deleting simulator clone $clone of $name"
		xcrun simctl --set testing shutdown "$clone" 2>/dev/null || true
		xcrun simctl --set testing delete "$clone"
	done
