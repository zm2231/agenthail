#!/usr/bin/env bash
set -euo pipefail

CODESIGN_BIN="${CODESIGN_BIN:-/usr/bin/codesign}"
retry_delay="${CODESIGN_RETRY_DELAY:-3}"
attempt=1
while :; do
	set +e
	output="$("$CODESIGN_BIN" "$@" 2>&1)"
	status=$?
	set -e
	[ -z "$output" ] || printf '%s\n' "$output" >&2
	[ "$status" -ne 0 ] || exit 0
	case "$output" in
		*"timestamp service is not available"*|*"timestamp service could not be contacted"*) ;;
		*) exit "$status" ;;
	esac
	[ "$attempt" -lt 4 ] || exit "$status"
	sleep $((attempt * retry_delay))
	attempt=$((attempt + 1))
done
