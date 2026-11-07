#!/usr/bin/env bash
set -euo pipefail

app="${1:?usage: $0 APP_BUNDLE}"
plist="$app/Info.plist"
test -f "$plist"
if plutil -extract NSAppTransportSecurity xml1 -o /dev/null "$plist" 2>/dev/null; then
  echo "$plist has NSAppTransportSecurity; only Debug builds may allow loopback connections" >&2
  exit 1
fi
