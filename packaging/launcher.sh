#!/bin/bash
set -e

AGENTHAIL_ROOT="${AGENTHAIL_ROOT:-/Library/Application Support/Agenthail}"
export AGENTHAIL_SIDECAR="${AGENTHAIL_SIDECAR:-$AGENTHAIL_ROOT/sidecar.py}"
export AGENTHAIL_COOKIE_BRIDGE="${AGENTHAIL_COOKIE_BRIDGE:-$AGENTHAIL_ROOT/cookie.mjs}"
export AGENTHAIL_PYTHON="${AGENTHAIL_PYTHON:-$AGENTHAIL_ROOT/runtime/python/bin/python3}"
export AGENTHAIL_MAC_APP="${AGENTHAIL_MAC_APP:-/Applications/Agenthail.app/Contents/MacOS/Agenthail}"
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:${PATH:-}"
export PYTHONPATH="$AGENTHAIL_ROOT/pydeps${PYTHONPATH:+:$PYTHONPATH}"
export PYTHONDONTWRITEBYTECODE=1

skill="$AGENTHAIL_ROOT/skills/agenthail-operations"
if [ -f "$skill/SKILL.md" ]; then
	for runtime in "${CLAUDE_CONFIG_DIR:-$HOME/.claude}" "${CODEX_HOME:-$HOME/.codex}" "$HOME/.hermes"; do
		[ -d "$runtime" ] || continue
		link="$runtime/skills/agenthail-operations"
		mkdir -p "$runtime/skills"
		if [ ! -e "$link" ]; then
			ln -sfn "$skill" "$link"
		fi
		# The root uninstaller cannot see this user's CLAUDE_CONFIG_DIR or CODEX_HOME, so it reads the links from here.
		if [ "$(readlink "$link" 2>/dev/null)" = "$skill" ] && ! grep -Fqx "$link" "$HOME/.agenthail/skill-links" 2>/dev/null; then
			(umask 077 && mkdir -p "$HOME/.agenthail" && printf '%s\n' "$link" >>"$HOME/.agenthail/skill-links")
		fi
	done
fi

exec "$AGENTHAIL_ROOT/Agenthail Daemon.app/Contents/MacOS/agenthail" "$@"
