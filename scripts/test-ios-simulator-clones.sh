#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir "$work/bin" "$work/slim"
cat >"$work/bin/xcrun" <<'SH'
#!/usr/bin/env bash
shift
if [ "$1" = list ]; then
	cat <<'JSON'
{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
{"name":"Agenthail CI runner-2","udid":"base","state":"Booted"},
{"name":"Agenthail CI runner-20","udid":"other-base","state":"Booted"}]}}
JSON
	exit 0
fi
[ "$1" = --set ] && [ "$2" = testing ] || exit 3
shift 2
case "$1" in
list) cat "$WORK/testing.json" ;;
shutdown) ;;
delete) echo "$2" >>"$WORK/deleted" ;;
*) exit 3 ;;
esac
SH
cat >"$work/slim/simslim" <<'SH'
#!/usr/bin/env bash
echo "$*" >>"$WORK/slimmed"
[ "$2" != clone-12 ] || { echo "simslim could not reach the clone"; exit 1; }
SH
cat >"$work/testing.json" <<'JSON'
{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
{"name":"Clone 1 of Agenthail CI runner-2","udid":"clone-1","state":"Booted"},
{"name":"Clone 12 of Agenthail CI runner-2","udid":"clone-12","state":"Booting"},
{"name":"Clone 3 of Agenthail CI runner-2","udid":"clone-3","state":"Shutdown"},
{"name":"Clone 1 of Agenthail CI runner-20","udid":"other-clone","state":"Booted"},
{"name":"Clone 1 of X of Agenthail CI runner-2","udid":"nested-clone","state":"Booted"},
{"name":"Agenthail CI runner-2","udid":"testing-base","state":"Booted"}]}}
JSON
chmod +x "$work/bin/xcrun" "$work/slim/simslim"
ln -s "$(command -v jq)" "$work/bin/jq"
# A closed PATH, so a simslim installed on this machine never stands in for the stub.
export PATH="$work/bin:/usr/bin:/bin" WORK="$work"

touch "$work/deleted"
"$ROOT/scripts/ios-simulator-clones.sh" delete base >/dev/null
[ "$(sort "$work/deleted" | tr '\n' ' ')" = "clone-1 clone-12 clone-3 " ] || fail "deleted: $(tr '\n' ' ' <"$work/deleted")"

if "$ROOT/scripts/ios-simulator-clones.sh" delete missing 2>/dev/null; then
	fail "an unknown simulator succeeded"
fi

touch "$work/running" "$work/slimmed"
PATH="$work/slim:$PATH" "$ROOT/scripts/ios-simulator-clones.sh" slim base "$work/running" >"$work/log" 2>&1 &
slimmer=$!
sleep 2
sed 's/"Booting"/"Booted"/' "$work/testing.json" >"$work/testing.next" && mv "$work/testing.next" "$work/testing.json"
sleep 2
rm "$work/running"
wait "$slimmer" || fail "slim failed: $(cat "$work/log")"
profile="$ROOT/scripts/simslim-profile.json"
[ "$(LC_ALL=C sort "$work/slimmed")" = "on clone-1 --no-reboot --profile $profile
on clone-12 --no-reboot --profile $profile" ] || fail "slimmed other than each booted clone once: $(cat "$work/slimmed")"
grep -q 'slimmed clone clone-1 in ' "$work/log" || fail "no time for clone-1: $(cat "$work/log")"
grep -q 'could not slim clone clone-12 after .* it runs stock' "$work/log" && grep -q 'simslim could not reach the clone' "$work/log" ||
	fail "a failed slim was not reported: $(cat "$work/log")"

touch "$work/running"
"$ROOT/scripts/ios-simulator-clones.sh" slim base "$work/running" >"$work/log" 2>&1 || fail "slim without simslim failed"
grep -q 'simslim is not installed' "$work/log" || fail "no notice without simslim: $(cat "$work/log")"

echo "ios-simulator-clones tests passed"
