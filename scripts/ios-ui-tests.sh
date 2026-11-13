#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
list="${UI_TEST_TIERS:-$root/native/ui-test-tiers.txt}"
sources="${UI_TEST_SOURCES:-$root/native/iOSUITests}"
all_tiers="smoke main nightly"
known_tiers="$all_tiers live"
wide='^(native/iOSUITests/|native/ui-test-tiers\.txt$|scripts/ios-ui-tests\.sh$|scripts/ios-simulator\.sh$|\.github/workflows/ios\.yml$|native/iOS/AgenthailIOSViews\.swift$|native/iOS/(SessionPreview|Preview|Transcript|QueueTail|OlderPlace|OpeningRebuild|StreamBatch|ScrollJump|CompactActivity|IOSAgentMessageRows|Timeline)[^/]*\.swift$)'

usage() {
	cat >&2 <<EOF
usage: $0 check
       $0 args all|parallel|alone <tier>...
       $0 tiers <event> <changed-files> <labels>
EOF
	exit 2
}

entries() { grep -vE '^[[:space:]]*(#|$)' "$list" || true; }

declared() {
	awk '
		FNR == 1 { class = "" }
		/^((@[A-Za-z]+|public|private|fileprivate|internal|final|open)[[:space:]]+)*(class|struct|enum|actor|extension|protocol)[[:space:]]/ { class = "" }
		/^((@[A-Za-z]+|public|private|fileprivate|internal|open)[[:space:]]+)*(final[[:space:]]+)?class[[:space:]]+[A-Za-z0-9_]+[[:space:]]*:[[:space:]]*XCTestCase/ {
			sub(/^.*class[[:space:]]+/, ""); sub(/[[:space:]]*:.*/, ""); class = $0; next
		}
		class != "" && /^[[:space:]]*func[[:space:]]+test[A-Za-z0-9_]*[[:space:]]*\(/ {
			sub(/^[[:space:]]*func[[:space:]]+/, ""); sub(/[[:space:]]*\(.*/, ""); print class "/" $0
		}
	' "$sources"/*.swift | sort
}

check() {
	local bad=0 tier test flag extra
	while read -r tier test flag extra; do
		if [ -n "${extra:-}" ] || { [ -n "${flag:-}" ] && [ "$flag" != alone ]; } || [[ ! " $known_tiers " == *" $tier "* ]] || [[ ! "$test" =~ ^[A-Za-z0-9_]+/test[A-Za-z0-9_]*$ ]]; then
			echo "malformed line: $tier $test ${flag:-} ${extra:-}" >&2
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
	[ "$#" -gt 1 ] || usage
	local set="$1" tier test flag
	shift
	local wanted=" $* "
	case "$set" in
		all | parallel) echo "-only-testing:AgenthailIOSTests" ;;
		alone) ;;
		*) usage ;;
	esac
	while read -r tier test flag; do
		[[ "$wanted" == *" $tier "* ]] || continue
		case "$set:${flag:-}" in
			all:* | parallel: | alone:alone) echo "-only-testing:AgenthailIOSUITests/$test" ;;
		esac
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
