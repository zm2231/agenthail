#!/usr/bin/env bash
set -Eeuo pipefail
trap 'echo "error: package install test failed at line $LINENO: $BASH_COMMAND" >&2' ERR

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
pkg="${1:?usage: scripts/test-pkg-install.sh <Agenthail.pkg>}"
pkg="$(cd "$(dirname "$pkg")" && pwd)/$(basename "$pkg")"
expected_revision="${AGENTHAIL_EXPECTED_REVISION:-$(git rev-parse HEAD)}"
legacy_app=""
unrelated_app=""
legacy_pid=""
unrelated_pid=""
legacy_marker="$HOME/.agenthail/legacy-fixture-started"
unrelated_marker="$HOME/.agenthail/unrelated-fixture-started"
fixture_is_running() {
	local pid="$1"
	local executable="$2"
	[[ "$pid" =~ ^[0-9]+$ ]] || return 1
	kill -0 "$pid" 2>/dev/null || return 1
	lsof -a -p "$pid" -d txt -Fn 2>/dev/null | grep -Fqx "n$executable"
}
cleanup_fixtures() {
	[ -z "$legacy_pid" ] || ! fixture_is_running "$legacy_pid" "$legacy_app/Contents/MacOS/Agenthail" || kill -TERM "$legacy_pid" >/dev/null 2>&1 || true
	[ -z "$unrelated_pid" ] || ! fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail" || kill -TERM "$unrelated_pid" >/dev/null 2>&1 || true
	rm -f "$legacy_marker" "$unrelated_marker"
}
trap cleanup_fixtures EXIT

mkdir -p "$HOME/.codex"
sudo installer -pkg "$pkg" -target /

test -x /usr/local/bin/agenthail
test -x /usr/local/bin/agenthail-uninstall
test -x "/Library/Application Support/Agenthail/runtime/python/bin/python3"
test -x "/Library/Application Support/Agenthail/runtime/node/bin/node"
test -x /Applications/Agenthail.app/Contents/MacOS/Agenthail
for _ in {1..5}; do
	menu_count="$({ pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$' || true; } | wc -l | tr -d ' ')"
	[ "$menu_count" = 1 ] && break
	sleep 1
done
if [ "$menu_count" = 0 ]; then
	/Applications/Agenthail.app/Contents/MacOS/Agenthail >"$TMPDIR/agenthail-pkg-menu.log" 2>&1 &
fi
for _ in {1..30}; do
	menu_count="$({ pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$' || true; } | wc -l | tr -d ' ')"
	[ "$menu_count" = 1 ] && break
	sleep 1
done
if [ "$menu_count" != 1 ]; then
	cat "$TMPDIR/agenthail-pkg-menu.log" 2>/dev/null || true
	ps -axo pid=,command= | grep 'Agenthail.app/Contents/MacOS/Agenthail' || true
fi
test "$menu_count" = 1
menu_pid="$(pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$')"
kill -TERM "$menu_pid"
for _ in {1..30}; do
	menu_count="$({ pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$' || true; } | wc -l | tr -d ' ')"
	[ "$menu_count" = 0 ] && break
	sleep 1
done
test "$menu_count" = 0
legacy_app="$TMPDIR/AgenthailLegacy.app"
mkdir -p "$legacy_app/Contents/MacOS"
legacy_app="$(cd "$legacy_app" && pwd -P)"
cp /Applications/Agenthail.app/Contents/Info.plist "$legacy_app/Contents/Info.plist"
swiftc "$ROOT/scripts/fixtures/native-app.swift" -o "$legacy_app/Contents/MacOS/Agenthail"
rm -f "$legacy_marker"
open -g -j -n "$legacy_app"
for _ in {1..30}; do
	[ -s "$legacy_marker" ] && break
	sleep 1
done
if [ ! -s "$legacy_marker" ]; then
	lsappinfo list | grep -i agenthail || true
	ps -axo pid=,command= | grep 'AgenthailLegacy.app' || true
fi
test -s "$legacy_marker"
legacy_pid="$(tr -d '[:space:]' <"$legacy_marker")"
fixture_is_running "$legacy_pid" "$legacy_app/Contents/MacOS/Agenthail"
/Applications/Agenthail.app/Contents/MacOS/Agenthail >"$TMPDIR/agenthail-pkg-menu.log" 2>&1 &
for _ in {1..30}; do
	menu_count="$({ pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$' || true; } | wc -l | tr -d ' ')"
	! kill -0 "$legacy_pid" 2>/dev/null && [ "$menu_count" = 1 ] && break
	sleep 1
done
test "$menu_count" = 1
if kill -0 "$legacy_pid" 2>/dev/null; then
	echo "error: same-bundle duplicate remained alive after Agenthail launched" >&2
	exit 1
fi
legacy_pid=""
rm -f "$legacy_marker"
unrelated_app="$TMPDIR/AgenthailUnrelated.app"
mkdir -p "$unrelated_app/Contents/MacOS"
unrelated_app="$(cd "$unrelated_app" && pwd -P)"
cp /Applications/Agenthail.app/Contents/Info.plist "$unrelated_app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleIdentifier com.agenthail.unrelated-fixture' "$unrelated_app/Contents/Info.plist"
swiftc "$ROOT/scripts/fixtures/native-app.swift" -o "$unrelated_app/Contents/MacOS/Agenthail"
rm -f "$unrelated_marker"
open -g -j -n "$unrelated_app"
for _ in {1..30}; do
	[ -s "$unrelated_marker" ] && break
	sleep 1
done
test -s "$unrelated_marker"
sleep 2
unrelated_pid="$(tr -d '[:space:]' <"$unrelated_marker")"
fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail"
test "$(/usr/local/bin/agenthail version --json | jq -r .revision)" = "$expected_revision"
/usr/local/bin/agenthail help | grep -q 'thread create codex'
/usr/local/bin/agenthail help | grep -q 'update \[--check\]'
test -L "$HOME/.codex/skills/agenthail-operations"

if ! /usr/local/bin/agenthail daemon status >/dev/null 2>&1; then
	/usr/local/bin/agenthail daemon install
fi
/usr/local/bin/agenthail daemon status
/usr/local/bin/agenthail dashboard status
test "$(jq -r .enabled "$HOME/.agenthail/dashboard.json")" = "true"
menu_pid="$(pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$')"
dashboard_token="$HOME/.agenthail/dashboard.token"
old_token_hash="$(shasum -a 256 "$dashboard_token" | awk '{print $1}')"
mv "$dashboard_token" "$TMPDIR/dashboard.token.before-rotation"
/usr/local/bin/agenthail daemon restart
for _ in {1..30}; do
	[ -s "$dashboard_token" ] && [ "$(shasum -a 256 "$dashboard_token" | awk '{print $1}')" != "$old_token_hash" ] && break
	sleep 1
done
test -s "$dashboard_token"
test "$(shasum -a 256 "$dashboard_token" | awk '{print $1}')" != "$old_token_hash"
dashboard_port="$(jq -r '.listen | split(":") | last' "$HOME/.agenthail/dashboard.json")"
for _ in {1..30}; do
	lsof -nP -a -p "$menu_pid" -iTCP:"$dashboard_port" -sTCP:ESTABLISHED >/dev/null 2>&1 && break
	sleep 1
done
lsof -nP -a -p "$menu_pid" -iTCP:"$dashboard_port" -sTCP:ESTABLISHED >/dev/null
doctor_json="$(/usr/local/bin/agenthail doctor --json || true)"
jq -e '.surfaces | length == 3' <<<"$doctor_json" >/dev/null
jq -e 'all(.surfaces[]; ((.error // "") | test("curl_cffi|python.*not found|node.*not found|sweet-cookie"; "i") | not))' <<<"$doctor_json" >/dev/null
plist="$HOME/Library/LaunchAgents/com.agenthail.daemon.plist"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_PYTHON raw -o - "$plist")" = "/Library/Application Support/Agenthail/runtime/python/bin/python3"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_SIDECAR raw -o - "$plist")" = "/Library/Application Support/Agenthail/sidecar.py"
first_pid="$(cat "$HOME/.agenthail/daemon.pid")"

sudo installer -pkg "$pkg" -target /
/usr/local/bin/agenthail daemon status
sleep 2
test "$({ pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$' || true; } | wc -l | tr -d ' ')" = 1
upgraded_menu_pid="$(pgrep -u "$UID" -f '^/Applications/Agenthail.app/Contents/MacOS/Agenthail$')"
test "$menu_pid" != "$upgraded_menu_pid"
second_pid="$(cat "$HOME/.agenthail/daemon.pid")"
test "$first_pid" != "$second_pid"
test "$(/usr/local/bin/agenthail version --json | jq -r .revision)" = "$expected_revision"
fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail"

sudo /usr/local/bin/agenthail-uninstall
test ! -e /usr/local/bin/agenthail
test ! -e /usr/local/bin/agenthail-uninstall
test ! -e "/Library/Application Support/Agenthail"
test ! -e /Applications/Agenthail.app
test -d "$HOME/.agenthail"
fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail"
kill -TERM "$unrelated_pid"
for _ in {1..30}; do
	! fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail" && break
	sleep 1
done
if fixture_is_running "$unrelated_pid" "$unrelated_app/Contents/MacOS/Agenthail"; then
	echo "error: unrelated fixture did not stop during cleanup" >&2
	exit 1
fi
unrelated_pid=""
rm -f "$unrelated_marker"
if launchctl print "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1; then
	echo "error: daemon service remained loaded after uninstall" >&2
	exit 1
fi

echo "package install, upgrade, and uninstall verified"
