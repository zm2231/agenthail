#!/bin/bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	exec /usr/bin/sudo "$0" "$@"
fi

purge_data=0
if [ "${1:-}" = "--purge-data" ]; then
	purge_data=1
elif [ "$#" -gt 0 ]; then
	echo "usage: sudo agenthail-uninstall [--purge-data]" >&2
	exit 64
fi

while read -r user uid; do
	case "$uid" in
		''|*[!0-9]*) continue ;;
	esac
	if [ "$uid" -lt 500 ] || [ "$uid" -eq 65534 ]; then
		continue
	fi
	home="$(dscl . -read "/Users/$user" NFSHomeDirectory 2>/dev/null | awk '{print $2}')"
	[ -n "$home" ] || continue
	terminate_exact_executable() {
		expected="$1"
		for pid in $(pgrep -u "$uid" -x Agenthail 2>/dev/null || true); do
			[ "$(ps -p "$pid" -o command=)" != "$expected" ] || kill -TERM "$pid" >/dev/null 2>&1 || true
		done
	}
	if [ -x /Applications/Agenthail.app/Contents/MacOS/Agenthail ]; then
		launchctl asuser "$uid" /usr/bin/sudo -u "$user" env HOME="$home" /Applications/Agenthail.app/Contents/MacOS/Agenthail service disable >/dev/null 2>&1 || true
	fi
	if [ -x "/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail" ]; then
		launchctl asuser "$uid" /usr/bin/sudo -u "$user" env HOME="$home" "/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail" daemon uninstall >/dev/null 2>&1 || true
		launchctl asuser "$uid" /usr/bin/sudo -u "$user" env HOME="$home" "/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail" daemon stop >/dev/null 2>&1 || true
	fi
	launchctl bootout "gui/$uid/com.agenthail.daemon" >/dev/null 2>&1 || true
	terminate_exact_executable "/Applications/Agenthail.app/Contents/MacOS/Agenthail"
	plist="$home/Library/LaunchAgents/com.agenthail.daemon.plist"
	if [ -f "$plist" ] && [ "$(plutil -extract ProgramArguments.0 raw -o - "$plist" 2>/dev/null || true)" = "/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail" ]; then
		rm -f "$plist"
	fi
	links=("$home/.claude/skills/agenthail-operations" "$home/.codex/skills/agenthail-operations" "$home/.hermes/skills/agenthail-operations")
	if [ -f "$home/.agenthail/skill-links" ] && [ ! -L "$home/.agenthail/skill-links" ]; then
		while IFS= read -r link; do
			links+=("$link")
		done <"$home/.agenthail/skill-links"
	fi
	for link in "${links[@]}"; do
		if [ -L "$link" ] && [ "$(readlink "$link")" = "/Library/Application Support/Agenthail/skills/agenthail-operations" ]; then
			/usr/bin/sudo -u "$user" /bin/rm -f "$link" || true
		fi
	done
	[ -L "$home/.agenthail/skill-links" ] || rm -f "$home/.agenthail/skill-links"
	if [ "$purge_data" -eq 1 ]; then
		rm -rf "$home/.agenthail"
	fi
done < <(dscl . -list /Users UniqueID)

rm -rf "/Library/Application Support/Agenthail" /Applications/Agenthail.app
rm -f /usr/local/bin/agenthail /usr/local/bin/agenthail-uninstall
pkgutil --forget com.agenthail.pkg >/dev/null 2>&1 || true
echo "Agenthail uninstalled. User data was $([ "$purge_data" -eq 1 ] && echo removed || echo preserved)."
