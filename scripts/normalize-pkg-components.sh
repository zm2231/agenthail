#!/usr/bin/env bash
set -euo pipefail

components="${1:?usage: scripts/normalize-pkg-components.sh <components.plist>}"
index=0
while /usr/libexec/PlistBuddy -c "Print :$index:RootRelativeBundlePath" "$components" >/dev/null 2>&1; do
	if /usr/libexec/PlistBuddy -c "Print :$index:BundleIsRelocatable" "$components" >/dev/null 2>&1; then
		/usr/libexec/PlistBuddy -c "Set :$index:BundleIsRelocatable false" "$components"
	else
		/usr/libexec/PlistBuddy -c "Add :$index:BundleIsRelocatable bool false" "$components"
	fi
	index=$((index + 1))
done
