#!/usr/bin/env bash
# No optional or native dependencies, so one staged tree serves every platform.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NAME="${1:?usage: stage-node-host.sh claude-host|browser-host DEST}"
DEST="${2:?usage: stage-node-host.sh claude-host|browser-host DEST}"
NPM="${NPM:-npm}"
SOURCE="$ROOT/$NAME"
test -f "$SOURCE/package.json"

rm -rf "$DEST"
mkdir -p "$DEST"
# npm ci rejects the lock file when the prefix runs through a symlink, as
# macOS temporary directories under /var do.
DEST="$(cd "$DEST" && pwd -P)"
cp "$SOURCE"/*.mjs "$SOURCE/package.json" "$SOURCE/package-lock.json" "$DEST/"
for module in "$DEST"/*.mjs; do
	for dependency in $({ grep -oE "(from|import\() *['\"]\./[^'\"]+['\"]" "$module" || true; } | sed -E "s/.*['\"](\.\/[^'\"]+)['\"]/\1/"); do
		if [ ! -f "$DEST/$dependency" ]; then
			echo "error: $(basename "$module") imports $dependency, which is not staged" >&2
			exit 1
		fi
	done
done
"$NPM" ci --omit=dev --omit=optional --ignore-scripts --no-bin-links --no-audit --no-fund --prefix "$DEST" >/dev/null
# agenthail update extracts only directories and regular files.
if find "$DEST" ! -type d ! -type f -print -quit | grep -q .; then
	echo "error: the staged $NAME contains entries that are not directories or regular files" >&2
	exit 1
fi
if find "$DEST/node_modules" -name '*.node' -print -quit | grep -q .; then
	echo "error: $NAME gained a native dependency; it can no longer ship as one tree" >&2
	exit 1
fi
"$NPM" ls --omit=dev --omit=optional --prefix "$DEST" >/dev/null
