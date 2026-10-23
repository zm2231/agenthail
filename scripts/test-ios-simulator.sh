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
  "simctl boot"*|"simctl bootstatus"*) ;;
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
chmod +x "$WORK/bin/"*
export PATH="$WORK/bin:$PATH" LOCKED="$WORK/locked"

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

run 2 xcodebuild test -scheme AgenthailIOS -derivedDataPath /Volumes/disk/dd
[ -z "$locked" ] || fail "derived data under /Volumes took the simulator"

(cd /Volumes && run 2 xcodebuild test -scheme AgenthailIOS -derivedDataPath dd && [ -z "$locked" ]) || fail "relative derived data under /Volumes took the simulator"

XCODE_DERIVED_DATA=/Volumes/disk/DerivedData run 2 xcodebuild test -scheme AgenthailIOS
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

if [ "$failures" -gt 0 ]; then
  exit 1
fi
echo "ios-simulator ok"
