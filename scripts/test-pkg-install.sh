#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
pkg="${1:?usage: scripts/test-pkg-install.sh <Agenthail.pkg>}"
pkg="$(cd "$(dirname "$pkg")" && pwd)/$(basename "$pkg")"
expected_revision="${AGENTHAIL_EXPECTED_REVISION:-$(git rev-parse HEAD)}"

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
legacy_app="$TMPDIR/AgenthailLegacy.app"
mkdir -p "$legacy_app/Contents/MacOS"
cp /Applications/Agenthail.app/Contents/Info.plist "$legacy_app/Contents/Info.plist"
cp "$ROOT/scripts/fixtures/legacy-agenthail.sh" "$legacy_app/Contents/MacOS/Agenthail"
chmod +x "$legacy_app/Contents/MacOS/Agenthail"
legacy_marker="$HOME/.agenthail/legacy-fixture-started"
rm -f "$legacy_marker"
open -g -j "$legacy_app"
for _ in {1..30}; do
	[ -f "$legacy_marker" ] && break
	sleep 1
done
test -f "$legacy_marker"
for _ in {1..30}; do
	legacy_count="$({ pgrep -u "$UID" -f "^$legacy_app/Contents/MacOS/Agenthail$" || true; } | wc -l | tr -d ' ')"
	[ "$legacy_count" = 0 ] && break
	sleep 1
done
test "$legacy_count" = 0
rm -f "$legacy_marker"
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
second_pid="$(cat "$HOME/.agenthail/daemon.pid")"
test "$first_pid" != "$second_pid"
test "$(/usr/local/bin/agenthail version --json | jq -r .revision)" = "$expected_revision"

sudo /usr/local/bin/agenthail-uninstall
test ! -e /usr/local/bin/agenthail
test ! -e /usr/local/bin/agenthail-uninstall
test ! -e "/Library/Application Support/Agenthail"
test ! -e /Applications/Agenthail.app
test -d "$HOME/.agenthail"
if launchctl print "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1; then
	echo "error: daemon service remained loaded after uninstall" >&2
	exit 1
fi

echo "package install, upgrade, and uninstall verified"
