#!/usr/bin/env bash
# Usage: release-source-proof.sh <commit sha>
# Needs GH_TOKEN and GITHUB_REPOSITORY. POLL_SECONDS and POLL_LIMIT bound the
# wait for the required jobs of a CI run that is still going.
set -euo pipefail

SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"
OUTPUT="${GITHUB_OUTPUT:-/dev/null}"
REQUIRED=(verify verify-linux-rest verify-linux)
SHARDS='verify-linux-daemon ('

say() {
	printf '%s\n' "$1"
	printf '%s\n' "$1" >>"$SUMMARY"
}

outcome() {
	{
		echo "proven=$1"
		echo "run=$2"
		echo "reason=$3"
	} >>"$OUTPUT"
	if [ "$1" = true ]; then
		say "Source proof: CI run $2 passed every required job at $sha; the release skips its own source suites."
	else
		say "Source proof: $3; the release runs its own source suites."
	fi
}

sha="${1:?usage: $0 <commit sha>}"
poll="${POLL_SECONDS:-30}" limit="${POLL_LIMIT:-60}"

judged() {
	awk -F'\t' -v prefix="$SHARDS" -v required="${REQUIRED[*]}" '
		BEGIN { n = split(required, names, " "); for (i = 1; i <= n; i++) want[names[i]] = 1 }
		want[$3] || index($3, prefix) == 1' <<<"$1"
}

report() {
	say "Required jobs in CI run $url:"
	while IFS=$'\t' read -r state result name; do
		if [ -n "$name" ]; then
			say "- $name: ${result:-$state}"
		fi
	done <<<"$(judged "$jobs")"
}

for ((attempt = 1; ; attempt++)); do
	run="$(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?head_sha=$sha&event=push&branch=main&per_page=1" --jq '.workflow_runs[0] // empty | "\(.id) \(.status) \(.html_url)"')"
	if [ -z "$run" ]; then
		outcome false "" "no CI run for a push of $sha to main"
		exit 0
	fi
	read -r id status url <<<"$run"
	jobs="$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$id/jobs?per_page=100" --jq '.jobs[] | "\(.status)\t\(.conclusion // "")\t\(.name)"')"
	unpassed="$(judged "$jobs" | awk -F'\t' '$1 == "completed" && $2 != "success" {print $3 " " $2}')"
	if [ -n "$unpassed" ]; then
		report
		outcome false "$url" "CI run $url at $sha has ${unpassed//$'\n'/, }"
		exit 0
	fi
	pending=""
	for name in "${REQUIRED[@]}"; do
		state="$(awk -F'\t' -v name="$name" '$3 == name {print $1; exit}' <<<"$jobs")"
		if [ "$state" != completed ]; then
			pending="$name ${state:-missing}"
			break
		fi
	done
	if [ -z "$pending" ] || [ "$status" = completed ]; then
		break
	fi
	if [ "$attempt" -ge "$limit" ]; then
		report
		outcome false "$url" "CI run $url at $sha was still running $pending after $limit checks"
		exit 0
	fi
	sleep "$poll"
done

report
if [ -n "$pending" ]; then
	outcome false "$url" "CI run $url at $sha has $pending"
	exit 0
fi
if [ -z "$(judged "$jobs" | awk -F'\t' -v prefix="$SHARDS" 'index($3, prefix) == 1')" ]; then
	outcome false "$url" "CI run $url at $sha has no verify-linux-daemon shard"
	exit 0
fi
outcome true "$url" ""
