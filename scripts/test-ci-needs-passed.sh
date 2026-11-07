#!/usr/bin/env bash
set -euo pipefail
gate="$(cd "$(dirname "$0")" && pwd)/ci-needs-passed.sh"
check() {
	local want="$1" results="$2"
	if "$gate" "$results" >/dev/null 2>&1; then got=pass; else got=fail; fi
	[ "$got" = "$want" ] || { echo "FAIL: $results gave $got, want $want" >&2; exit 1; }
}
check pass '["success","success"]'
check fail '["success","skipped"]'
check fail '["cancelled","success"]'
check fail '["success","failure"]'
check fail '[]'
echo "ci-needs-passed tests passed"
