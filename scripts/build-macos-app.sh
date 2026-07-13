#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT="${1:-$ROOT/dist/Agenthail.app}"
ARCH="${2:-$(uname -m)}"
if [ "${AGENTHAIL_CODESIGN_IDENTITY+x}" = "x" ]; then
  IDENTITY="$AGENTHAIL_CODESIGN_IDENTITY"
else
  IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application:/ {print $2; exit}')"
  IDENTITY="${IDENTITY:--}"
fi

rm -rf "$OUTPUT"
mkdir -p "$OUTPUT/Contents/MacOS" "$OUTPUT/Contents/Resources"

cat >"$OUTPUT/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Agenthail</string>
  <key>CFBundleExecutable</key><string>Agenthail</string>
  <key>CFBundleIdentifier</key><string>com.agenthail.app</string>
  <key>CFBundleIconFile</key><string>Agenthail</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Agenthail</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSUserNotificationAlertStyle</key><string>alert</string>
</dict></plist>
PLIST

ICONSET="$(mktemp -d)/Agenthail.iconset"
mkdir -p "$ICONSET"
/usr/bin/swift "$ROOT/native/GenerateIcon.swift" "$ICONSET/icon_512x512@2x.png"
for spec in "16:icon_16x16.png" "32:icon_16x16@2x.png" "32:icon_32x32.png" "64:icon_32x32@2x.png" "128:icon_128x128.png" "256:icon_128x128@2x.png" "256:icon_256x256.png" "512:icon_256x256@2x.png" "512:icon_512x512.png"; do
  dimension="${spec%%:*}"
  filename="${spec#*:}"
  /usr/bin/sips -z "$dimension" "$dimension" "$ICONSET/icon_512x512@2x.png" --out "$ICONSET/$filename" >/dev/null
done
/usr/bin/iconutil -c icns "$ICONSET" -o "$OUTPUT/Contents/Resources/Agenthail.icns"
rm -rf "$(dirname "$ICONSET")"

/usr/bin/swiftc \
  -parse-as-library \
  -O \
  -target "${ARCH}-apple-macos13.0" \
  "$ROOT/native/AgenthailApp.swift" \
  -o "$OUTPUT/Contents/MacOS/Agenthail"

codesign --force --deep --options runtime --sign "$IDENTITY" "$OUTPUT"
echo "$OUTPUT"
