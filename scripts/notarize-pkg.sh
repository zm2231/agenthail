#!/usr/bin/env bash
# Usage: notarize-pkg.sh submit|finish <Agenthail.pkg>
# submit sends the package to the notary service under AGENTHAIL_NOTARY_PROFILE and records the
# submission beside it. finish waits for that submission, staples and assesses the package, and
# writes its checksum, since stapling changes the file.
set -euo pipefail

action="${1:-}"
pkg="${2:-}"
if { [ "$action" != submit ] && [ "$action" != finish ]; } || [ ! -f "$pkg" ]; then
	echo "usage: $0 submit|finish <Agenthail.pkg>" >&2
	exit 2
fi
profile="${AGENTHAIL_NOTARY_PROFILE:?AGENTHAIL_NOTARY_PROFILE is required}"
submission="$pkg.notary-submission"

json_field() {
	jq -Rrs --arg field "$1" 'capture("(?s)(?<json>\\{.*\\})").json | fromjson | .[$field] // empty'
}

if [ "$action" = submit ]; then
	echo "Submitting $(basename "$pkg") for notarization"
	id="$(xcrun notarytool submit "$pkg" --keychain-profile "$profile" --no-s3-acceleration --output-format json | json_field id)"
	[ -n "$id" ] || { echo "error: notarytool returned no submission id for $(basename "$pkg")" >&2; exit 1; }
	echo "$id" >"$submission"
	echo "submission: $id"
	exit 0
fi

id="$(cat "$submission")"
status="$(xcrun notarytool wait "$id" --keychain-profile "$profile" --timeout "${AGENTHAIL_NOTARY_TIMEOUT:-20m}" --output-format json | json_field status)" || true
if [ "$status" != Accepted ]; then
	echo "error: notarization $id of $(basename "$pkg") ended with status '${status:-unknown}'" >&2
	xcrun notarytool log "$id" --keychain-profile "$profile" >&2 || true
	exit 1
fi
xcrun stapler staple "$pkg"
xcrun stapler validate "$pkg"
spctl --assess --type install --verbose=2 "$pkg"
rm -f "$submission"
(cd "$(dirname "$pkg")" && shasum -a 256 "$(basename "$pkg")" >"$(basename "$pkg").sha256")
