#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

WORKTREE_DIRTY=0
if [ -n "$(git status --porcelain)" ]; then
	WORKTREE_DIRTY=1
fi
if [ "$WORKTREE_DIRTY" -eq 1 ] && [ "${AGENTHAIL_ALLOW_DIRTY:-0}" != "1" ]; then
	echo "error: release builds require a clean worktree (set AGENTHAIL_ALLOW_DIRTY=1 for a local test artifact)" >&2
	exit 1
fi

GOOS_VALUE="${GOOS:-darwin}"
GOARCH_VALUE="${GOARCH:-arm64}"
REVISION="$(git rev-parse HEAD)"
VERSION="${AGENTHAIL_VERSION:-$(git describe --tags --always)}"
if [ "$WORKTREE_DIRTY" -eq 1 ]; then
	VERSION="$VERSION-dirty"
fi
COMMIT_EPOCH="$(git show -s --format=%ct HEAD)"
BUILT_AT="$(date -u -r "$COMMIT_EPOCH" '+%Y-%m-%dT%H:%M:%SZ')"
STAMP="$(date -u -r "$COMMIT_EPOCH" '+%Y%m%d%H%M.%S')"
NAME="agenthail-${VERSION}-${GOOS_VALUE}-${GOARCH_VALUE}"
DIST="$ROOT/dist"
STAGE="$DIST/.release-stage/$NAME"

rm -rf "$DIST/.release-stage"
mkdir -p "$STAGE/sidecar"

LDFLAGS="-s -w -X main.version=$VERSION -X main.revision=$REVISION -X main.builtAt=$BUILT_AT"
CGO_ENABLED=0 GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" go build -trimpath -ldflags "$LDFLAGS" -o "$STAGE/agenthail" ./cmd/agenthail
if [ "$GOOS_VALUE" = "darwin" ] && ! command -v codesign >/dev/null 2>&1; then
	echo "error: macOS release packaging requires codesign" >&2
	exit 1
fi
if [ "$GOOS_VALUE" = "darwin" ]; then
	if [ "${AGENTHAIL_CODESIGN_IDENTITY+x}" = "x" ]; then
		CODESIGN_IDENTITY="$AGENTHAIL_CODESIGN_IDENTITY"
	else
		CODESIGN_IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application:/ {print $2; exit}')"
		CODESIGN_IDENTITY="${CODESIGN_IDENTITY:--}"
	fi
	if [ "$WORKTREE_DIRTY" -eq 0 ] && [ "$CODESIGN_IDENTITY" = "-" ] && [ "${AGENTHAIL_ALLOW_UNNOTARIZED:-0}" != "1" ]; then
		echo "error: production macOS releases require a Developer ID Application identity" >&2
		exit 1
	fi
	if [ "$WORKTREE_DIRTY" -eq 0 ] && [ -z "${AGENTHAIL_NOTARY_PROFILE:-}" ] && [ "${AGENTHAIL_ALLOW_UNNOTARIZED:-0}" != "1" ]; then
		echo "error: production macOS releases require AGENTHAIL_NOTARY_PROFILE" >&2
		exit 1
	fi
	codesign --force --options runtime --sign "$CODESIGN_IDENTITY" "$STAGE/agenthail"
	codesign --verify --strict --verbose=2 "$STAGE/agenthail"
	AGENTHAIL_CLI_SOURCE="$STAGE/agenthail" AGENTHAIL_CODESIGN_IDENTITY="$CODESIGN_IDENTITY" AGENTHAIL_APP_VERSION="$VERSION" AGENTHAIL_APP_BUILD="$(git rev-list --count HEAD)" "$ROOT/scripts/build-macos-app.sh" "$STAGE/Agenthail.app" "$GOARCH_VALUE" >/dev/null
fi

cp README.md LICENSE COMMERCIAL.md install.sh "$STAGE/"
cp sidecar/sidecar.py sidecar/cookie.mjs sidecar/package.json sidecar/package-lock.json "$STAGE/sidecar/"
cp -R skills "$STAGE/"
find "$STAGE/skills" -name .DS_Store -delete
test -f "$STAGE/skills/agenthail-operations/SKILL.md"
test -f "$STAGE/skills/agenthail-operations/agents/openai.yaml"

if [ "$GOOS_VALUE" = "darwin" ] && [ -n "${AGENTHAIL_NOTARY_PROFILE:-}" ]; then
	NOTARY_ARCHIVE="$DIST/.release-stage/$NAME-notary.zip"
	ditto -c -k --keepParent "$STAGE" "$NOTARY_ARCHIVE"
	echo "Submitting release archive for notarization"
	xcrun notarytool submit "$NOTARY_ARCHIVE" --keychain-profile "$AGENTHAIL_NOTARY_PROFILE" --no-s3-acceleration --wait --timeout "${AGENTHAIL_NOTARY_TIMEOUT:-20m}"
	xcrun stapler staple "$STAGE/Agenthail.app"
	rm -f "$NOTARY_ARCHIVE"
fi
find "$DIST/.release-stage" -exec touch -t "$STAMP" {} +

ARCHIVE="$DIST/$NAME.tar.gz"
COPYFILE_DISABLE=1 tar -C "$DIST/.release-stage" -cf - "$NAME" | gzip -n > "$ARCHIVE"
(cd "$DIST" && shasum -a 256 "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256")

if [ "$(go env GOHOSTOS)" = "$GOOS_VALUE" ] && [ "$(go env GOHOSTARCH)" = "$GOARCH_VALUE" ]; then
	"$STAGE/agenthail" version --json
else
	go version -m "$STAGE/agenthail"
fi
echo "archive: $ARCHIVE"
echo "checksum: $ARCHIVE.sha256"
