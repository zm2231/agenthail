#!/usr/bin/env bash
set -euo pipefail

udid="${1:?usage: $0 SIMULATOR_UDID}"
if [ "${GITHUB_ACTIONS:-}" != true ] || [ "${RUNNER_ENVIRONMENT:-}" != self-hosted ] || [ -z "${RUNNER_NAME:-}" ]; then
  echo "$0: refusing: only a self-hosted GitHub Actions job resets a simulator keychain" >&2
  exit 1
fi
name="Agenthail CI $RUNNER_NAME"
matches="$(xcrun simctl list devices --json | jq -r --arg name "$name" --arg udid "$udid" '[.devices[][] | select(.name == $name and .udid == $udid)] | length')"
if [ "$matches" != 1 ]; then
  echo "$0: refusing: $udid is not this runner's simulator, $name" >&2
  exit 1
fi
xcrun simctl keychain "$udid" reset
