#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
	echo "error: Agenthail.pkg currently targets Apple silicon macOS" >&2
	exit 1
fi

dirty=0
if [ -n "$(git status --porcelain)" ]; then
	dirty=1
fi
if [ "$dirty" -eq 1 ] && [ "${AGENTHAIL_ALLOW_DIRTY:-0}" != "1" ]; then
	echo "error: package builds require a clean worktree" >&2
	exit 1
fi

revision="$(git rev-parse HEAD)"
version="${AGENTHAIL_VERSION:-$(git describe --tags --always)}"
if [ "$dirty" -eq 1 ]; then
	version="$version-dirty"
fi
package_version="${AGENTHAIL_PACKAGE_VERSION:-${version#v}}"
package_version="${package_version%%-*}"
if ! [[ "$package_version" =~ ^[0-9]+(\.[0-9]+){1,3}$ ]]; then
	package_version="0.0.0"
fi
built_at="$(date -u -r "$(git show -s --format=%ct HEAD)" '+%Y-%m-%dT%H:%M:%SZ')"
dist="$ROOT/dist"
work="$dist/.pkg-stage"
root="$work/root"
payload="$root/Library/Application Support/Agenthail"
component="$work/Agenthail-component.pkg"
components="$work/components.plist"
output="$dist/Agenthail-$version-arm64.pkg"
checksum="$output.sha256"

rm -rf "$work" "$output" "$checksum"
mkdir -p "$payload" "$root/Applications" "$root/usr/local/bin"
"$ROOT/scripts/build-runtime-bundle.sh" "$payload"

ldflags="-s -w -X main.version=$version -X main.revision=$revision -X main.builtAt=$built_at"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$ldflags" -o "$payload/agenthail" ./cmd/agenthail
cp sidecar/sidecar.py sidecar/cookie.mjs "$payload/"
cp -R skills "$payload/"
cp README.md LICENSE COMMERCIAL.md THIRD_PARTY_NOTICES.md "$payload/"
cp packaging/launcher.sh "$root/usr/local/bin/agenthail"
cp packaging/uninstall.sh "$root/usr/local/bin/agenthail-uninstall"
chmod 0755 "$payload/agenthail" "$root/usr/local/bin/agenthail" "$root/usr/local/bin/agenthail-uninstall"

if [ "${AGENTHAIL_CODESIGN_IDENTITY+x}" = "x" ]; then
	app_identity="$AGENTHAIL_CODESIGN_IDENTITY"
else
	app_identity="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application:/ {print $2; exit}')"
	app_identity="${app_identity:--}"
fi
if [ "${AGENTHAIL_INSTALLER_IDENTITY+x}" = "x" ]; then
	installer_identity="$AGENTHAIL_INSTALLER_IDENTITY"
else
	installer_identity="$(security find-identity -v -p basic 2>/dev/null | awk '/Developer ID Installer:/ {print $2; exit}')"
	installer_identity="${installer_identity:-}"
fi
if [ "${AGENTHAIL_ALLOW_UNNOTARIZED:-0}" != "1" ]; then
	[ "$app_identity" != "-" ] || { echo "error: Developer ID Application identity is required" >&2; exit 1; }
	[ -n "$installer_identity" ] || { echo "error: Developer ID Installer identity is required" >&2; exit 1; }
	[ -n "${AGENTHAIL_NOTARY_PROFILE:-}" ] || { echo "error: AGENTHAIL_NOTARY_PROFILE is required" >&2; exit 1; }
fi

sign_args=(--force --options runtime --sign "$app_identity")
if [ "$app_identity" != "-" ]; then
	sign_args+=(--timestamp)
fi
while IFS= read -r path; do
	if [ "$path" = "$payload/runtime/node/bin/node" ]; then
		continue
	fi
	if file "$path" | grep -q 'Mach-O'; then
		if [ "$app_identity" = "-" ] && [ "$path" = "$payload/runtime/python/bin/python3.13" ]; then
			codesign "${sign_args[@]}" --entitlements "$ROOT/packaging/python-local-entitlements.plist" "$path"
		else
			codesign "${sign_args[@]}" "$path"
		fi
	fi
done < <(find "$payload" -type f -perm -111 -o -type f \( -name '*.dylib' -o -name '*.so' \))
codesign --verify --strict --verbose=2 "$payload/agenthail"
codesign --verify --strict --verbose=2 "$payload/runtime/node/bin/node"

AGENTHAIL_CLI_SOURCE="$payload/agenthail" \
AGENTHAIL_CODESIGN_IDENTITY="$app_identity" \
AGENTHAIL_APP_VERSION="$package_version" \
AGENTHAIL_APP_BUILD="$(git rev-list --count HEAD)" \
	"$ROOT/scripts/build-macos-app.sh" "$root/Applications/Agenthail.app" arm64 >/dev/null

xattr -cr "$root"
pkgbuild --analyze --root "$root" "$components"
index=0
while /usr/libexec/PlistBuddy -c "Print :$index:RootRelativeBundlePath" "$components" >/dev/null 2>&1; do
	/usr/libexec/PlistBuddy -c "Set :$index:BundleIsRelocatable false" "$components"
	index=$((index + 1))
done
COPYFILE_DISABLE=1 pkgbuild --root "$root" --scripts "$ROOT/packaging/scripts" --component-plist "$components" --identifier com.agenthail.pkg --version "$package_version" --install-location / "$component"
if [ -n "$installer_identity" ]; then
	COPYFILE_DISABLE=1 productbuild --package "$component" --sign "$installer_identity" "$output"
else
	COPYFILE_DISABLE=1 productbuild --package "$component" "$output"
fi

pkgutil --check-signature "$output" || [ "${AGENTHAIL_ALLOW_UNNOTARIZED:-0}" = "1" ]
if [ -n "${AGENTHAIL_NOTARY_PROFILE:-}" ]; then
	echo "Submitting package for notarization"
	xcrun notarytool submit "$output" --keychain-profile "$AGENTHAIL_NOTARY_PROFILE" --no-s3-acceleration --wait --timeout "${AGENTHAIL_NOTARY_TIMEOUT:-20m}"
	xcrun stapler staple "$output"
	xcrun stapler validate "$output"
	spctl --assess --type install --verbose=2 "$output"
fi
shasum -a 256 "$output" > "$checksum"
echo "package: $output"
echo "checksum: $checksum"
