#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

usage() {
	echo "usage: $0 list | touched <changed-paths-file>" >&2
	exit 2
}

list() {
	local sources
	sources="$(yq '.targets[].sources[].path' "$ROOT/native/project.yml")"
	[ -n "$sources" ] || { echo "no iOS sources in native/project.yml" >&2; exit 1; }
	printf '%s\n' \
		native/project.yml \
		native/Agenthail.xcodeproj \
		native/ui-test-tiers.txt \
		scripts/ios-inputs.sh \
		scripts/ios-simulator.sh \
		scripts/ios-test-stall-guard.sh \
		scripts/ios-ui-tests.sh \
		scripts/test-xcodegen-project.sh \
		scripts/release-ios-proof.sh \
		.github/workflows/ios.yml
	while IFS= read -r source; do printf 'native/%s\n' "$source"; done <<<"$sources" | sort -u
}

touched() {
	local changed="${1:?}" file input
	local -a inputs
	mapfile -t inputs < <(list)
	while IFS= read -r file; do
		for input in "${inputs[@]}"; do
			if [ "$file" = "$input" ] || [[ "$file" == "$input"/* ]]; then
				echo true
				return
			fi
		done
	done <"$changed"
	echo false
}

case "${1:-}" in
	list) list ;;
	touched) shift && touched "$@" ;;
	*) usage ;;
esac
