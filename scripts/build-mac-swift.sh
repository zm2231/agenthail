#!/usr/bin/env bash
# Usage: build-mac-swift.sh [arch]
# Builds the Mac app's Swift product in release and prints the directory that
# holds the binary and its resource bundles. The build lives in native/.build,
# which release CI restores from the cache scripts/mac-swift-cache-key.sh names.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SWIFT_ARCH="${1:-$(uname -m)}"
[ "$SWIFT_ARCH" = "amd64" ] && SWIFT_ARCH="x86_64"
MACOS_MINIMUM=15.0
SDK_VERSION="$(xcrun --sdk macosx --show-sdk-version)"
# Dependencies must share the app's deployment target, or mismatched async specializations abort (swiftlang/swift#86204).
SWIFT_TARGET="$SWIFT_ARCH-apple-macosx$MACOS_MINIMUM"
# Only Swift Build's resource accessors look in Contents/Resources; the native build system's accessors trap in a shipped app.
# Swift Build links with the deployment target as the SDK version, which turns off AppKit behavior keyed to the real SDK.
SWIFT_BUILD=(swift build --package-path "$ROOT/native" -c release --build-system swiftbuild --triple "$SWIFT_TARGET"
	-Xlinker -platform_version -Xlinker macos -Xlinker "$MACOS_MINIMUM" -Xlinker "$SDK_VERSION")
SWIFT_BIN="$("${SWIFT_BUILD[@]}" --show-bin-path)"
rm -rf "$SWIFT_BIN"/*.bundle
if ! SWIFT_LOG="$("${SWIFT_BUILD[@]}" --product AgenthailMac 2>&1)"; then
	printf '%s\n' "$SWIFT_LOG" >&2
	exit 1
fi
LINKED_SDK="$(otool -l "$SWIFT_BIN/AgenthailMac" | awk '/LC_BUILD_VERSION/ {found=1} found && $1 == "sdk" {print $2; exit}')"
if [ "$LINKED_SDK" != "$SDK_VERSION" ]; then
	echo "error: the Mac app is linked against macOS SDK '${LINKED_SDK:-unknown}', not the $SDK_VERSION SDK it was built with" >&2
	exit 1
fi
echo "$SWIFT_BIN"
