#!/bin/bash
set -e

AGENTHAIL_ROOT="${AGENTHAIL_ROOT:-/Library/Application Support/Agenthail}"
export AGENTHAIL_SIDECAR="${AGENTHAIL_SIDECAR:-$AGENTHAIL_ROOT/sidecar.py}"
export AGENTHAIL_COOKIE_BRIDGE="${AGENTHAIL_COOKIE_BRIDGE:-$AGENTHAIL_ROOT/cookie.mjs}"
export AGENTHAIL_PYTHON="${AGENTHAIL_PYTHON:-$AGENTHAIL_ROOT/runtime/python/bin/python3}"
export AGENTHAIL_MAC_APP="${AGENTHAIL_MAC_APP:-/Applications/Agenthail.app/Contents/MacOS/Agenthail}"
export PATH="$AGENTHAIL_ROOT/runtime/node/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:${PATH:-}"
export PYTHONPATH="$AGENTHAIL_ROOT/pydeps${PYTHONPATH:+:$PYTHONPATH}"
export PYTHONDONTWRITEBYTECODE=1

skill="$AGENTHAIL_ROOT/skills/agenthail-operations"
if [ -f "$skill/SKILL.md" ]; then
	for runtime in "$HOME/.claude" "$HOME/.codex" "$HOME/.hermes"; do
		[ -d "$runtime" ] || continue
		link="$runtime/skills/agenthail-operations"
		mkdir -p "$runtime/skills"
		if [ -L "$link" ]; then
			if [ "$(readlink "$link")" = "$skill" ]; then
				ln -sfn "$skill" "$link"
			fi
		elif [ ! -e "$link" ]; then
			ln -s "$skill" "$link"
		fi
	done
fi

exec "$AGENTHAIL_ROOT/agenthail" "$@"
