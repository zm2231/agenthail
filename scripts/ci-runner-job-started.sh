#!/usr/bin/env bash
set -euo pipefail

event="${GITHUB_EVENT_PATH:?}"
status=0
jq -e 'has("pull_request")' "$event" >/dev/null || status=$?
case "$status" in
	0) ;;
	1) exit 0 ;;
	*)
		echo "This runner could not read the job's event $event (jq exit $status), so it cannot tell where the job came from." >&2
		exit 1
		;;
esac
head="$(jq -r '.pull_request.head.repo.full_name // "a deleted fork"' "$event")"
if [ "$head" != "${GITHUB_REPOSITORY:?}" ]; then
	echo "This runner only runs pull requests from $GITHUB_REPOSITORY, not $head." >&2
	exit 1
fi
