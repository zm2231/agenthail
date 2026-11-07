#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
list="${UI_TEST_TIERS:-$root/native/ui-test-tiers.txt}"
sources="${UI_TEST_SOURCES:-$root/native/iOSUITests}"
all_tiers="smoke main nightly"
wide='^(native/iOSUITests/|native/ui-test-tiers\.txt$|scripts/ios-ui-tests\.sh$|scripts/ios-simulator\.sh$|\.github/workflows/ios\.yml$|native/iOS/AgenthailIOSViews\.swift$|native/iOS/(SessionPreview|Preview|Transcript|QueueTail|OlderPlace|OpeningRebuild|StreamBatch|ScrollJump|CompactActivity|IOSAgentMessageRows|Timeline)[^/]*\.swift$)'

usage() {
	cat >&2 <<EOF
usage: $0 check
       $0 args <tier>...
       $0 tiers <event> <changed-files> <labels>
EOF
	exit 2
}

entries() { grep -vE '^[[:space:]]*(#|$)' "$list" || true; }

declared() {
	awk '
		FNR == 1 { class = "" }
		/^[[:space:]]*(final[[:space:]]+)?class[[:space:]]+[A-Za-z0-9_]+[[:space:]]*:[[:space:]]*XCTestCase/ {
			sub(/^[[:space:]]*(final[[:space:]]+)?class[[:space:]]+/, ""); sub(/[[:space:]]*:.*/, ""); class = $0; next
		}
		class != "" && /^[[:space:]]*func[[:space:]]+test[A-Za-z0-9_]*[[:space:]]*\(/ {
			sub(/^[[:space:]]*func[[:space:]]+/, ""); sub(/[[:space:]]*\(.*/, ""); print class "/" $0
		}
	' "$sources"/*.swift | sort
}

check() {
	local bad=0 tier test
	while read -r tier test extra; do
		if [ -n "${extra:-}" ] || [[ ! " $all_tiers " == *" $tier "* ]] || [[ ! "$test" =~ ^[A-Za-z0-9_]+/test[A-Za-z0-9_]*$ ]]; then
			echo "malformed line: $tier $test ${extra:-}" >&2
			bad=1
		fi
	done < <(entries)
	local listed
	listed="$(entries | awk '{print $2}' | sort)"
	local dupes missing stale
	dupes="$(uniq -d <<<"$listed")"
	missing="$(comm -23 <(declared) <(uniq <<<"$listed"))"
	stale="$(comm -13 <(declared) <(uniq <<<"$listed"))"
	[ -z "$dupes" ] || { echo "listed more than once in $list:" >&2; sed 's/^/  /' <<<"$dupes" >&2; bad=1; }
	[ -z "$missing" ] || { echo "UI tests in no tier; add each to $list:" >&2; sed 's/^/  /' <<<"$missing" >&2; bad=1; }
	[ -z "$stale" ] || { echo "listed in $list but not a test:" >&2; sed 's/^/  /' <<<"$stale" >&2; bad=1; }
	[ "$bad" = 0 ] || exit 1
	echo "every UI test is in one tier ($(wc -l <<<"$listed" | tr -d ' ') tests)"
}

args() {
	[ "$#" -gt 0 ] || usage
	local wanted=" $* " tier test
	echo "-only-testing:AgenthailIOSTests"
	while read -r tier test; do
		[[ "$wanted" == *" $tier "* ]] && echo "-only-testing:AgenthailIOSUITests/$test"
	done < <(entries)
}

tiers() {
	[ "$#" -eq 3 ] || usage
	local event="$1" changed="$2" labels="$3"
	case "$event" in
		push) echo "smoke main" ;;
		schedule | workflow_dispatch) echo "$all_tiers" ;;
		pull_request)
			if grep -qx 'ui-all' "$labels"; then
				echo "$all_tiers"
			elif grep -qE "$wide" "$changed"; then
				echo "smoke main"
			else
				echo "smoke"
			fi
			;;
		*) usage ;;
	esac
}

case "${1:-}" in
	check) check ;;
	args) shift && args "$@" ;;
	tiers) shift && tiers "$@" ;;
	*) usage ;;
esac
