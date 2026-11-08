#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"
OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

usage() {
	echo "usage: $0 find <commit> | require <proven> <suite-result>" >&2
	exit 2
}

say() {
	printf '%s\n' "$1"
	printf '%s\n' "$1" >>"$SUMMARY"
}

outcome() {
	local proven="$1" commit="$2" run="$3" reason="$4"
	{
		echo "proven=$proven"
		echo "commit=$commit"
		echo "run=$run"
		echo "reason=$reason"
	} >>"$OUTPUT"
}

find_proof() {
	local sha proof id url conclusion
	local -a inputs
	sha="$(git -C "$ROOT" rev-parse --verify "${1:?}^{commit}")"
	mapfile -t inputs < <("$ROOT/scripts/ios-inputs.sh" list)
	proof="$(git -C "$ROOT" log -1 --first-parent --format=%H "$sha" -- "${inputs[@]}")"
	if [ -z "$proof" ]; then
		outcome false "" "" "no commit at or before $sha changed an iOS input"
		say "iOS proof: no commit at or before $sha changed an iOS input; the release runs the iOS suite."
		return
	fi
	if ! git -C "$ROOT" diff --quiet "$proof" "$sha" -- "${inputs[@]}"; then
		outcome false "$proof" "" "iOS inputs differ between $proof and $sha"
		say "iOS proof: iOS inputs differ between $proof and $sha; the release runs the iOS suite."
		return
	fi
	while read -r id url; do
		[ -n "$id" ] || continue
		conclusion="$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$id/jobs?per_page=100" --jq '[.jobs[] | select(.name == "ios") | .conclusion] | first // ""')"
		if [ "$conclusion" = success ]; then
			outcome true "$proof" "$url" ""
			say "iOS proof: $sha has the same iOS inputs as $proof, the last commit to change one, and iOS run $url passed its ios job there."
			return
		fi
	done < <(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/ios.yml/runs?head_sha=$proof&event=push&per_page=100" --jq '.workflow_runs[] | "\(.id) \(.html_url)"')
	outcome false "$proof" "" "no iOS run for a push of $proof passed its ios job"
	say "iOS proof: no iOS run for a push of $proof, the last commit to change an iOS input, passed its ios job; the release runs the iOS suite."
}

require_proof() {
	local proven="${1:-}" suite="${2:-}" sha="${GITHUB_SHA:?}"
	local run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:?}/actions/runs/${GITHUB_RUN_ID:?}"
	if [ "$proven" = true ]; then
		say "iOS: proven for $sha by run ${PROOF_RUN:?} at ${PROOF_COMMIT:?}."
		return
	fi
	if [ "$suite" = success ]; then
		say "iOS: this release ran the iOS suite at $sha and it passed ($run_url)."
		return
	fi
	{
		echo "### Release left as a draft"
		echo
		echo "No passing iOS run covers $sha: ${PROOF_REASON:-no proof was found}. The iOS suite this release ran ended \`${suite:-not run}\` ($run_url)."
		echo
		echo "The draft has every asset; nothing went to TestFlight or the push relay. Fix iOS on main and release again, or rerun this workflow's failed jobs."
	} >>"$SUMMARY"
	echo "::error::Release left as a draft: no passing iOS run covers $sha (${PROOF_REASON:-no proof was found}); the iOS suite this release ran ended '${suite:-not run}'."
	exit 1
}

case "${1:-}" in
	find) shift && find_proof "$@" ;;
	require) shift && require_proof "$@" ;;
	*) usage ;;
esac
