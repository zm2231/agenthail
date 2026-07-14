#!/usr/bin/env bash
set -euo pipefail

pkg="${1:?usage: scripts/test-pkg-artifact.sh <Agenthail.pkg>}"
pkg="$(cd "$(dirname "$pkg")" && pwd)/$(basename "$pkg")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

pkgutil --check-signature "$pkg" || [ "${AGENTHAIL_ALLOW_UNNOTARIZED:-0}" = "1" ]
pkgutil --expand-full "$pkg" "$work/expanded"
component="$work/expanded/Agenthail-component.pkg"
payload="$component/Payload"
root="$payload/Library/Application Support/Agenthail"
app="$payload/Applications/Agenthail.app"
package_info="$component/PackageInfo"

test -x "$root/agenthail"
test -x "$root/runtime/python/bin/python3"
test -x "$root/runtime/node/bin/node"
test -f "$root/pydeps/curl_cffi/__init__.py"
test -f "$root/node_modules/@steipete/sweet-cookie/package.json"
test -f "$root/skills/agenthail-operations/SKILL.md"
test -x "$payload/usr/local/bin/agenthail"
test -x "$payload/usr/local/bin/agenthail-uninstall"
test -x "$app/Contents/MacOS/Agenthail"
if [ "$(xmllint --xpath 'count(/pkg-info/relocate/bundle)' "$package_info")" != "0" ]; then
	echo "error: Agenthail.app is relocatable" >&2
	exit 1
fi

if find "$payload" -name '._*' -print -quit | grep -q .; then
	echo "error: package contains AppleDouble metadata" >&2
	exit 1
fi

bash -n "$payload/usr/local/bin/agenthail" "$payload/usr/local/bin/agenthail-uninstall" "$component/Scripts/preinstall" "$component/Scripts/postinstall"
codesign --verify --deep --strict --verbose=2 "$app"
codesign --verify --strict --verbose=2 "$root/agenthail"

mkdir -p "$work/home/.claude/skills" "$work/home/.codex"
mkdir -p "$work/user-skill"
ln -s "$work/user-skill" "$work/home/.claude/skills/agenthail-operations"
env -i HOME="$work/home" AGENTHAIL_ROOT="$root" AGENTHAIL_MAC_APP="$app/Contents/MacOS/Agenthail" PATH=/usr/bin:/bin "$payload/usr/local/bin/agenthail" version --json | jq -e '.version and .revision'
test -L "$work/home/.codex/skills/agenthail-operations"
test "$(readlink "$work/home/.codex/skills/agenthail-operations")" = "$root/skills/agenthail-operations"
test "$(readlink "$work/home/.claude/skills/agenthail-operations")" = "$work/user-skill"
PYTHONPATH="$root/pydeps" PYTHONDONTWRITEBYTECODE=1 "$root/runtime/python/bin/python3" -c 'from curl_cffi import requests; assert requests'
(cd "$root" && PATH="$root/runtime/node/bin:/usr/bin:/bin" "$root/runtime/node/bin/node" -e "import('@steipete/sweet-cookie').then(module => { if (!module.getCookies) process.exit(1) })")

linkage="$work/linkage.txt"
while IFS= read -r path; do
	if file "$path" | grep -q 'Mach-O'; then
		otool -L "$path" >> "$linkage" 2>/dev/null || true
	fi
done < <(find "$root" -type f)
if rg -n '/opt/homebrew|/usr/local/Cellar' "$linkage"; then
	echo "error: package has a Homebrew runtime linkage" >&2
	exit 1
fi

echo "package artifact verified: $pkg"
