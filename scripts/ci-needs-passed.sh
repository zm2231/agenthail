#!/usr/bin/env bash
# Usage: ci-needs-passed.sh '<toJSON(needs)>' ['<JSON list of jobs the run chose to skip>']
set -euo pipefail
needs="${1:?usage: $0 '<toJSON(needs)>' ['<skippable jobs>']}"
skippable="${2:-[]}"
echo "needed jobs: $(jq -c 'map_values(.result)' <<<"$needs")"
echo "chosen to skip: $skippable"
jq -e --argjson skip "$skippable" 'type == "object" and length > 0 and (to_entries | all(.value.result == "success" or (.value.result == "skipped" and (.key as $k | $skip | index($k) != null))))' <<<"$needs" >/dev/null
