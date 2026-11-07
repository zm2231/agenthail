#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

cat >"$work/tests" <<'SH'
#!/usr/bin/env bash
for seconds in $DURATIONS; do
	echo "Test Case '-[AgenthailIOSUITests.Suite test$seconds]' passed ($seconds.123 seconds)."
done
echo "finished" >"$MARK"
exit "$STATUS"
SH
chmod +x "$work/tests"

run() {
	DURATIONS="$1" STATUS="$2" MARK="$work/mark" AGENTHAIL_IOS_SLOW_TEST_SECONDS=200 \
		"$ROOT/scripts/ios-test-stall-guard.sh" "$work/tests" >"$work/out" 2>&1
}

rm -f "$work/mark"
run "5 201 3 250" 0 || fail "two slow tests stopped the run: $(cat "$work/out")"
[ -f "$work/mark" ] || fail "the test command did not run to the end"
grep -q "test250\]' passed" "$work/out" || fail "test output was not passed through"

rm -f "$work/mark"
if run "1 2" 65; then fail "the test command's failure was not passed on"; fi
[ -f "$work/mark" ] || fail "the failing test command did not run to the end"

cat >"$work/tests" <<'SH'
#!/usr/bin/env bash
for seconds in 201 3 300 400; do
	echo "Test Case '-[AgenthailIOSUITests.Suite test$seconds]' failed ($seconds.5 seconds)."
done
sleep 30
echo "finished" >"$MARK"
SH
rm -f "$work/mark"
start=$SECONDS
if run "" 0; then fail "three slow tests did not stop the run"; fi
[ $((SECONDS - start)) -lt 20 ] || fail "the guard waited for the test command to finish"
[ ! -f "$work/mark" ] || fail "the test command kept running"
grep -q "3 tests each took over 200 s" "$work/out" || fail "missing the stop message: $(cat "$work/out")"
grep -q "Suite test400" "$work/out" || fail "the stop message does not name the slow tests"

echo "ios-test-stall-guard tests passed"
