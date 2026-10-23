#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
unset CLAUDE_CONFIG_DIR CODEX_HOME
TMP="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-install-test.XXXXXX")"
TEST_HOME="$TMP/home odd\" dollar\$ back\\slash"
DATA_DIR="$TEST_HOME/data odd\" dollar\$ back\\slash"
OLD_BIN="$TEST_HOME/.local/bin"
NEW_BIN="$TEST_HOME/new bin odd\" dollar\$ back\\slash"
FAKE_BIN="$TMP/fake-bin"
CUSTOM_HOME="$TMP/custom home"
CUSTOM_BIN="$CUSTOM_HOME/custom tools"
CUSTOM_DATA_1="$CUSTOM_HOME/data one"
CUSTOM_DATA_2="$CUSTOM_HOME/data two"
SUPERVISED_HOME="$TMP/supervised home"
SUPERVISED_BIN="$SUPERVISED_HOME/custom tools"
SUPERVISED_DATA_1="$SUPERVISED_HOME/data one"
SUPERVISED_DATA_2="$SUPERVISED_HOME/data two"
OTHER_HOME_DAEMON=""
COLLISION_BIN="$TEST_HOME/unmanaged tools"
SKILL_HOME="$TMP/skill home"
SKILL_BIN="$SKILL_HOME/bin"
SKILL_DATA="$SKILL_HOME/share/agenthail"
FAILED_HOME="$TMP/failed fresh home"
FAILED_BIN="$FAILED_HOME/bin"
FAILED_DATA="$FAILED_HOME/data"
SOURCE_HOME="$TMP/source home"
SOURCE_BIN="$SOURCE_HOME/bin"
SOURCE_DATA="$SOURCE_HOME/data"
SOURCE_GOCACHE="$(go env GOCACHE)"
SOURCE_GO_BIN="$(dirname "$(command -v go)")"
SOURCE_GOMODCACHE="$(go env GOMODCACHE)"
node_dist="$("$ROOT/scripts/fetch-node.sh" --dist "$(uname -s | tr '[:upper:]' '[:lower:]')" "$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')")"
export AGENTHAIL_BUILD_CACHE="$TMP/build cache odd\" dollar\$ back\\slash"
mkdir -p "$AGENTHAIL_BUILD_CACHE"
cp "$node_dist.tar.gz" "$AGENTHAIL_BUILD_CACHE/"

cleanup() {
	[ -z "$OTHER_HOME_DAEMON" ] || kill "$OTHER_HOME_DAEMON" 2>/dev/null || true
	if [ -x "$NEW_BIN/agenthail" ]; then
		HOME="$TEST_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$NEW_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	elif [ -x "$OLD_BIN/agenthail" ]; then
		HOME="$TEST_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$OLD_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	fi
	if [ -x "$SKILL_BIN/agenthail" ]; then
		HOME="$SKILL_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$SKILL_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	fi
	if [ -x "$CUSTOM_BIN/agenthail" ]; then
		HOME="$CUSTOM_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$CUSTOM_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	fi
	if [ -x "$SUPERVISED_BIN/agenthail" ]; then
		HOME="$SUPERVISED_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$SUPERVISED_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	fi
	if [ -x "$SOURCE_BIN/agenthail" ]; then
		HOME="$SOURCE_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$SOURCE_BIN/agenthail" daemon uninstall >/dev/null 2>&1 || true
	fi
	chmod -R u+w "$TMP" 2>/dev/null || true
	rm -rf "$TMP"
}
trap cleanup EXIT

mkdir -p "$TEST_HOME" "$CUSTOM_HOME" "$SUPERVISED_HOME" "$FAKE_BIN"
cp "$ROOT/scripts/fixtures/fake-launchctl.sh" "$FAKE_BIN/launchctl"
chmod +x "$FAKE_BIN/launchctl"

PYTHON_BIN="${AGENTHAIL_TEST_PYTHON:-}"
if [ -z "$PYTHON_BIN" ]; then
	for candidate in python3.14 python3.13 python3.12 python3.11 python3.10 python3; do
		candidate_path="$(command -v "$candidate" 2>/dev/null || true)"
		if [ -n "$candidate_path" ] && "$candidate_path" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' >/dev/null 2>&1; then
			PYTHON_BIN="$candidate_path"
			break
		fi
	done
fi
if [ -z "$PYTHON_BIN" ]; then
	echo "Python 3.10+ is required for installer tests" >&2
	exit 1
fi

(cd "$ROOT" && go build -trimpath -ldflags "-X main.version=v0.0.0-install-test" -o "$TMP/agenthail" ./cmd/agenthail)
AGENTHAIL_CLI_SOURCE="$TMP/agenthail" AGENTHAIL_CODESIGN_IDENTITY=- "$ROOT/scripts/build-macos-app.sh" "$TMP/Agenthail.app" "$(uname -m)" >/dev/null

install_once() {
	local home="$1"
	local install_dir="$2"
	local data_dir="$3"
	shift 3
	if [ ! -f "$home/.agenthail/dashboard.json" ]; then
		local port
		port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
		mkdir -p "$home/.agenthail"
		printf '{"enabled":true,"listen":"127.0.0.1:%s","codexRecentHours":5}\n' "$port" > "$home/.agenthail/dashboard.json"
	fi
	HOME="$home" \
	PATH="$SOURCE_GO_BIN:$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" \
	AGENTHAIL_PYTHON="$PYTHON_BIN" \
	AGENTHAIL_CLAUDE_BIN="$home/custom Claude & tools/claude" \
	AGENTHAIL_PREBUILT_BINARY="$TMP/agenthail" \
	AGENTHAIL_PREBUILT_MAC_APP="$TMP/Agenthail.app" \
	AGENTHAIL_SKIP_MAC_APP_LAUNCH=1 \
	AGENTHAIL_INSTALL_DIR="$install_dir" \
	AGENTHAIL_DATA_DIR="$data_dir" \
	"$ROOT/install.sh" "$@"
}

install_from_source() {
	local port
	port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
	mkdir -p "$SOURCE_HOME/.agenthail"
	printf '{"enabled":true,"listen":"127.0.0.1:%s","codexRecentHours":5}\n' "$port" > "$SOURCE_HOME/.agenthail/dashboard.json"
	HOME="$SOURCE_HOME" \
	PATH="$SOURCE_GO_BIN:$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" \
	AGENTHAIL_PYTHON="$PYTHON_BIN" \
	AGENTHAIL_PREBUILT_MAC_APP="$TMP/Agenthail.app" \
	AGENTHAIL_PUSH_RELAY_URL="https://relay.example.test" \
	AGENTHAIL_SKIP_MAC_APP_LAUNCH=1 \
	AGENTHAIL_INSTALL_DIR="$SOURCE_BIN" \
	AGENTHAIL_DATA_DIR="$SOURCE_DATA" \
	GOCACHE="$SOURCE_GOCACHE" \
	GOMODCACHE="$SOURCE_GOMODCACHE" \
	GOTOOLCHAIN=local \
	"$ROOT/install.sh"
}

export AGENTHAIL_INSTALL_TEST_FAIL_AFTER_ACTIVATION=1
if install_once "$FAILED_HOME" "$FAILED_BIN" "$FAILED_DATA" >"$TMP/fresh-rollback.log" 2>&1; then
	echo "error: injected fresh-install failure unexpectedly succeeded" >&2
	exit 1
fi
unset AGENTHAIL_INSTALL_TEST_FAIL_AFTER_ACTIVATION
if ! grep -q "injected post-activation failure" "$TMP/fresh-rollback.log"; then
	echo "error: the fresh install failed before activation" >&2
	cat "$TMP/fresh-rollback.log" >&2
	exit 1
fi
FAILED_PID="$(cat "$FAILED_HOME/.fake-agenthail-launchd-last-pid")"
test ! -e "$FAILED_HOME/Library/LaunchAgents/com.agenthail.daemon.plist"
test ! -e "$FAILED_HOME/.fake-agenthail-launchd-loaded"
test ! -e "$FAILED_HOME/.agenthail/daemon.pid"
test ! -e "$FAILED_BIN/agenthail"
test ! -e "$FAILED_DATA"
if kill -0 "$FAILED_PID" >/dev/null 2>&1; then
	echo "error: failed fresh install left daemon process $FAILED_PID running" >&2
	exit 1
fi
grep -Fq 'injected post-activation failure' "$TMP/fresh-rollback.log"

mkdir -p "$COLLISION_BIN"
printf '#!/usr/bin/env sh\necho unrelated\n' >"$COLLISION_BIN/agenthail"
chmod +x "$COLLISION_BIN/agenthail"
if install_once "$TEST_HOME" "$COLLISION_BIN" "$DATA_DIR" >"$TMP/unmanaged-collision.log" 2>&1; then
	echo "error: unmanaged wrapper collision was overwritten" >&2
	exit 1
fi
grep -Fq 'refusing to overwrite unmanaged executable' "$TMP/unmanaged-collision.log"
grep -Fq 'echo unrelated' "$COLLISION_BIN/agenthail"

install_once "$TEST_HOME" "$OLD_BIN" "$DATA_DIR" >/dev/null
HOME="$TEST_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$OLD_BIN/agenthail" daemon status >/dev/null
dashboard_status="$(HOME="$TEST_HOME" PATH="$FAKE_BIN:/opt/homebrew/bin:/usr/bin:/bin" "$OLD_BIN/agenthail" dashboard status)"
grep -Fq 'dashboard: enabled' <<<"$dashboard_status"
test "$(jq -r .enabled "$TEST_HOME/.agenthail/dashboard.json")" = "true"

HOME="$TEST_HOME" "$FAKE_BIN/launchctl" bootout "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1 || true
sqlite3 "$TEST_HOME/.agenthail/registry.db" <<'SQL'
ALTER TABLE routes DROP COLUMN active;
ALTER TABLE routes DROP COLUMN once_only;
ALTER TABLE message_queue DROP COLUMN evidence;
ALTER TABLE message_queue DROP COLUMN operation;
ALTER TABLE session_journal_state DROP COLUMN seed_status;
ALTER TABLE session_journal_state DROP COLUMN seed_seq;
PRAGMA user_version=10;
SQL

install_once "$TEST_HOME" "$NEW_BIN" "$DATA_DIR" >"$TMP/upgrade.log"

test ! -e "$OLD_BIN/agenthail"
test -x "$NEW_BIN/agenthail"
test -x "$DATA_DIR/Agenthail.app/Contents/MacOS/Agenthail"
test -x "$DATA_DIR/Agenthail.app/Contents/Resources/agenthail"
grep -Fq 'stopping supervised daemon for upgrade' "$TMP/upgrade.log"
grep -Fq 'reinstalling supervised daemon' "$TMP/upgrade.log"
grep -Fq 'agenthail-managed-wrapper-v1' "$NEW_BIN/agenthail"
HOME="$TEST_HOME" PATH="$FAKE_BIN:$NEW_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail version --json >/dev/null
HOME="$TEST_HOME" PATH="$FAKE_BIN:$NEW_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail daemon status >/dev/null
HOME="$TEST_HOME" PATH="$FAKE_BIN:$NEW_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail queue list --all --json >/dev/null
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name='active'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name='once_only'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('message_queue') WHERE name='evidence'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('message_queue') WHERE name='operation'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" 'PRAGMA user_version')" = "$(HOME="$TEST_HOME" PATH="$FAKE_BIN:$NEW_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail version --json | jq -r .schemaVersion)"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('relay_sources','relay_events','relay_provenance')")" = "3"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('routes') WHERE name IN ('handoff','event_cursor','last_goal_key','last_error')")" = "4"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('session_journal_state') WHERE name='seed_status'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('session_journal_state') WHERE name='seed_seq'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('session_journal_state') WHERE name='seed_identity'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('catalog_sessions') WHERE name='discovery_failures'")" = "1"
test "$(sqlite3 "$TEST_HOME/.agenthail/registry.db" "SELECT COUNT(*) FROM pragma_table_info('launcher_pending') WHERE name='alias'")" = "1"

test -f "$DATA_DIR/skills/agenthail-operations/SKILL.md"
test ! -e "$TEST_HOME/.claude"
test ! -e "$TEST_HOME/.hermes"

mkdir -p "$SKILL_HOME/.claude" "$SKILL_HOME/.codex/skills" "$SKILL_HOME/.hermes"

install_once "$SKILL_HOME" "$SKILL_BIN" "$SKILL_DATA" >"$TMP/skill-install.log"
grep -Fq 'run /config, and enable Remote Control for all sessions' "$TMP/skill-install.log"
grep -Fq 'agenthail thread create codex "task" --json' "$TMP/skill-install.log"
test -L "$SKILL_HOME/.claude/skills/agenthail-operations"
test -L "$SKILL_HOME/.codex/skills/agenthail-operations"
test -L "$SKILL_HOME/.hermes/skills/agenthail-operations"
test -f "$SKILL_HOME/.claude/skills/agenthail-operations/SKILL.md"
test -f "$SKILL_HOME/.codex/skills/agenthail-operations/SKILL.md"
test -f "$SKILL_HOME/.hermes/skills/agenthail-operations/SKILL.md"

rm -rf "$SKILL_HOME/.claude/skills" "$SKILL_HOME/.codex/skills" "$SKILL_HOME/.hermes/skills"
printf '{"remoteControlAtStartup":true}\n' >"$SKILL_HOME/.claude/settings.json"
install_once "$SKILL_HOME" "$SKILL_BIN" "$SKILL_DATA" --no-skill >"$TMP/skill-disabled.log"
grep -Fq 'Remote Control is enabled for all sessions' "$TMP/skill-disabled.log"
test ! -e "$SKILL_HOME/.claude/skills/agenthail-operations"
test ! -e "$SKILL_HOME/.codex/skills/agenthail-operations"
test ! -e "$SKILL_HOME/.hermes/skills/agenthail-operations"

mkdir -p "$TMP/checkout/skills/agenthail-operations" "$SKILL_HOME/.codex/skills" "$SKILL_HOME/.hermes/skills"
ln -s "$TMP/checkout/skills/agenthail-operations" "$SKILL_HOME/.codex/skills/agenthail-operations"
ln -s "$TMP/removed-install/skills/agenthail-operations" "$SKILL_HOME/.hermes/skills/agenthail-operations"
mkdir -p "$SKILL_HOME/.claude/skills/agenthail-operations"
printf 'mine\n' >"$SKILL_HOME/.claude/skills/agenthail-operations/SKILL.md"
install_once "$SKILL_HOME" "$SKILL_BIN" "$SKILL_DATA" >"$TMP/skill-collision.log" 2>&1
grep -Fq 'is not an agenthail symlink' "$TMP/skill-collision.log"
grep -Fqx 'mine' "$SKILL_HOME/.claude/skills/agenthail-operations/SKILL.md"
test "$(readlink "$SKILL_HOME/.codex/skills/agenthail-operations")" = "$TMP/checkout/skills/agenthail-operations"
test "$(readlink "$SKILL_HOME/.hermes/skills/agenthail-operations")" = "$SKILL_DATA/skills/agenthail-operations"

CONFIGURED_CLAUDE="$SKILL_HOME/claude config"
CONFIGURED_CODEX="$SKILL_HOME/codex home"
mkdir -p "$CONFIGURED_CLAUDE" "$CONFIGURED_CODEX"
printf '{"remoteControlAtStartup":true}\n' >"$CONFIGURED_CLAUDE/settings.json"
rm -f "$SKILL_HOME/.claude/settings.json"
CLAUDE_CONFIG_DIR="$CONFIGURED_CLAUDE" CODEX_HOME="$CONFIGURED_CODEX" install_once "$SKILL_HOME" "$SKILL_BIN" "$SKILL_DATA" >"$TMP/skill-configured.log"
grep -Fq 'Remote Control is enabled for all sessions' "$TMP/skill-configured.log"
test "$(readlink "$CONFIGURED_CLAUDE/skills/agenthail-operations")" = "$SKILL_DATA/skills/agenthail-operations"
test "$(readlink "$CONFIGURED_CODEX/skills/agenthail-operations")" = "$SKILL_DATA/skills/agenthail-operations"
grep -Fqx 'mine' "$SKILL_HOME/.claude/skills/agenthail-operations/SKILL.md"

install_once "$CUSTOM_HOME" "$CUSTOM_BIN" "$CUSTOM_DATA_1" >/dev/null
CUSTOM_PID_1="$(cat "$CUSTOM_HOME/.agenthail/daemon.pid")"
install_once "$CUSTOM_HOME" "$CUSTOM_BIN" "$CUSTOM_DATA_1" >"$TMP/custom-in-place.log"
CUSTOM_PID_2="$(cat "$CUSTOM_HOME/.agenthail/daemon.pid")"
test "$CUSTOM_PID_1" != "$CUSTOM_PID_2"
grep -Fq 'stopping supervised daemon for upgrade' "$TMP/custom-in-place.log"
grep -Fq 'reinstalling supervised daemon' "$TMP/custom-in-place.log"

touch "$CUSTOM_DATA_1/rollback-sentinel"
CUSTOM_WRAPPER_BEFORE="$(shasum -a 256 "$CUSTOM_BIN/agenthail" | awk '{print $1}')"
export AGENTHAIL_INSTALL_TEST_FAIL_AFTER_ACTIVATION=1
if install_once "$CUSTOM_HOME" "$CUSTOM_BIN" "$CUSTOM_DATA_1" >"$TMP/custom-rollback.log" 2>&1; then
	echo "error: injected post-activation failure unexpectedly succeeded" >&2
	exit 1
fi
unset AGENTHAIL_INSTALL_TEST_FAIL_AFTER_ACTIVATION
test -f "$CUSTOM_DATA_1/rollback-sentinel"
test "$(shasum -a 256 "$CUSTOM_BIN/agenthail" | awk '{print $1}')" = "$CUSTOM_WRAPPER_BEFORE"
test "$(cat "$CUSTOM_HOME/.agenthail/daemon.pid")" != "$CUSTOM_PID_2"
grep -Fq 'injected post-activation failure' "$TMP/custom-rollback.log"
HOME="$CUSTOM_HOME" PATH="$FAKE_BIN:$CUSTOM_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail daemon status >/dev/null

if install_once "$CUSTOM_HOME" "$CUSTOM_BIN" "$CUSTOM_DATA_2" >"$TMP/custom-data-collision.log" 2>&1; then
	echo "error: data-directory migration unexpectedly overwrote the managed wrapper" >&2
	exit 1
fi
grep -Fq 'refusing to overwrite unmanaged executable' "$TMP/custom-data-collision.log"
grep -Fq "$(printf '%q' "$CUSTOM_DATA_1/agenthail")" "$CUSTOM_BIN/agenthail"

install_once "$SUPERVISED_HOME" "$SUPERVISED_BIN" "$SUPERVISED_DATA_1" >/dev/null
SUPERVISED_PID_1="$(cat "$SUPERVISED_HOME/.agenthail/daemon.pid")"
if install_once "$SUPERVISED_HOME" "$SUPERVISED_BIN" "$SUPERVISED_DATA_2" >"$TMP/supervised-data-collision.log" 2>&1; then
	echo "error: supervised data-directory migration unexpectedly overwrote the managed wrapper" >&2
	exit 1
fi
test "$(cat "$SUPERVISED_HOME/.agenthail/daemon.pid")" = "$SUPERVISED_PID_1"
grep -Fq 'refusing to overwrite unmanaged executable' "$TMP/supervised-data-collision.log"
SUPERVISED_PLIST="$SUPERVISED_HOME/Library/LaunchAgents/com.agenthail.daemon.plist"
test "$(plutil -extract ProgramArguments.0 raw -o - "$SUPERVISED_PLIST")" = "$SUPERVISED_DATA_1/agenthail"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_PYTHON raw -o - "$SUPERVISED_PLIST")" = "$PYTHON_BIN"
test "$(plutil -extract EnvironmentVariables.AGENTHAIL_CLAUDE_BIN raw -o - "$SUPERVISED_PLIST")" = "$SUPERVISED_HOME/custom Claude & tools/claude"
test "$(plutil -extract ProcessType raw -o - "$SUPERVISED_PLIST")" = Standard

HOME="$SUPERVISED_HOME" "$FAKE_BIN/launchctl" bootout "gui/$UID/com.agenthail.daemon" >/dev/null 2>&1 || true
sleep 600 &
OTHER_HOME_DAEMON="$!"
echo "$OTHER_HOME_DAEMON" >"$SUPERVISED_HOME/.fake-agenthail-launchd-last-pid"
echo "/Users/demo/Library/LaunchAgents/com.agenthail.daemon.plist" >"$SUPERVISED_HOME/.fake-agenthail-launchd-plist"
touch "$SUPERVISED_HOME/.fake-agenthail-launchd-loaded"
install_once "$SUPERVISED_HOME" "$SUPERVISED_BIN" "$SUPERVISED_DATA_1" >"$TMP/other-home-service.log"
grep -Fq 'leaving the supervised daemon loaded from /Users/demo/Library/LaunchAgents/com.agenthail.daemon.plist unchanged' "$TMP/other-home-service.log"
if grep -Fq 'supervised daemon for upgrade' "$TMP/other-home-service.log" || grep -Fq 'reinstalling supervised daemon' "$TMP/other-home-service.log"; then
	echo "error: install with another HOME's launchd service loaded replaced that service" >&2
	exit 1
fi
test -f "$SUPERVISED_HOME/.fake-agenthail-launchd-loaded"
kill -0 "$OTHER_HOME_DAEMON"
kill "$OTHER_HOME_DAEMON"
wait "$OTHER_HOME_DAEMON" 2>/dev/null || true
OTHER_HOME_DAEMON=""
rm -f "$SUPERVISED_HOME/.fake-agenthail-launchd-loaded" "$SUPERVISED_HOME/.fake-agenthail-launchd-plist"

install_from_source >"$TMP/source-install.log"
strings "$SOURCE_DATA/agenthail" | grep -F 'https://relay.example.test' >/dev/null
HOME="$SOURCE_HOME" PATH="$FAKE_BIN:$SOURCE_BIN:/opt/homebrew/bin:/usr/bin:/bin" agenthail daemon status >/dev/null

echo "legacy wrapper migration: OK"
echo "fresh install rollback: OK"
echo "custom wrapper upgrade: OK"
echo "post-activation rollback: OK"
echo "wrapper collision safety: OK"
echo "another HOME's launchd service: OK"
echo "source relay configuration: OK"
