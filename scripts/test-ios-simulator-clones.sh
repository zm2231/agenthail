#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir "$work/bin"
cat >"$work/bin/xcrun" <<'SH'
#!/usr/bin/env bash
shift
if [ "$1" = list ]; then
	cat <<'JSON'
{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
{"name":"Agenthail CI runner-2","udid":"base"},
{"name":"Agenthail CI runner-20","udid":"other-base"}]}}
JSON
	exit 0
fi
[ "$1" = --set ] && [ "$2" = testing ] || exit 3
shift 2
case "$1" in
list)
	cat <<'JSON'
{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
{"name":"Clone 1 of Agenthail CI runner-2","udid":"clone-1"},
{"name":"Clone 12 of Agenthail CI runner-2","udid":"clone-12"},
{"name":"Clone 1 of Agenthail CI runner-20","udid":"other-clone"},
{"name":"Clone 1 of X of Agenthail CI runner-2","udid":"nested-clone"},
{"name":"Agenthail CI runner-2","udid":"testing-base"}]}}
JSON
	;;
shutdown) ;;
delete) echo "$2" >>"$WORK/deleted" ;;
*) exit 3 ;;
esac
SH
chmod +x "$work/bin/xcrun"

touch "$work/deleted"
PATH="$work/bin:$PATH" WORK="$work" "$ROOT/scripts/ios-simulator-clones.sh" delete base >/dev/null
[ "$(sort "$work/deleted" | tr '\n' ' ')" = "clone-1 clone-12 " ] || fail "deleted: $(tr '\n' ' ' <"$work/deleted")"

if PATH="$work/bin:$PATH" WORK="$work" "$ROOT/scripts/ios-simulator-clones.sh" delete missing 2>/dev/null; then
	fail "an unknown simulator succeeded"
fi

echo "ios-simulator-clones tests passed"
