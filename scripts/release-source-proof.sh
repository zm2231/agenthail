#!/usr/bin/env bash
# Usage: release-source-proof.sh <commit sha>
# Needs GH_TOKEN and GITHUB_REPOSITORY. POLL_SECONDS and POLL_LIMIT bound the
# wait for a CI run that is still going.
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

for ((attempt = 1; ; attempt++)); do
	run="$(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?head_sha=$sha&event=push&branch=main&per_page=1" --jq '.workflow_runs[0] // empty | "\(.id) \(.status) \(.conclusion) \(.html_url)"')"
	if [ -z "$run" ]; then
		outcome false "" "no CI run for a push of $sha to main"
		exit 0
	fi
	read -r id status conclusion url <<<"$run"
	[ "$status" = completed ] && break
	if [ "$attempt" -ge "$limit" ]; then
		outcome false "$url" "CI run $url at $sha was still $status after $limit checks"
		exit 0
	fi
	sleep "$poll"
done

if [ "$conclusion" != success ]; then
	outcome false "$url" "CI run $url at $sha ended $conclusion"
	exit 0
fi

jobs="$(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$id/jobs?per_page=100" --jq '.jobs[] | "\(.conclusion)\t\(.name)"')"
for name in "${REQUIRED[@]}"; do
	result="$(awk -F'\t' -v name="$name" '$2 == name {print $1; exit}' <<<"$jobs")"
	if [ "$result" != success ]; then
		outcome false "$url" "CI run $url at $sha has $name ${result:-missing}"
		exit 0
	fi
done
shards="$(awk -F'\t' -v prefix="$SHARDS" 'index($2, prefix) == 1' <<<"$jobs")"
if [ -z "$shards" ]; then
	outcome false "$url" "CI run $url at $sha has no verify-linux-daemon shard"
	exit 0
fi
unpassed="$(awk -F'\t' '$1 != "success" {print $2 " " $1}' <<<"$shards")"
if [ -n "$unpassed" ]; then
	outcome false "$url" "CI run $url at $sha has ${unpassed//$'\n'/, }"
	exit 0
fi
outcome true "$url" ""
