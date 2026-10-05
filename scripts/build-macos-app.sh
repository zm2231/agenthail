#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT="${1:-$ROOT/dist/Agenthail.app}"
ARCH="${2:-$(uname -m)}"
CLI_SOURCE="${AGENTHAIL_CLI_SOURCE:-}"
ICON_SOURCE="${AGENTHAIL_ICON_SOURCE:-$ROOT/docs/brand/logo-512.png}"
MENU_ICON_SOURCE="${AGENTHAIL_MENU_ICON_SOURCE:-$ROOT/docs/brand/internal/menubar-v1-18.png}"
MENU_ICON_2X_SOURCE="${AGENTHAIL_MENU_ICON_2X_SOURCE:-$ROOT/docs/brand/internal/menubar-v1-36.png}"
APP_VERSION="${AGENTHAIL_APP_VERSION:-$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)}"
APP_VERSION="${APP_VERSION#v}"
APP_VERSION="${APP_VERSION%%-*}"
APP_BUILD="${AGENTHAIL_APP_BUILD:-$(git -C "$ROOT" rev-list --count HEAD 2>/dev/null || echo 1)}"
if [ "${AGENTHAIL_CODESIGN_IDENTITY+x}" = "x" ]; then
	IDENTITY="$AGENTHAIL_CODESIGN_IDENTITY"
else
	IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application:/ {print $2; exit}')"
	IDENTITY="${IDENTITY:--}"
fi

if [ -z "$CLI_SOURCE" ]; then
	CLI_SOURCE="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-cli-build.XXXXXX")/agenthail"
	trap 'rm -rf "$(dirname "$CLI_SOURCE")"' EXIT
	LDFLAGS=""
	if [ -n "${AGENTHAIL_PUSH_RELAY_URL:-}" ]; then
		LDFLAGS="-X github.com/zm2231/agenthail/internal/daemon.bundledPushRelayURL=$AGENTHAIL_PUSH_RELAY_URL"
	fi
	(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -trimpath -ldflags "$LDFLAGS" -o "$CLI_SOURCE" ./cmd/agenthail)
fi
if [ ! -x "$CLI_SOURCE" ]; then
	echo "error: Agenthail CLI is unavailable at $CLI_SOURCE" >&2
	exit 1
fi
if [ ! -f "$MENU_ICON_SOURCE" ] || [ ! -f "$MENU_ICON_2X_SOURCE" ]; then
	echo "error: Agenthail menu bar artwork is unavailable" >&2
	exit 1
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
  <key>CFBundleShortVersionString</key><string>APP_VERSION</string>
  <key>CFBundleVersion</key><string>APP_BUILD</string>
  <key>NSServices</key><array><dict>
    <key>NSMenuItem</key><dict><key>default</key><string>Send to Agenthail</string></dict>
    <key>NSMessage</key><string>sendToAgenthail</string>
    <key>NSPortName</key><string>Agenthail</string>
    <key>NSSendTypes</key><array><string>public.utf8-plain-text</string></array>
  </dict></array>
  <key>CFBundleURLTypes</key><array><dict>
    <key>CFBundleURLName</key><string>com.agenthail.app</string>
    <key>CFBundleURLSchemes</key><array><string>agenthail</string></array>
  </dict></array>
  <key>LSMinimumSystemVersion</key><string>15.0</string>
  <key>LSUIElement</key><true/>
  <key>NSUserNotificationAlertStyle</key><string>alert</string>
</dict></plist>
PLIST
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $APP_VERSION" "$OUTPUT/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $APP_BUILD" "$OUTPUT/Contents/Info.plist"

ICON_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-icons.XXXXXX")"
ICONSET="$ICON_ROOT/Agenthail.iconset"
mkdir -p "$ICONSET"
if [ ! -f "$ICON_SOURCE" ]; then
	ICON_SOURCE="$ICON_ROOT/icon-master.png"
	/usr/bin/swift "$ROOT/native/GenerateIcon.swift" "$ICON_SOURCE"
fi
for spec in "16:icon_16x16.png" "32:icon_16x16@2x.png" "32:icon_32x32.png" "64:icon_32x32@2x.png" "128:icon_128x128.png" "256:icon_128x128@2x.png" "256:icon_256x256.png" "512:icon_256x256@2x.png" "512:icon_512x512.png" "1024:icon_512x512@2x.png"; do
	dimension="${spec%%:*}"
	filename="${spec#*:}"
	/usr/bin/sips -z "$dimension" "$dimension" "$ICON_SOURCE" --out "$ICONSET/$filename" >/dev/null
done
/usr/bin/iconutil -c icns "$ICONSET" -o "$OUTPUT/Contents/Resources/Agenthail.icns"
rm -rf "$ICON_ROOT"
cp "$MENU_ICON_SOURCE" "$OUTPUT/Contents/Resources/AgenthailMenuBarIcon.png"
cp "$MENU_ICON_2X_SOURCE" "$OUTPUT/Contents/Resources/AgenthailMenuBarIcon@2x.png"

cp "$CLI_SOURCE" "$OUTPUT/Contents/Resources/agenthail"
"$ROOT/scripts/codesign-with-retry.sh" --force --options runtime --sign "$IDENTITY" "$OUTPUT/Contents/Resources/agenthail"
SWIFT_ARCH="$ARCH"
[ "$SWIFT_ARCH" = "amd64" ] && SWIFT_ARCH="x86_64"
SWIFT_BIN="$(swift build --package-path "$ROOT/native" -c release --arch "$SWIFT_ARCH" --show-bin-path)"
rm -rf "$SWIFT_BIN"/*.bundle
swift build --package-path "$ROOT/native" -c release --arch "$SWIFT_ARCH" --product AgenthailMac >/dev/null
cp "$SWIFT_BIN/AgenthailMac" "$OUTPUT/Contents/MacOS/Agenthail"
for bundle in "$SWIFT_BIN"/*.bundle; do
	cp -R "$bundle" "$OUTPUT/Contents/Resources/"
done
"$ROOT/scripts/codesign-with-retry.sh" --force --deep --options runtime --sign "$IDENTITY" "$OUTPUT"
codesign --verify --deep --strict --verbose=2 "$OUTPUT"
echo "$OUTPUT"
