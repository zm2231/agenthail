#!/usr/bin/env bash
set -euo pipefail

event="${GITHUB_EVENT_PATH:?}"
if jq -e 'has("pull_request")' "$event" >/dev/null; then
	head="$(jq -r '.pull_request.head.repo.full_name // "a deleted fork"' "$event")"
	if [ "$head" != "${GITHUB_REPOSITORY:?}" ]; then
		echo "This runner only runs pull requests from $GITHUB_REPOSITORY, not $head." >&2
		exit 1
	fi
fi
