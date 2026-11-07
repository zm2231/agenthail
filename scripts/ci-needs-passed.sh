#!/usr/bin/env bash
set -euo pipefail
results="${1:?usage: $0 '<toJSON(needs.*.result)>'}"
echo "needed jobs: $results"
jq -e 'type == "array" and length > 0 and all(. == "success")' <<<"$results" >/dev/null
