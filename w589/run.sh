#!/bin/bash
# comment-checker-disable-file
# Real-window capture for W589. Run from a clone of this branch on mini-2: bash w589/run.sh
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"; kit="$repo/w589"
dir=$(mktemp -d /tmp/w589rw.XXXX); mkdir -p "$dir/t" "$dir/apptmp"
echo "run dir $dir, commit $(git -C "$repo" rev-parse --short HEAD)"
(cd "$repo" && CGO_ENABLED=0 go build -o "$dir/agenthail" ./cmd/agenthail && go build -o "$dir/harness" ./cmd/ios-live-harness)
AGENTHAIL_CLI_SOURCE="$dir/agenthail" "$repo/scripts/build-macos-app.sh" "$dir/Agenthail.app" >"$dir/build.log" 2>&1 || { tail -40 "$dir/build.log"; exit 1; }
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier com.agenthail.app.w589" "$dir/Agenthail.app/Contents/Info.plist"
codesign --force --deep --options runtime --entitlements "$repo/packaging/macos-app-entitlements.plist" --sign - "$dir/Agenthail.app"
swiftc -O "$kit/windowid.swift" -o "$dir/windowid"
swiftc -O "$kit/overlap.swift" -o "$dir/overlap"
base=$(cd "$dir/t" && pwd -P)
TMPDIR="$base/" W589_BASE="$base" "$dir/harness" -agenthail "$dir/agenthail" -hosts 1 -- bash "$kit/inner.sh" "$kit" "$dir" || echo "harness exit $?"
out="$dir/out"
mkdir -p "$dir/return"
cp "$out/drive.log" "$out/app.log" "$out/suspects.txt" "$dir/return/" 2>/dev/null || true
cp "$out/capture.err" "$dir/return/" 2>/dev/null || true
awk '{print $1}' "$out/suspects.txt" | head -60 | xargs -I{} cp {} "$dir/return/" 2>/dev/null || true
ls "$out"/frames/*.jpg 2>/dev/null | awk 'NR % 20 == 1' | xargs -I{} cp {} "$dir/return/" 2>/dev/null || true
tar czf "$dir/w589-return.tgz" -C "$dir/return" .
echo "send back: $dir/w589-return.tgz (all frames stay in $out/frames)"
