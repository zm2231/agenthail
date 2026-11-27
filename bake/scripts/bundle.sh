#!/bin/sh
set -eu
name=$1
bin=$2
out=$3
app="$out/$name.app"
links=
if [ "$(basename "$bin")" = agenthail-rune ]; then
	scheme=$("$bin" --link-scheme)
	links="<key>CFBundleURLTypes</key><array><dict><key>CFBundleURLName</key><string>com.agenthail.bake.$name.link</string><key>CFBundleURLSchemes</key><array><string>$scheme</string></array></dict></array>"
fi
mkdir -p "$app/Contents/MacOS"
cp "$bin" "$app/Contents/MacOS/$name"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.agenthail.bake.$name</string>
<key>CFBundleName</key><string>$name</string>
<key>CFBundleExecutable</key><string>$name</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSMicrophoneUsageDescription</key><string>Agenthail uses the microphone when you talk to your agents.</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
$links
</dict></plist>
PLIST
codesign --force --sign - "$app" >/dev/null 2>&1
echo "$app"
