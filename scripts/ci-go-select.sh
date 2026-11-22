#!/usr/bin/env bash
# Decides which Linux Go test jobs a run needs, from scripts/go-test-select, and writes
# daemon, surfaces, packages and skippable to GITHUB_OUTPUT. A push, a merge-train branch
# and a stack whose base is unknown select everything.
# Needs EVENT, HEAD_REF, BASE, STACKED and STACK_BASE_REF.
set -euo pipefail

if [ "$EVENT" != pull_request ] || [[ "$HEAD_REF" == train/* ]]; then
	args=(-all)
elif [ "$STACKED" = true ]; then
	if [ -z "$STACK_BASE_REF" ]; then
		args=(-all)
	else
		git fetch --no-tags origin "+refs/heads/$STACK_BASE_REF:refs/remotes/origin/$STACK_BASE_REF"
		args=(-base "origin/$STACK_BASE_REF")
	fi
else
	args=(-base "$BASE")
fi
module="$(go list -m)"
selected="$(go run ./scripts/go-test-select "${args[@]}")"
daemon=false surfaces=false packages=()
while read -r line; do
	case "$line" in
	"") ;;
	"$module/internal/daemon") daemon=true ;;
	"$module/internal/surface/claudesurface" | "$module/internal/surface/codexsurface" | suite:node-hosts) surfaces=true ;;
	suite:*) ;;
	*) packages+=("$line") ;;
	esac
done <<<"$selected"
skippable=()
[ "$daemon" = true ] || skippable+=('"verify-linux-daemon"')
[ "$surfaces" = true ] || skippable+=('"verify-linux-surfaces"')
echo "internal/daemon: $daemon; surfaces and Node hosts: $surfaces; other packages: ${#packages[@]}"
{
	echo "daemon=$daemon"
	echo "surfaces=$surfaces"
	echo "packages=${packages[*]+"${packages[*]}"}"
	echo "skippable=[$(IFS=,; echo "${skippable[*]+"${skippable[*]}"}")]"
} >>"$GITHUB_OUTPUT"
