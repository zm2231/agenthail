#!/bin/sh
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
target=${CARGO_TARGET_DIR:-/Volumes/4/caches/agenthail-v2-target}
out=${1:-/Volumes/4/caches/agenthail-views-app}
(cd "$here" && CARGO_TARGET_DIR="$target" cargo build --release)
rm -rf "$out/views.app" "$out/Agenthail Views.app"
sh "$here/../scripts/bundle.sh" views "$target/release/agenthail-views" "$out" >/dev/null
plutil -replace CFBundleName -string "Agenthail Views" "$out/views.app/Contents/Info.plist"
plutil -replace CFBundleDisplayName -string "Agenthail Views" "$out/views.app/Contents/Info.plist"
mv "$out/views.app" "$out/Agenthail Views.app"
codesign --force --sign - "$out/Agenthail Views.app" >/dev/null 2>&1
echo "$out/Agenthail Views.app"
