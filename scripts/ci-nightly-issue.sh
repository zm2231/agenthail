#!/usr/bin/env bash
set -euo pipefail

result="${1:?usage: $0 <job result>}"
title="${NIGHTLY_ISSUE_TITLE:-Nightly iPad UI tests are failing}"
run="${GITHUB_SERVER_URL:?}/${GITHUB_REPOSITORY:?}/actions/runs/${GITHUB_RUN_ID:?}"

number="$(gh issue list --state open --search "in:title \"$title\"" --json number,title |
	jq -r --arg title "$title" 'map(select(.title == $title)) | first | .number // empty')"

if [ "$result" = success ]; then
	[ -z "$number" ] || gh issue close "$number" --comment "Passing again: $run"
	exit 0
fi

body="The nightly iPad UI test run ended with \`$result\`: $run"
if [ -n "${ARTIFACT_URL:-}" ]; then
	body+=$'\n\n'"Test results: $ARTIFACT_URL"
else
	body+=$'\n\n'"No test results were saved: the run stopped before the tests wrote them. The run's log has the cause."
fi
if [ -n "$number" ]; then
	gh issue comment "$number" --body "$body"
else
	gh issue create --title "$title" --body "$body"
fi
