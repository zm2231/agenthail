#!/usr/bin/env bash
# Runs scripts/ci-runner-job-started.sh against event payloads, to show it
# refuses a pull request from another repository and any event it cannot read.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOK="$ROOT/scripts/ci-runner-job-started.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-runner-hook.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

failures=0
fail() {
  echo "FAIL: $*"
  failures=$((failures + 1))
}

# hook <expected exit> <event json> [env...]: runs the hook on the event.
hook() {
  local expected="$1" event="$2" status=0
  shift 2
  printf '%s' "$event" >"$WORK/event.json"
  env GITHUB_EVENT_PATH="$WORK/event.json" GITHUB_REPOSITORY=owner/repo "$@" bash "$HOOK" >"$WORK/out" 2>&1 || status=$?
  [ "$status" = "$expected" ] || fail "exit $status, want $expected for $event ${*:-}: $(cat "$WORK/out")"
}

hook 0 '{"pull_request":{"head":{"repo":{"full_name":"owner/repo"}}}}'
hook 1 '{"pull_request":{"head":{"repo":{"full_name":"someone/fork"}}}}'
hook 1 '{"pull_request":{"head":{"repo":null}}}'
hook 0 '{"ref":"refs/heads/main"}'
hook 1 '{"pull_request":'
grep -q 'could not read' "$WORK/out" || fail "a malformed event was not reported: $(cat "$WORK/out")"

mkdir -p "$WORK/bin"
ln -s "$(type -P bash)" "$WORK/bin/bash"
hook 1 '{"pull_request":{"head":{"repo":{"full_name":"someone/fork"}}}}' PATH="$WORK/bin"
grep -q 'could not read' "$WORK/out" || fail "a missing jq was not reported: $(cat "$WORK/out")"

if [ "$failures" -gt 0 ]; then
  exit 1
fi
echo "runner hook ok"
