#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir "$work/bin" "$work/dist"
cat >"$work/bin/xcrun" <<'SH'
#!/usr/bin/env bash
echo "xcrun $*" >>"$CALLS"
case "$1 $2" in
"notarytool submit")
	echo "Conducting pre-submission checks"
	echo '{"id":"sub-1","message":"Successfully uploaded file"}'
	;;
"notarytool wait")
	[ "$3" = sub-1 ] || exit 3
	echo "Current status: In Progress..."
	echo "{\"id\":\"sub-1\",\"status\":\"$STATUS\"}"
	[ "$STATUS" = Accepted ]
	;;
"notarytool log") echo "notary log for $3" ;;
"stapler staple") echo stapled >>"$3" ;;
"stapler validate") ;;
*) exit 3 ;;
esac
SH
printf '#!/usr/bin/env bash\necho "spctl $*" >>"$CALLS"\n' >"$work/bin/spctl"
chmod +x "$work/bin/xcrun" "$work/bin/spctl"

pkg="$work/dist/Agenthail-v1.0.0-arm64.pkg"
run() {
	PATH="$work/bin:$PATH" CALLS="$work/calls" AGENTHAIL_NOTARY_PROFILE=profile "$ROOT/scripts/notarize-pkg.sh" "$@"
}

echo package >"$pkg"
run submit "$pkg" >/dev/null
[ "$(cat "$pkg.notary-submission")" = sub-1 ] || fail "submit did not record the submission"
STATUS=Accepted run finish "$pkg" >/dev/null
grep -q stapled "$pkg" || fail "finish did not staple the package"
grep -q '^spctl --assess --type install' "$work/calls" || fail "finish did not assess the package"
[ ! -e "$pkg.notary-submission" ] || fail "finish left the submission record"
(cd "$work/dist" && shasum -a 256 -c "$(basename "$pkg").sha256" >/dev/null) || fail "the checksum is not of the stapled package"

echo package >"$pkg"
rm -f "$pkg.sha256" "$work/calls"
run submit "$pkg" >/dev/null
if STATUS=Invalid run finish "$pkg" >/dev/null 2>"$work/err"; then
	fail "an invalid submission finished"
fi
grep -q "status 'Invalid'" "$work/err" || fail "no status in: $(cat "$work/err")"
grep -q "notary log for sub-1" "$work/err" || fail "the notary log was not printed"
! grep -q stapled "$pkg" || fail "an invalid submission was stapled"
[ ! -e "$pkg.sha256" ] || fail "an invalid submission got a checksum"

if run finish "$work/dist/missing.pkg" 2>/dev/null; then
	fail "a missing package was accepted"
fi

echo "notarize-pkg tests passed"
