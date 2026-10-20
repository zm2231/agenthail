#!/usr/bin/env bash
# Stages the Claude host beside the agenthail binary. The SDK's bundled Claude
# binaries are optional dependencies and are left out: the host runs the
# user's own claude, and the remaining dependencies are plain JavaScript, so
# one staged tree serves every platform.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${1:?usage: stage-claude-host.sh DEST}"
NPM="${NPM:-npm}"

rm -rf "$DEST"
mkdir -p "$DEST"
# npm ci rejects the lock file when the prefix runs through a symlink, as
# macOS temporary directories under /var do.
DEST="$(cd "$DEST" && pwd -P)"
cp "$ROOT/claude-host/host.mjs" "$ROOT/claude-host/package.json" "$ROOT/claude-host/package-lock.json" "$DEST/"
"$NPM" ci --omit=dev --omit=optional --ignore-scripts --no-bin-links --no-audit --no-fund --prefix "$DEST" >/dev/null
# agenthail update extracts only directories and regular files.
if find "$DEST" ! -type d ! -type f -print -quit | grep -q .; then
	echo "error: the staged Claude host contains entries that are not directories or regular files" >&2
	exit 1
fi
if find "$DEST/node_modules" -name '*.node' -print -quit | grep -q .; then
	echo "error: the Claude host gained a native dependency; it can no longer ship as one tree" >&2
	exit 1
fi
test -f "$DEST/node_modules/@anthropic-ai/claude-agent-sdk/package.json"
