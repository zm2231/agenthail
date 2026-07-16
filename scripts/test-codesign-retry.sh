#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
attempts="$work/attempts"
fake="$work/codesign"

cat >"$fake" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
count=0
[ ! -f "$ATTEMPTS" ] || count="$(<"$ATTEMPTS")"
count=$((count + 1))
printf '%s' "$count" >"$ATTEMPTS"
case "${MODE:-transient}" in
	terminal)
		echo "invalid signing identity" >&2
		exit 9
		;;
	alternate)
		[ "$count" -ge 2 ] || { echo "The timestamp service could not be contacted." >&2; exit 2; }
		;;
	exhaust)
		echo "The timestamp service is not available." >&2
		exit 7
		;;
	*)
		[ "$count" -ge 3 ] || { echo "The timestamp service is not available." >&2; exit 1; }
		;;
esac
SH
chmod +x "$fake"

ATTEMPTS="$attempts" CODESIGN_BIN="$fake" CODESIGN_RETRY_DELAY=0 "$ROOT/scripts/codesign-with-retry.sh" --sign fixture
test "$(<"$attempts")" = "3"
printf '0' >"$attempts"
ATTEMPTS="$attempts" MODE=alternate CODESIGN_BIN="$fake" CODESIGN_RETRY_DELAY=0 "$ROOT/scripts/codesign-with-retry.sh" --sign fixture
test "$(<"$attempts")" = "2"
printf '0' >"$attempts"
set +e
ATTEMPTS="$attempts" MODE=exhaust CODESIGN_BIN="$fake" CODESIGN_RETRY_DELAY=0 "$ROOT/scripts/codesign-with-retry.sh" --sign fixture
status=$?
set -e
test "$status" = "7"
test "$(<"$attempts")" = "4"
printf '0' >"$attempts"
if ATTEMPTS="$attempts" MODE=terminal CODESIGN_BIN="$fake" "$ROOT/scripts/codesign-with-retry.sh" --sign fixture; then
	exit 1
fi
test "$(<"$attempts")" = "1"
