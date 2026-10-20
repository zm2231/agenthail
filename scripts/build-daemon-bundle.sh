#!/usr/bin/env bash
# Builds the Agenthail Daemon bundle around an agenthail binary. macOS keeps a
# privacy grant for the process responsible for a request, and every process
# the daemon starts inherits the daemon as that process. As the main
# executable of a signed bundle, the daemon is known to macOS by the bundle's
# identifier and team, so its grants survive every update, and its hardened
# runtime may send Apple Events, which it may not without the entitlement.
#
# usage: build-daemon-bundle.sh <Agenthail Daemon.app> <agenthail binary>
# AGENTHAIL_CODESIGN_IDENTITY signs it ("-" signs ad hoc, for local builds).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT="${1:?usage: build-daemon-bundle.sh <Agenthail Daemon.app> <agenthail binary>}"
BINARY="${2:?usage: build-daemon-bundle.sh <Agenthail Daemon.app> <agenthail binary>}"
IDENTITY="${AGENTHAIL_CODESIGN_IDENTITY:?AGENTHAIL_CODESIGN_IDENTITY is required}"
VERSION="${AGENTHAIL_APP_VERSION:-0.0.0}"
BUILD="${AGENTHAIL_APP_BUILD:-0}"
BUNDLE_ID=com.agenthail.daemon
MACOS_MINIMUM=15.0

rm -rf "$OUTPUT"
mkdir -p "$OUTPUT/Contents/MacOS"
cat >"$OUTPUT/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Agenthail Daemon</string>
  <key>CFBundleExecutable</key><string>agenthail</string>
  <key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Agenthail Daemon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$BUILD</string>
  <key>LSBackgroundOnly</key><true/>
  <key>LSMinimumSystemVersion</key><string>$MACOS_MINIMUM</string>
  <key>NSAppleEventsUsageDescription</key><string>Agents you run with Agenthail control the apps you allow, such as Codex Computer Use for desktop automation.</string>
</dict></plist>
PLIST
plutil -lint "$OUTPUT/Contents/Info.plist" >/dev/null

cp "$BINARY" "$OUTPUT/Contents/MacOS/agenthail"
xcrun swiftc -O -target "arm64-apple-macosx$MACOS_MINIMUM" -o "$OUTPUT/Contents/MacOS/agenthail-permissions" "$ROOT/packaging/daemon/permissions.swift"
chmod 0755 "$OUTPUT/Contents/MacOS/agenthail" "$OUTPUT/Contents/MacOS/agenthail-permissions"

sign_args=(--force --options runtime --sign "$IDENTITY")
if [ "$IDENTITY" != "-" ]; then
	sign_args+=(--timestamp)
fi
"$ROOT/scripts/codesign-with-retry.sh" "${sign_args[@]}" "$OUTPUT/Contents/MacOS/agenthail-permissions"
"$ROOT/scripts/codesign-with-retry.sh" "${sign_args[@]}" --entitlements "$ROOT/packaging/daemon/entitlements.plist" "$OUTPUT"
codesign --verify --deep --strict --verbose=2 "$OUTPUT"

# A grant is kept for the designated requirement. With a Developer ID it must
# name the bundle and the team, not the build, so every update keeps it.
if [ "$IDENTITY" != "-" ]; then
	requirement="$(codesign -d -r- "$OUTPUT" 2>&1)"
	case "$requirement" in
	*"identifier \"$BUNDLE_ID\" and anchor apple generic"*"certificate leaf[subject.OU] = "*) ;;
	*)
		printf 'error: the daemon bundle does not require its identifier and team:\n%s\n' "$requirement" >&2
		exit 1
		;;
	esac
fi
codesign -d --entitlements - --xml "$OUTPUT" 2>/dev/null | grep -q 'com.apple.security.automation.apple-events' || {
	echo "error: the daemon bundle lacks the Apple Events entitlement" >&2
	exit 1
}
