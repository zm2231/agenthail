#!/usr/bin/env bash
# Runs scripts/ios-simulator.sh against stub xcrun, lockf and defaults, so it
# needs no simulator: lockf records the command it would have run.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Under /tmp, not TMPDIR: the links below must start on the internal disk.
WORK="$(mktemp -d /tmp/agenthail-ios-simulator.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"

cat >"$WORK/bin/xcrun" <<'EOF'
#!/usr/bin/env bash
case "$1 $2 ${3:-}" in
  "simctl list devices") echo '{"devices":{"ios":[{"name":"Agenthail iPhone","udid":"UDID-IPHONE"}]}}' ;;
  "simctl list runtimes") echo '{"runtimes":[{"identifier":"com.apple.CoreSimulator.SimRuntime.iOS-27-0","isAvailable":true}]}' ;;
  "simctl list devicetypes") echo "${DEVICE_TYPES:?}" ;;
  "simctl create"*) printf '%s\n' "$@" >"$CREATED"; echo UDID-NEW ;;
  "simctl boot"*|"simctl bootstatus"*) ;;
  "simctl shutdown"*) echo "$3" >"$SHUTDOWN" ;;
  "simctl delete"*) echo "$3" >"$DELETED"; exit "${DELETE_EXIT:-0}" ;;
  "simctl rename"*) [ "${RENAME_EXIT:-0}" = 0 ] || exit 1; echo "$3 $4" >"$RENAMED" ;;
  *) echo "unexpected xcrun $*" >&2; exit 1 ;;
esac
EOF
cat >"$WORK/bin/lockf" <<'EOF'
#!/usr/bin/env bash
shift 2
printf '%s\n' "$@" >"$LOCKED"
EOF
cat >"$WORK/bin/defaults" <<'EOF'
#!/usr/bin/env bash
[ -n "${XCODE_DERIVED_DATA:-}" ] || exit 1
echo "$XCODE_DERIVED_DATA"
EOF
mkdir -p "$WORK/slim"
cat >"$WORK/slim/simslim" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$SLIMMED"
exit "${SIMSLIM_EXIT:-0}"
EOF
chmod +x "$WORK/bin/"* "$WORK/slim/simslim"
ln -s "$(command -v jq)" "$WORK/bin/jq"
# A closed PATH, so a simslim installed on this machine never stands in for the stub.
export PATH="$WORK/bin:/usr/bin:/bin" LOCKED="$WORK/locked" CREATED="$WORK/created" SLIMMED="$WORK/slimmed" SHUTDOWN="$WORK/shutdown" DELETED="$WORK/deleted" RENAMED="$WORK/renamed"

failures=0
fail() {
  echo "FAIL: $*"
  failures=$((failures + 1))
}

# run <expected exit> <args...>: runs the script and leaves what lockf ran in
# $locked, empty when the script stopped before taking the lock.
run() {
  local expected="$1" status=0
  shift
  rm -f "$LOCKED"
  "$ROOT/scripts/ios-simulator.sh" iphone run "$@" >"$WORK/out" 2>&1 || status=$?
  locked="$(cat "$LOCKED" 2>/dev/null || true)"
  [ "$status" = "$expected" ] || fail "exit $status, want $expected: $* ($(cat "$WORK/out"))"
}

run 0 xcodebuild test -scheme AgenthailIOS -derivedDataPath "$HOME/Library/Developer/Xcode/DerivedData/agenthail-x"
[ "$(tail -2 <<<"$locked" | tr '\n' ' ')" = "-collect-test-diagnostics never " ] || fail "a test run does not turn off diagnostics: $locked"

run 0 xcodebuild test-without-building -scheme AgenthailIOS
grep -qx -- -collect-test-diagnostics <<<"$locked" || fail "test-without-building does not turn off diagnostics: $locked"

run 0 xcodebuild test -scheme AgenthailIOS -collect-test-diagnostics on-failure
{ [ "$(grep -c -- -collect-test-diagnostics <<<"$locked")" = 1 ] && grep -qx on-failure <<<"$locked"; } || fail "an explicit diagnostics choice was overridden: $locked"

run 0 xcodebuild build-for-testing -scheme AgenthailIOS
grep -q -- -collect-test-diagnostics <<<"$locked" && fail "a build was given a test-only flag: $locked"

run 0 xcrun xcodebuild test -scheme AgenthailIOS
grep -qx -- -collect-test-diagnostics <<<"$locked" || fail "xcrun xcodebuild test does not turn off diagnostics: $locked"

run 2 xcodebuild test -scheme AgenthailIOS -derivedDataPath /Volumes/work/dd
[ -z "$locked" ] || fail "derived data under /Volumes took the simulator"

(cd /Volumes && run 2 xcodebuild test -scheme AgenthailIOS -derivedDataPath dd && [ -z "$locked" ]) || fail "relative derived data under /Volumes took the simulator"

XCODE_DERIVED_DATA=/Volumes/work/DerivedData run 2 xcodebuild test -scheme AgenthailIOS
[ -z "$locked" ] || fail "Xcode's own derived data location under /Volumes was not refused"

mkdir -p "$WORK/volume"
ln -s /Volumes "$WORK/volumes"
run 2 xcodebuild test -scheme AgenthailIOS -derivedDataPath "$WORK/volumes/disk/dd"
[ -z "$locked" ] || fail "derived data reached through a link to /Volumes took the simulator"
XCODE_DERIVED_DATA="$WORK/volumes/disk/DerivedData" run 2 xcodebuild test -scheme AgenthailIOS
[ -z "$locked" ] || fail "Xcode's own derived data location reached through a link to /Volumes was not refused"
ln -s "$WORK/volume" "$WORK/internal"
run 0 xcodebuild test -scheme AgenthailIOS -derivedDataPath "$WORK/internal/dd"
[ -n "$locked" ] || fail "derived data reached through a link to the internal disk was refused"

run 0 xcodebuild build -scheme test
[ "$(tr '\n' ' ' <<<"$locked")" = "xcodebuild build -scheme test " ] || fail "a scheme named test made a build a test run: $locked"
run 0 xcodebuild -quiet test -scheme AgenthailIOS
grep -qx -- -collect-test-diagnostics <<<"$locked" || fail "a test action after a flag without a value was missed: $locked"

run 0 cp /tmp/xcodebuild-notes /tmp/out
[ "$(tr '\n' ' ' <<<"$locked")" = "cp /tmp/xcodebuild-notes /tmp/out " ] || fail "a command that only names xcodebuild in a path was changed or refused: $locked"
run 2 env FOO=1 xcodebuild test -scheme AgenthailIOS
[ -z "$locked" ] || fail "xcodebuild run through env was run unchecked"

run 2 sh -c "xcodebuild test -scheme AgenthailIOS"
[ -z "$locked" ] || fail "xcodebuild inside a shell was run unchecked"

run 0 xcrun simctl install booted App.app
[ "$(tr '\n' ' ' <<<"$locked")" = "xcrun simctl install booted App.app " ] || fail "a non-xcodebuild command was changed: $locked"

pro13='{"name":"iPad Pro 13-inch (M5)","identifier":"iPad-Pro-13-inch-M5"}'
pro11='{"name":"iPad Pro 11-inch (M5)","identifier":"iPad-Pro-11-inch-M5"}'
air='{"name":"iPad Air 13-inch (M3)","identifier":"iPad-Air-13-inch-M3"}'
phone='{"name":"iPhone 17 Pro","identifier":"iPhone-17-Pro"}'
created() {
  rm -f "$CREATED"
  DEVICE_TYPES="{\"devicetypes\":[$1]}" "$ROOT/scripts/ios-simulator.sh" "$2" >/dev/null 2>&1 || true
  sed -n 4p "$CREATED" 2>/dev/null || true
}
[ "$(created "$pro13,$pro11,$phone" ipad)" = iPad-Pro-11-inch-M5 ] || fail "a new iPad simulator is not the 11-inch iPad Pro the iPad UI tests are written for"
[ "$(created "$air,$phone" ipad)" = iPad-Air-13-inch-M3 ] || fail "without an 11-inch iPad Pro no iPad simulator was created"
[ "$(AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" created "$phone,$pro11" iphone)" = iPhone-17-Pro ] || fail "the iPhone simulator type changed"

slim() {
  rm -f "$SLIMMED" "$SHUTDOWN" "$DELETED" "$RENAMED" "$CREATED"
  slim_status=0
  DEVICE_TYPES="{\"devicetypes\":[$phone]}" "$@" >/dev/null 2>&1 || slim_status=$?
  slimmed="$(cat "$SLIMMED" 2>/dev/null | tr '\n' ' ' || true)"
  shutdown="$(cat "$SHUTDOWN" 2>/dev/null || true)"
  renamed="$(cat "$RENAMED" 2>/dev/null || true)"
  created_as="$(sed -n 3p "$CREATED" 2>/dev/null || true)"
}
slim env PATH="$WORK/slim:$PATH" AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" "$ROOT/scripts/ios-simulator.sh" iphone
[ "$slimmed" = "on UDID-NEW --profile $ROOT/scripts/simslim-profile.json " ] || fail "a new simulator was not slimmed with the committed profile: $slimmed"
[ "$shutdown" = UDID-NEW ] || fail "a slimmed new simulator was not handed back shut down: $shutdown"
[ "$created_as" = "Agenthail CI phone (slimming)" ] || fail "a simulator being slimmed already had the name runs look up: $created_as"
[ "$renamed" = "UDID-NEW Agenthail CI phone" ] || fail "a slimmed simulator did not take its name: $renamed"
ln -s "$ROOT/scripts/ios-simulator.sh" "$WORK/linked-simulator.sh"
slim env PATH="$WORK/slim:$PATH" AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" "$WORK/linked-simulator.sh" iphone
[ "$slimmed" = "on UDID-NEW --profile $ROOT/scripts/simslim-profile.json " ] || fail "the script run through a link did not find the committed profile: $slimmed"
slim env PATH="$WORK/slim:$PATH" "$ROOT/scripts/ios-simulator.sh" iphone
[ -z "$slimmed" ] || fail "an existing simulator was slimmed in place: $slimmed"
slim env AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" "$ROOT/scripts/ios-simulator.sh" iphone
{ [ "$slim_status" = 0 ] && [ -z "$shutdown" ] && [ -z "$renamed" ] && [ "$created_as" = "Agenthail CI phone" ]; } || fail "a new simulator without simslim installed was not created as before: exit $slim_status, created as $created_as, shutdown $shutdown, renamed $renamed"
for delete_exit in 0 1; do
  slim env PATH="$WORK/slim:$PATH" SIMSLIM_EXIT=1 DELETE_EXIT=$delete_exit AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" "$ROOT/scripts/ios-simulator.sh" iphone
  [ "$slim_status" != 0 ] || fail "a failed slim handed the simulator back as if it worked (delete exit $delete_exit)"
  [ -z "$renamed" ] || fail "a failed slim gave its simulator the name runs look up (delete exit $delete_exit): $renamed"
  [ "$(cat "$DELETED" 2>/dev/null || true)" = UDID-NEW ] || fail "a failed slim did not try to delete its simulator (delete exit $delete_exit)"
done
slim env PATH="$WORK/slim:$PATH" RENAME_EXIT=1 AGENTHAIL_SIMULATOR_NAME="Agenthail CI phone" "$ROOT/scripts/ios-simulator.sh" iphone
{ [ "$slim_status" != 0 ] && [ "$(cat "$DELETED" 2>/dev/null || true)" = UDID-NEW ]; } || fail "a slimmed simulator that could not take its name was left behind: exit $slim_status"

if [ "$failures" -gt 0 ]; then
  exit 1
fi
echo "ios-simulator ok"
