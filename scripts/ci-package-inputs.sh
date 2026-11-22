#!/usr/bin/env bash
# Writes package=true|false: true unless a pull request changes nothing scripts/package-inputs.txt names.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

package=true
if [ "$EVENT" = pull_request ] && [[ "$HEAD_REF" != train/* ]]; then
	base="$BASE"
	if [ "$STACKED" = true ]; then
		base=""
		if [ -n "$STACK_BASE_REF" ]; then
			git fetch --no-tags origin "+refs/heads/$STACK_BASE_REF:refs/remotes/origin/$STACK_BASE_REF"
			base="origin/$STACK_BASE_REF"
		fi
	fi
	if [ -n "$base" ]; then
		spec=()
		while read -r line; do
			case "$line" in
			"") ;;
			!*) spec+=(":(glob,exclude)${line#!}") ;;
			*) spec+=(":(glob)$line") ;;
			esac
		done <scripts/package-inputs.txt
		from="$(git merge-base "$base" HEAD)"
		if git diff --quiet "$from" HEAD -- "${spec[@]}"; then
			package=false
		fi
	fi
fi
echo "package=$package" >>"$GITHUB_OUTPUT"
echo "package job: $package"
