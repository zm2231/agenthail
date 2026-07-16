#!/usr/bin/env bash
set -euo pipefail

STATE="$HOME/.fake-agenthail-launchd-loaded"
PIDFILE="$HOME/.agenthail/daemon.pid"

case "${1:-}" in
	print)
		case "${2:-}" in
			*com.agenthail.daemon*) test -f "$STATE" ;;
			*) exit 1 ;;
		esac
		;;
	bootout)
		case "${*: -1}" in
			*com.agenthail.daemon*) ;;
			*) exit 0 ;;
		esac
		if [ -f "$STATE" ] && [ -f "$PIDFILE" ]; then
			pid="$(cat "$PIDFILE")"
			kill -TERM "$pid" >/dev/null 2>&1 || true
			for _ in $(seq 1 100); do
				kill -0 "$pid" >/dev/null 2>&1 || break
				sleep 0.05
			done
		fi
		rm -f "$STATE"
		;;
	bootstrap)
		plist="${3:?missing plist path}"
		program="$(plutil -extract ProgramArguments.0 raw -o - "$plist")"
		mkdir -p "$HOME/.agenthail"
		"$program" daemon-run >>"$HOME/.agenthail/fake-launchd.log" 2>&1 &
		echo "$!" >"$HOME/.fake-agenthail-launchd-last-pid"
		touch "$STATE"
		;;
	kickstart)
		;;
	*)
		exit 1
		;;
esac
