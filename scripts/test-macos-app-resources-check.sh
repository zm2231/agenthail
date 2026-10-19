#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-resources.XXXXXX")"
trap 'rm -rf "$work"' EXIT

make_app() {
	local app="$work/$1.app"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources/Fixture_Fixture.bundle"
	printf '%s\n' "${@:2}" >"$app/Contents/MacOS/Agenthail"
	echo "$app"
}

expect() {
	local want="$1" app="$2" message="$3"
	if "$ROOT/scripts/test-macos-app-resources.sh" "$app" >/dev/null 2>&1; then got=0; else got=1; fi
	if [ "$got" != "$want" ]; then
		echo "error: $message" >&2
		exit 1
	fi
}

expect 0 "$(make_app swiftbuild 'unable to find bundle named Fixture_Fixture')" "a Swift Build accessor with its bundle in Contents/Resources was rejected"
expect 1 "$(make_app missing 'unable to find bundle named Other_Other')" "a Swift Build accessor whose bundle is missing was accepted"
expect 1 "$(make_app native 'could not load resource bundle: from ' '/build/release/Fixture_Fixture.bundle')" "a native build system accessor was accepted"
expect 1 "$(make_app buildpath '/Users/runner/work/native/.build/release/Fixture_Fixture.bundle')" "an absolute build path to a resource bundle was accepted"
echo "resource bundle check rejects native accessors and missing bundles"
