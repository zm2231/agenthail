#!/usr/bin/env bash
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: $0 URL PATH SHA256" >&2; exit 2; }
url="$1" path="$2" sha="$3"
attempts="${AGENTHAIL_DOWNLOAD_ATTEMPTS:-6}"
backoff="${AGENTHAIL_DOWNLOAD_BACKOFF_SECONDS:-10}"

checksum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum <"$1" | cut -d' ' -f1
	else
		shasum -a 256 <"$1" | cut -d' ' -f1
	fi
}

[ -f "$path" ] && [ "$(checksum "$path")" = "$sha" ] && exit 0
rm -f "$path"
mkdir -p "$(dirname "$path")"
download=""
trap 'rm -f "$download"' EXIT
# An upstream outage outlasts curl's own retries, so attempts back off over about five minutes.
for attempt in $(seq "$attempts"); do
	download="$(mktemp "$path.XXXXXX")"
	status=0
	curl -fsSL --connect-timeout 20 -o "$download" "$url" || status=$?
	got="$(checksum "$download")"
	if [ "$status" -eq 0 ] && [ "$got" = "$sha" ]; then
		mv "$download" "$path"
		exit 0
	fi
	rm -f "$download"
	echo "verified-download: attempt $attempt of $attempts for $url failed (curl exit $status, got $got, want $sha)" >&2
	[ "$attempt" -lt "$attempts" ] && sleep $((backoff << (attempt - 1)))
done
echo "error: no download of $url matched the pinned checksum $sha" >&2
exit 1
