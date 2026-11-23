#!/bin/bash
# comment-checker-disable-file
# Real-window capture for W589. From a clone of this branch on mini-2: bash w589/run.sh
# Builds a renamed test copy of the app under a bundle id never used before, runs it against a scratch daemon with a busy
# Claude turn, has it save its own window ~10 times a second, then removes everything except the output it prints.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"; kit="$repo/w589"
stamp=$(date +%Y%m%d%H%M%S)
bundle="com.agenthail.w589.r$stamp$RANDOM"
dir=$(mktemp -d /tmp/w589rw.XXXX)
keep="/tmp/w589-out-$stamp"
mkdir -p "$keep"
real_app="/Applications/Agenthail.app/Contents/MacOS/Agenthail"
before=$(pgrep -f "^$real_app" | sort | tr '\n' ' ' || true)

cleanup() {
  set +e
  pkill -TERM -f "^$dir/Agenthail.app/Contents/MacOS/AgenthailW589" 2>/dev/null
  /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u "$dir/Agenthail.app" 2>/dev/null
  defaults delete "$bundle" 2>/dev/null
  rm -f "$HOME/Library/Preferences/$bundle.plist"
  rm -rf "$HOME/Library/Saved Application State/$bundle.savedState" "$HOME/Library/Caches/$bundle" "$HOME/Library/HTTPStorages/$bundle"
  after=$(pgrep -f "^$real_app" | sort | tr '\n' ' ' || true)
  if [ "$after" != "$before" ]; then echo "WARNING: /Applications/Agenthail.app processes changed during the run: before [$before] after [$after]" | tee -a "$keep/guard.txt"; fi
  rm -rf "$dir"
}
trap cleanup EXIT

echo "run dir $dir, commit $(git -C "$repo" rev-parse --short HEAD), bundle id $bundle"
(cd "$repo" && CGO_ENABLED=0 go build -o "$dir/agenthail" ./cmd/agenthail && go build -o "$dir/harness" ./cmd/ios-live-harness)
AGENTHAIL_CODESIGN_IDENTITY=- AGENTHAIL_CLI_SOURCE="$dir/agenthail" "$repo/scripts/build-macos-app.sh" "$dir/Agenthail.app" >"$keep/build.log" 2>&1 || { tail -40 "$keep/build.log"; exit 1; }
plist="$dir/Agenthail.app/Contents/Info.plist"
mv "$dir/Agenthail.app/Contents/MacOS/Agenthail" "$dir/Agenthail.app/Contents/MacOS/AgenthailW589"
/usr/libexec/PlistBuddy -c "Set :CFBundleExecutable AgenthailW589" -c "Set :CFBundleIdentifier $bundle" -c "Set :CFBundleName Agenthail W589" -c "Set :CFBundleDisplayName Agenthail W589" \
  -c "Delete :CFBundleURLTypes" -c "Delete :NSServices" "$plist"
clang -dynamiclib -fobjc-arc -framework AppKit -o "$dir/w589capture.dylib" "$kit/capture.m"
codesign --force --sign - "$dir/w589capture.dylib"
codesign --force --deep --entitlements "$repo/packaging/macos-app-entitlements.plist" --sign - "$dir/Agenthail.app"
swiftc -O "$kit/overlap.swift" -o "$dir/overlap"
mkdir -p "$dir/t"
base=$(cd "$dir/t" && pwd -P)
status=0
TMPDIR="$base/" W589_BASE="$base" "$dir/harness" -agenthail "$dir/agenthail" -hosts 1 -- bash "$kit/inner.sh" "$kit" "$dir" "$bundle" || status=$?
out="$dir/out"
cp "$out/drive.log" "$out/app.log" "$out/suspects.txt" "$out/frames/capture.log" "$keep/" 2>/dev/null || true
mkdir -p "$keep/frames"
cp "$out"/frames/*.jpg "$keep/frames/" 2>/dev/null || true
mkdir -p "$keep/return"
cp "$keep"/*.log "$keep"/*.txt "$keep/return/" 2>/dev/null || true
awk '{print $1}' "$keep/suspects.txt" 2>/dev/null | head -60 | while read -r f; do cp "$keep/frames/$(basename "$f")" "$keep/return/" 2>/dev/null || true; done
ls "$keep"/frames/*.jpg 2>/dev/null | awk 'NR % 20 == 1' | while read -r f; do cp "$f" "$keep/return/"; done
tar czf "$keep/w589-return.tgz" -C "$keep/return" .
echo "harness exit $status"
echo "send back: $keep/w589-return.tgz"
echo "all frames: $keep/frames ($(ls "$keep"/frames | wc -l | tr -d ' ') files); delete $keep when done"
