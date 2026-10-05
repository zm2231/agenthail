#!/usr/bin/env bash
set -euo pipefail

kind="${1:-iphone}"
case "$kind" in
  iphone) name="Agenthail iPhone"; family="iPhone" ;;
  ipad) name="Agenthail iPad"; family="iPad" ;;
  *) echo "usage: $0 [iphone|ipad] [--boot]" >&2; exit 2 ;;
esac

devices="$(xcrun simctl list devices available --json)"
udid="$(jq -r --arg name "$name" '[.devices[][] | select(.name == $name)] | first | .udid // empty' <<<"$devices")"

if [ -z "$udid" ]; then
  runtime="$(xcrun simctl list runtimes --json | jq -r '.runtimes | map(select(.isAvailable and (.identifier | contains("iOS")))) | last | .identifier')"
  device="$(xcrun simctl list devicetypes --json | jq -r --arg family "$family" '.devicetypes | map(select(.name | startswith($family))) | first | .identifier')"
  udid="$(xcrun simctl create "$name" "$device" "$runtime")"
fi

if [ "${2:-}" = "--boot" ]; then
  xcrun simctl boot "$udid" 2>/dev/null || true
  xcrun simctl bootstatus "$udid" -b >/dev/null
fi

echo "$udid"
