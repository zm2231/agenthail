#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
cleanup() {
	[ ! -f "$work/child.pid" ] || kill "$(cat "$work/child.pid")" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

alive() {
	kill -0 "$(cat "$1")" 2>/dev/null
}

cat >"$work/tests" <<'SH'
#!/usr/bin/env bash
echo $$ >"$WORK/tests.pid"
if [ "$CHILD" = yes ]; then
	sleep 60 &
	echo $! >"$WORK/child.pid"
fi
for seconds in $DURATIONS; do
	echo "Test Case '-[AgenthailIOSUITests.Suite test$seconds]' $RESULT ($seconds.123 seconds)."
done
if [ "$HANG" = yes ]; then
	wait
fi
echo "finished" >"$WORK/mark"
exit "$STATUS"
SH
chmod +x "$work/tests"

run() {
	[ ! -f "$work/child.pid" ] || kill "$(cat "$work/child.pid")" 2>/dev/null || true
	rm -f "$work/mark" "$work/tests.pid" "$work/child.pid"
	WORK="$work" DURATIONS="$1" RESULT="$2" STATUS="$3" CHILD="$4" HANG="$5" AGENTHAIL_IOS_SLOW_TEST_SECONDS=200 \
		"$ROOT/scripts/ios-test-stall-guard.sh" "$work/tests" >"$work/out" 2>&1
}

run "5 201 3 250" passed 0 no no || fail "two slow tests stopped the run: $(cat "$work/out")"
[ -f "$work/mark" ] || fail "the test command did not run to the end"
grep -q "test250\]' passed" "$work/out" || fail "test output was not passed through"

status=0
run "1 2" failed 65 no no || status=$?
[ "$status" = 65 ] || fail "the test command's status 65 came back as $status"

status=0
run "1" passed 7 yes no || status=$?
[ "$status" = 7 ] || fail "with a child holding the output open, status 7 came back as $status"
alive "$work/child.pid" || fail "the child ended before the guard returned, so the guard may have waited for it"

status=0
run "201 3 300 400" failed 0 yes yes || status=$?
[ "$status" = 1 ] || fail "three slow tests did not stop the run: status $status"
[ ! -f "$work/mark" ] || fail "the test command ran to the end"
if alive "$work/tests.pid"; then fail "the test command is still running"; fi
alive "$work/child.pid" || fail "the child ended before the guard returned, so the guard may have waited for it"
grep -q "3 tests each took over 200 s" "$work/out" || fail "missing the stop message: $(cat "$work/out")"
grep -q "Suite test400" "$work/out" || fail "the stop message does not name the slow tests"

echo "ios-test-stall-guard tests passed"
