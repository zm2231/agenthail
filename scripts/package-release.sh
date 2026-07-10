#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ -n "$(git status --porcelain)" ] && [ "${AGENTHAIL_ALLOW_DIRTY:-0}" != "1" ]; then
	echo "error: release builds require a clean worktree (set AGENTHAIL_ALLOW_DIRTY=1 for a local test artifact)" >&2
	exit 1
fi

GOOS_VALUE="${GOOS:-darwin}"
GOARCH_VALUE="${GOARCH:-arm64}"
REVISION="$(git rev-parse HEAD)"
VERSION="${AGENTHAIL_VERSION:-$(git describe --tags --always)}"
if [ -n "$(git status --porcelain)" ]; then
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
if [ "$GOOS_VALUE" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
	codesign --force --sign "${AGENTHAIL_CODESIGN_IDENTITY:--}" "$STAGE/agenthail"
fi

cp README.md install.sh "$STAGE/"
cp sidecar/sidecar.py sidecar/cookie.mjs sidecar/package.json sidecar/package-lock.json "$STAGE/sidecar/"
cp -R skills "$STAGE/"
test -f "$STAGE/skills/agenthail-operations/SKILL.md"
test -f "$STAGE/skills/agenthail-operations/agents/openai.yaml"
find "$DIST/.release-stage" -exec touch -t "$STAMP" {} +

ARCHIVE="$DIST/$NAME.tar.gz"
COPYFILE_DISABLE=1 tar -C "$DIST/.release-stage" -cf - "$NAME" | gzip -n > "$ARCHIVE"
(cd "$DIST" && shasum -a 256 "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256")

"$STAGE/agenthail" version --json
echo "archive: $ARCHIVE"
echo "checksum: $ARCHIVE.sha256"
