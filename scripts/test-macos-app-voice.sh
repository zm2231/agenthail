#!/usr/bin/env bash
set -euo pipefail

app="${1:?usage: scripts/test-macos-app-voice.sh <Agenthail.app>}"
entitlements="$(mktemp "${TMPDIR:-/tmp}/agenthail-entitlements.XXXXXX")"
trap 'rm -f "$entitlements"' EXIT

codesign -d --entitlements :- "$app" >"$entitlements" 2>/dev/null
if [ "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.security.device.audio-input' "$entitlements")" != "true" ]; then
	echo "error: $app is not signed with the audio-input entitlement" >&2
	exit 1
fi
signature="$(codesign -d --verbose=2 "$app" 2>&1)"
if ! [[ "$signature" =~ flags=[^[:space:]]*runtime ]]; then
	echo "error: $app is not signed with the hardened runtime" >&2
	exit 1
fi
if [ -z "$(/usr/libexec/PlistBuddy -c 'Print :NSMicrophoneUsageDescription' "$app/Contents/Info.plist" 2>/dev/null)" ]; then
	echo "error: $app has no NSMicrophoneUsageDescription" >&2
	exit 1
fi
