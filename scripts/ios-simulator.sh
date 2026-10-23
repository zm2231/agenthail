#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 [iphone|ipad] [--boot]" >&2
  echo "       $0 [iphone|ipad] run <command> [args...]" >&2
  exit 2
}

kind="${1:-iphone}"
case "$kind" in
  iphone) name="Agenthail iPhone"; family="iPhone" ;;
  ipad) name="Agenthail iPad"; family="iPad" ;;
  *) usage ;;
esac
name="${AGENTHAIL_SIMULATOR_NAME:-$name}"

devices="$(xcrun simctl list devices available --json)"
udid="$(jq -r --arg name "$name" '[.devices[][] | select(.name == $name)] | first | .udid // empty' <<<"$devices")"

if [ -z "$udid" ]; then
  runtime="$(xcrun simctl list runtimes --json | jq -r '.runtimes | map(select(.isAvailable and (.identifier | contains("iOS")))) | last | .identifier')"
  device="$(xcrun simctl list devicetypes --json | jq -r --arg family "$family" '.devicetypes | map(select(.name | startswith($family))) | first | .identifier')"
  udid="$(xcrun simctl create "$name" "$device" "$runtime")"
fi

case "${2:-}" in
  "") ;;
  --boot)
    xcrun simctl boot "$udid" 2>/dev/null || true
    xcrun simctl bootstatus "$udid" -b >/dev/null
    ;;
  run)
    [ "$#" -ge 3 ] || usage
    shift 2
    # Fixed path, not TMPDIR: every session on this machine must contend for the same lock.
    lock="/tmp/agenthail-simulator-$udid.lock"
    xcrun simctl boot "$udid" 2>/dev/null || true
    xcrun simctl bootstatus "$udid" -b >/dev/null
    AGENTHAIL_SIMULATOR_UDID="$udid" exec lockf -k "$lock" "$@"
    ;;
  *) usage ;;
esac

echo "$udid"
