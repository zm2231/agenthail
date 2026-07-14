#!/usr/bin/env bash
set -euo pipefail

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
test "$(/usr/local/bin/agenthail version --json | jq -r .revision)" = "$expected_revision"
test -L "$HOME/.codex/skills/agenthail-operations"

if ! /usr/local/bin/agenthail daemon status >/dev/null 2>&1; then
	/usr/local/bin/agenthail daemon install
fi
/usr/local/bin/agenthail daemon status
/usr/local/bin/agenthail dashboard enable --no-open
/usr/local/bin/agenthail dashboard status
doctor_json="$(/usr/local/bin/agenthail doctor --json || true)"
jq -e '.surfaces | length == 3' <<<"$doctor_json" >/dev/null
jq -e 'all(.surfaces[]; ((.error // "") | test("curl_cffi|python.*not found|node.*not found|sweet-cookie"; "i") | not))' <<<"$doctor_json" >/dev/null
plist="$HOME/Library/LaunchAgents/com.agenthail.daemon.plist"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_PYTHON raw -o - "$plist")" = "/Library/Application Support/Agenthail/runtime/python/bin/python3"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_SIDECAR raw -o - "$plist")" = "/Library/Application Support/Agenthail/sidecar.py"
first_pid="$(cat "$HOME/.agenthail/daemon.pid")"

sudo installer -pkg "$pkg" -target /
/usr/local/bin/agenthail daemon status
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
