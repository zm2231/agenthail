#!/usr/bin/env bash
set -euo pipefail

app="${1:?usage: scripts/test-macos-app-resources.sh <Agenthail.app>}"
binary="$app/Contents/MacOS/Agenthail"
resources="$app/Contents/Resources"
strings="$(strings -a "$binary")"
status=0

# A resource accessor that falls back to an absolute build path cannot find its bundle on another Mac.
while IFS= read -r path; do
	echo "error: $binary loads a package resource bundle from its build directory: $path" >&2
	status=1
done < <(grep -E '^/.+\.bundle$' <<<"$strings" || true)
if grep -q '^could not load resource bundle: from ' <<<"$strings"; then
	echo "error: $binary uses resource accessors that ignore Contents/Resources" >&2
	status=1
fi

while IFS= read -r name; do
	if [ ! -d "$resources/$name.bundle" ]; then
		echo "error: $app has no $name.bundle in Contents/Resources" >&2
		status=1
	fi
done < <(sed -n 's/^unable to find bundle named //p' <<<"$strings")

exit "$status"
