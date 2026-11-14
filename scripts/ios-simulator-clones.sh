#!/usr/bin/env bash
# Usage: ios-simulator-clones.sh delete <udid>
#        ios-simulator-clones.sh slim <udid> <run-file>
# Works on the clones a parallel xcodebuild test run makes of the simulator
# <udid>. Xcode keeps them in its testing device set, named "Clone N of <name>",
# and deletes them when a run ends, but a killed run can leave them behind.
# delete removes them. slim slims each one once it has booted, while
# <run-file> exists; Xcode creates them stock, and slimming never fails the run.
set -euo pipefail

usage() {
	echo "usage: $0 delete <udid> | slim <udid> <run-file>" >&2
	exit 2
}

case "${1:-}" in
delete) [ $# = 2 ] || usage ;;
slim) [ $# = 3 ] || usage ;;
*) usage ;;
esac
udid="$2"
name="$(xcrun simctl list devices --json | jq -r --arg udid "$udid" '[.devices[][] | select(.udid == $udid)] | first | .name // empty')"
[ -n "$name" ] || {
	echo "$0: no simulator $udid" >&2
	exit 1
}

clones() {
	xcrun simctl --set testing list devices --json |
		jq -r --arg name "$name" --arg state "$1" '.devices[][] | select(.name | test("^Clone [0-9]+ of ") and sub("^Clone [0-9]+ of "; "") == $name) | select($state == "" or .state == $state) | .udid'
}

slim_one() {
	local clone="$1" started=$SECONDS out
	echo "$(date -u +%H:%M:%SZ) slimming clone $clone"
	if out="$(simslim on "$clone" --no-reboot --profile "$profile" 2>&1)"; then
		echo "$(date -u +%H:%M:%SZ) slimmed clone $clone in $((SECONDS - started)) s"
	else
		echo "$(date -u +%H:%M:%SZ) could not slim clone $clone after $((SECONDS - started)) s; it runs stock:"
		printf '%s\n' "$out" | tail -n 20
	fi
}

if [ "$1" = delete ]; then
	clones "" | while read -r clone; do
		echo "Deleting simulator clone $clone of $name"
		xcrun simctl --set testing shutdown "$clone" 2>/dev/null || true
		xcrun simctl --set testing delete "$clone"
	done
	exit 0
fi

command -v simslim >/dev/null || {
	echo "simslim is not installed, so the clones of $name run stock"
	exit 0
}
profile="$(cd "$(dirname "$0")" && pwd)/simslim-profile.json"
seen=" "
while [ -e "$3" ]; do
	for clone in $(clones Booted 2>/dev/null || true); do
		case "$seen" in *" $clone "*) continue ;; esac
		seen="$seen$clone "
		slim_one "$clone" &
	done
	sleep 1
done
wait
