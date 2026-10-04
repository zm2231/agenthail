#!/usr/bin/env bash
set -euo pipefail

root="$(mktemp -d /tmp/agenthail-pkg-components.XXXXXX)"
components="$root/components.plist"
component="$root/component.pkg"
mkdir -p "$root/root/Applications/Agenthail.app/Contents/MacOS"
cat > "$root/root/Applications/Agenthail.app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.agenthail.fixture</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST

pkgbuild --analyze --root "$root/root" "$components"
scripts/normalize-pkg-components.sh "$components"
/usr/libexec/PlistBuddy -c 'Print :0:BundleIsRelocatable' "$components" | grep -Fx 'false'
/usr/libexec/PlistBuddy -c 'Set :0:BundleIsRelocatable true' "$components"
scripts/normalize-pkg-components.sh "$components"
/usr/libexec/PlistBuddy -c 'Print :0:BundleIsRelocatable' "$components" | grep -Fx 'false'
COPYFILE_DISABLE=1 pkgbuild --root "$root/root" --component-plist "$components" --identifier com.agenthail.fixture --version 1.0 --install-location / "$component"
pkgutil --expand-full "$component" "$root/expanded"
test -f "$root/expanded/Agenthail-fixture-component.pkg/PackageInfo" || test -f "$root/expanded/PackageInfo"
echo "package component plist normalization verified: $component"
