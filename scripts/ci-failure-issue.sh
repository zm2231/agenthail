#!/usr/bin/env bash
set -euo pipefail

result="${1:?usage: $0 <job result> <issue title> <run name>}"
title="${2:?usage: $0 <job result> <issue title> <run name>}"
name="${3:?usage: $0 <job result> <issue title> <run name>}"
run="${GITHUB_SERVER_URL:?}/${GITHUB_REPOSITORY:?}/actions/runs/${GITHUB_RUN_ID:?}"

number="$(gh issue list --state open --search "in:title \"$title\"" --json number,title |
	jq -r --arg title "$title" 'map(select(.title == $title)) | first | .number // empty')"

if [ "$result" = success ]; then
	[ -z "$number" ] || gh issue close "$number" --comment "Passing again: $run"
	exit 0
fi

body="The $name ended with \`$result\`: $run"
if [ -n "${ARTIFACT_URL:-}" ]; then
	body+=$'\n\n'"Test results: $ARTIFACT_URL"
else
	body+=$'\n\n'"No test results were saved: the run stopped before the tests wrote them. The run's log has the cause."
fi
if [ -n "${ISSUE_DETAILS:-}" ]; then
	body+=$'\n\n```\n'"$ISSUE_DETAILS"$'\n```'
fi
if [ -n "$number" ]; then
	gh issue comment "$number" --body "$body"
else
	[ -z "${ISSUE_LABEL:-}" ] || gh label create "$ISSUE_LABEL" --force --description "Opened by a scheduled CI run" >/dev/null
	gh issue create --title "$title" --body "$body" ${ISSUE_ASSIGNEE:+--assignee "$ISSUE_ASSIGNEE"} ${ISSUE_LABEL:+--label "$ISSUE_LABEL"}
fi
