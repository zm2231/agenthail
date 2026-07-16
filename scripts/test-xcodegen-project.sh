#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cp -R "$repo/native" "$tmp/native"
rm -rf "$tmp/native/Agenthail.xcodeproj"
xcodegen generate --quiet --spec "$tmp/native/project.yml" --project "$tmp/native" --project-root "$tmp/native"

normalize() {
  sed -E '/^[[:space:]]*(compatibilityVersion|productRefGroup) = /d' "$1"
}

diff -u <(normalize "$repo/native/Agenthail.xcodeproj/project.pbxproj") <(normalize "$tmp/native/Agenthail.xcodeproj/project.pbxproj")
cmp "$repo/native/Agenthail.xcodeproj/project.xcworkspace/contents.xcworkspacedata" "$tmp/native/Agenthail.xcodeproj/project.xcworkspace/contents.xcworkspacedata"
cmp "$repo/native/Agenthail.xcodeproj/xcshareddata/xcschemes/AgenthailIOS.xcscheme" "$tmp/native/Agenthail.xcodeproj/xcshareddata/xcschemes/AgenthailIOS.xcscheme"
cmp "$repo/native/iOS/Info.plist" "$tmp/native/iOS/Info.plist"
