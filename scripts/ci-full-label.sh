#!/usr/bin/env bash
# Runs the full-CI jobs for a pull request that was just labelled full-ci.
# Usage: ci-full-label.sh <workflow file> <gate job> <full job>...
#
# The workflow's gate job reads the labels when it runs, so a run whose gate
# has not finished will pick the label up by itself. Only a run whose gate
# already finished and skipped the full jobs is rerun, cancelled first if it is
# still going; the rerun's gate then reads the label. A run that already has the
# full jobs, or was cancelled before its gate decided, is left alone.
# With RAN set to a job name, a run where that job was skipped is left alone too:
# iOS skips `ios` when no iOS input changed, and a rerun would skip it again.
# Needs GH_TOKEN, GITHUB_REPOSITORY and HEAD_SHA. POLL_SECONDS and POLL_LIMIT
# bound the wait for the gate and for a cancel to finish.
set -euo pipefail

workflow="$1" gate="$2"
shift 2
poll="${POLL_SECONDS:-10}" limit="${POLL_LIMIT:-60}"

api() {
  gh api "repos/$GITHUB_REPOSITORY/$1"
}

run="$(api "actions/workflows/$workflow/runs?event=pull_request&head_sha=$HEAD_SHA&per_page=1" | jq -r '.workflow_runs[0].id // empty')"
if [ -z "$run" ]; then
  echo "$workflow: no run for $HEAD_SHA yet; its gate will read the label"
  exit 0
fi

for ((attempt = 1; ; attempt++)); do
  jobs="$(api "actions/runs/$run/jobs?per_page=100")"
  gate_state="$(jq -r --arg gate "$gate" '[.jobs[] | select(.name == $gate)][0] | if . == null then "missing" else "\(.status)/\(.conclusion)" end' <<<"$jobs")"
  case "$gate_state" in
  completed/success) break ;;
  completed/* | missing)
    echo "$workflow run $run: gate is $gate_state; nothing to rerun"
    exit 0
    ;;
  esac
  if [ "$attempt" -ge "$limit" ]; then
    echo "$workflow run $run: gate still $gate_state after $limit checks" >&2
    exit 1
  fi
  sleep "$poll"
done

if [ -n "${RAN:-}" ] && [ "$(jq -r --arg ran "$RAN" '[.jobs[] | select(.name == $ran)][0].conclusion // "missing"' <<<"$jobs")" = skipped ]; then
  echo "$workflow run $run: $RAN was skipped, so the full jobs have nothing to run"
  exit 0
fi
skipped="$(jq -r '[.jobs[] | select(.name as $name | $ARGS.positional | index($name)) | select(.conclusion == "skipped")] | length' --args "$@" <<<"$jobs")"
if [ "$skipped" = 0 ]; then
  echo "$workflow run $run: the full jobs are already part of it"
  exit 0
fi

if [ "$(api "actions/runs/$run" | jq -r .status)" != completed ]; then
  gh run cancel "$run" -R "$GITHUB_REPOSITORY"
  for ((attempt = 1; ; attempt++)); do
    [ "$(api "actions/runs/$run" | jq -r .status)" = completed ] && break
    if [ "$attempt" -ge "$limit" ]; then
      echo "$workflow run $run: still not cancelled after $limit checks" >&2
      exit 1
    fi
    sleep "$poll"
  done
fi
gh run rerun "$run" -R "$GITHUB_REPOSITORY"
echo "$workflow run $run: rerun with the full jobs"
