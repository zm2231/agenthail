#!/bin/sh
# Builds the spike client into a bundle with its own identifier and starts it
# against a daemon. Usage: run.sh [debug|release]; prints the PID.
set -eu
profile=${1:-release}
work=${GPUI_SPIKE_DIR:?set GPUI_SPIKE_DIR to a scratch directory}
here=$(cd "$(dirname "$0")/.." && pwd)
flag=""; [ "$profile" = release ] && flag="--release"
cargo build $flag --manifest-path "$here/Cargo.toml" >&2
app="$work/AgenthailGPUI.app"
mkdir -p "$app/Contents/MacOS"
cp "${CARGO_TARGET_DIR:-$here/target}/$profile/agenthail-gpui" "$app/Contents/MacOS/agenthail-gpui"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.agenthail.gpui-spike</string>
<key>CFBundleName</key><string>Agenthail GPUI</string>
<key>CFBundleExecutable</key><string>agenthail-gpui</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.0.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>LSMinimumSystemVersion</key><string>13.0</string>
</dict></plist>
PLIST
codesign --force --sign - "$app" >&2
listen=${AGENTHAIL_LISTEN:-127.0.0.1:7412}
token_file=${AGENTHAIL_TOKEN_FILE:?set AGENTHAIL_TOKEN_FILE}
mkdir -p "$work/home"
AGENTHAIL_API_URL="http://$listen" AGENTHAIL_API_TOKEN=$(cat "$token_file") \
HOME="$work/home" CFFIXED_USER_HOME="$work/home" AGENTHAIL_GPUI_PROBE="$work/probe.log" \
  "$app/Contents/MacOS/agenthail-gpui" >"$work/app.log" 2>&1 &
echo $!
