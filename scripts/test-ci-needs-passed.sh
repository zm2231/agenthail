#!/usr/bin/env bash
set -euo pipefail
gate="$(cd "$(dirname "$0")" && pwd)/ci-needs-passed.sh"
check() {
	local want="$1" needs="$2" skip="${3:-[]}"
	if "$gate" "$needs" "$skip" >/dev/null 2>&1; then got=pass; else got=fail; fi
	[ "$got" = "$want" ] || { echo "FAIL: $needs skipping $skip gave $got, want $want" >&2; exit 1; }
}
check pass '{"a":{"result":"success"},"b":{"result":"success"}}'
check fail '{"a":{"result":"success"},"b":{"result":"skipped"}}'
check pass '{"a":{"result":"success"},"b":{"result":"skipped"}}' '["b"]'
check fail '{"a":{"result":"skipped"},"b":{"result":"skipped"}}' '["b"]'
check fail '{"a":{"result":"success"},"b":{"result":"failure"}}' '["b"]'
check fail '{"a":{"result":"cancelled"},"b":{"result":"success"}}'
check fail '{}'
echo "ci-needs-passed tests passed"
