#!/usr/bin/env bash
# Decides whether a run includes the full-CI jobs: on a push to main, on a
# merge-train branch, or when the pull request carries the full-ci label. The
# labels are read now rather than from the event, because `gh pr create --label`
# opens the pull request before it adds the labels, so the opened event has none.
# Needs GH_TOKEN, GITHUB_REPOSITORY, GITHUB_OUTPUT, GITHUB_EVENT_NAME, HEAD_REF
# and PR_NUMBER.
set -euo pipefail

full=false
if [[ "$GITHUB_EVENT_NAME" == push || "$HEAD_REF" == train/* ]]; then
	full=true
else
	labels="$(gh api "repos/$GITHUB_REPOSITORY/pulls/$PR_NUMBER" --jq '.labels[].name')"
	if grep -qx 'full-ci' <<<"$labels"; then
		full=true
	fi
fi
echo "full=$full" >>"$GITHUB_OUTPUT"
echo "full CI: $full"
