#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir -p "$work/src"
cat >"$work/src/AUITests.swift" <<'SWIFT'
import XCTest

@MainActor
final class AUITests: XCTestCase {
    private func launch() {}
    func testOne() {}
    func testTwo() throws {}
}

extension XCUIApplication {
    func testHelperInTheSameFile() {}
}

private struct Probe {
    func testShapedHelper() {}
}
SWIFT
cat >"$work/src/Helpers.swift" <<'SWIFT'
extension XCUIElement {
    func testLike() {}
}
SWIFT

run() {
	UI_TEST_TIERS="$work/tiers.txt" UI_TEST_SOURCES="$work/src" "$ROOT/scripts/ios-ui-tests.sh" "$@"
}

printf '# header\nsmoke AUITests/testOne\n\nnightly AUITests/testTwo\n' >"$work/tiers.txt"
run check >/dev/null || fail "a list naming every test passes"

printf 'smoke AUITests/testOne\n' >"$work/tiers.txt"
out="$(run check 2>&1)" && fail "a test in no tier passes the check"
[[ "$out" == *"in no tier"*"AUITests/testTwo"* ]] || fail "the missing test is named: $out"

printf 'smoke AUITests/testOne\nmain AUITests/testTwo\nmain AUITests/testGone\n' >"$work/tiers.txt"
out="$(run check 2>&1)" && fail "a listed test that does not exist passes the check"
[[ "$out" == *"not a test"*"AUITests/testGone"* ]] || fail "the stale entry is named: $out"

printf 'smoke AUITests/testOne\nmain AUITests/testOne\nmain AUITests/testTwo\n' >"$work/tiers.txt"
out="$(run check 2>&1)" && fail "a test in two tiers passes the check"
[[ "$out" == *"more than once"* ]] || fail "the duplicate is named: $out"

printf 'weekly AUITests/testOne\nmain AUITests/testTwo\n' >"$work/tiers.txt"
run check >/dev/null 2>&1 && fail "an unknown tier passes the check"

printf 'smoke AUITests/testOne\nnightly AUITests/testTwo\n' >"$work/tiers.txt"
[ "$(run args all smoke)" = "$(printf '%s\n' -only-testing:AgenthailIOSTests -only-testing:AgenthailIOSUITests/AUITests/testOne)" ] || fail "smoke args"
[ "$(run args all smoke main nightly | wc -l | tr -d ' ')" = 3 ] || fail "every tier runs every test and the unit tests"

printf 'smoke AUITests/testOne\nmain AUITests/testTwo alone\n' >"$work/tiers.txt"
run check >/dev/null || fail "a test marked alone passes the check"
[ "$(run args parallel smoke main)" = "$(printf '%s\n' -only-testing:AgenthailIOSTests -only-testing:AgenthailIOSUITests/AUITests/testOne)" ] || fail "the parallel set leaves out a test marked alone"
[ "$(run args alone smoke main)" = -only-testing:AgenthailIOSUITests/AUITests/testTwo ] || fail "the alone set is only the tests marked alone, without the unit tests"
[ -z "$(run args alone smoke)" ] || fail "the alone set keeps to the tiers asked for"
[ "$(run args all smoke main | wc -l | tr -d ' ')" = 3 ] || fail "all runs every test of the tiers"
printf 'smoke AUITests/testOne\nmain AUITests/testTwo serial\n' >"$work/tiers.txt"
run check >/dev/null 2>&1 && fail "a third field other than alone passes the check"
run args smoke >/dev/null 2>&1 && fail "args without a set is refused"

printf 'smoke AUITests/testOne\nlive AUITests/testTwo\n' >"$work/tiers.txt"
run check >/dev/null || fail "a live test passes the check"
[ "$(run args all smoke main nightly | wc -l | tr -d ' ')" = 2 ] || fail "the ordinary tiers never run a live test, which needs the live harness"
[ "$(run args all live | tail -1)" = -only-testing:AgenthailIOSUITests/AUITests/testTwo ] || fail "live args"
printf 'ui-all\n' >"$work/labels"
[[ " $(run tiers pull_request /dev/null "$work/labels") " != *" live "* ]] || fail "ui-all does not add the live tier"

: >"$work/labels"
printf 'docs/readme.md\n' >"$work/changed"
[ "$(run tiers pull_request "$work/changed" "$work/labels")" = smoke ] || fail "an unrelated change runs smoke"
for path in native/iOSUITests/AUITests.swift native/iOS/QueueTail.swift native/iOS/AgenthailIOSViews.swift native/iOS/SessionPreview.swift native/ui-test-tiers.txt; do
	printf '%s\n' "$path" >"$work/changed"
	[ "$(run tiers pull_request "$work/changed" "$work/labels")" = "smoke main" ] || fail "$path widens to the main tier"
done
printf 'native/iOS/Settings.swift\n' >"$work/changed"
[ "$(run tiers pull_request "$work/changed" "$work/labels")" = smoke ] || fail "an app change outside the transcript runs smoke"
printf 'full-ci\nui-all\n' >"$work/labels"
[ "$(run tiers pull_request "$work/changed" "$work/labels")" = "smoke main nightly" ] || fail "ui-all runs every tier"
[ "$(run tiers push "$work/changed" "$work/labels")" = "smoke main" ] || fail "a push to main runs smoke and main"

echo "ios-ui-tests tests passed"
