#!/usr/bin/env bash
# Runs scripts/ci-full-gate.sh and scripts/ci-full-label.sh against a stub gh, to
# show that a full-ci label reruns only a run whose gate already skipped the full
# jobs, and that the gate reads the labels the pull request has now.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-full-label.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

failures=0
fail() {
	echo "FAIL: $*"
	failures=$((failures + 1))
}

# The stub answers `gh api <path>` from $ANSWERS/<path with / and ? as _>, taking
# numbered answers .1, .2, ... in turn when they exist, and logs `gh run` calls.
mkdir -p "$WORK/bin"
cat >"$WORK/bin/gh" <<'SH'
#!/usr/bin/env bash
if [ "$1" = run ]; then
	echo "$*" >>"$CALLS"
	exit 0
fi
[ "$1" = api ] || exit 2
shift
[ "$1" = --paginate ] && shift
key="$(printf '%s' "$1" | tr '/?&=' '____')"
count_file="$ANSWERS/.count.$key"
n=$(( $(cat "$count_file" 2>/dev/null || echo 0) + 1 ))
echo "$n" >"$count_file"
if [ -f "$ANSWERS/$key.$n" ]; then
	answer="$ANSWERS/$key.$n"
elif [ -f "$ANSWERS/$key" ]; then
	answer="$ANSWERS/$key"
else
	answer="$(ls "$ANSWERS/$key".* 2>/dev/null | sort -t. -k2 -n | tail -1)"
fi
[ -n "$answer" ] || { echo "no answer for $1" >&2; exit 1; }
if [ "${2:-}" = --jq ]; then
	jq -r "$3" "$answer"
else
	cat "$answer"
fi
SH
chmod +x "$WORK/bin/gh"

RUNS='repos_owner_repo_actions_workflows_ci.yml_runs_event_pull_request_head_sha_abc_per_page_1'
JOBS='repos_owner_repo_actions_runs_7_jobs_per_page_100'
RUN='repos_owner_repo_actions_runs_7'

jobs_json() {
	local gate="$1" installer="$2"
	printf '{"jobs":[{"name":"verify","status":"in_progress","conclusion":null},{"name":"gate","status":"%s","conclusion":%s},{"name":"installer","status":"completed","conclusion":%s}]}' "${gate%/*}" "${gate#*/}" "$installer"
}

# label <name> <expected calls> [setup...]: runs ci-full-label.sh for ci.yml.
label() {
	local name="$1" want="$2" status=0
	rm -rf "$WORK/answers" "$WORK/calls"
	mkdir -p "$WORK/answers"
	touch "$WORK/calls"
	env PATH="$WORK/bin:$PATH" ANSWERS="$WORK/answers" CALLS="$WORK/calls" GITHUB_REPOSITORY=owner/repo HEAD_SHA=abc \
		POLL_SECONDS=0 POLL_LIMIT=3 SETUP="$3" bash -c 'eval "$SETUP"; exec bash "$0" ci.yml gate installer package' "$ROOT/scripts/ci-full-label.sh" >"$WORK/out" 2>&1 || status=$?
	local calls
	calls="$(tr '\n' ';' <"$WORK/calls")"
	[ "$calls" = "$want" ] || fail "$name: gh run calls '$calls', want '$want': $(cat "$WORK/out")"
	[ "$status" = "${4:-0}" ] || fail "$name: exit $status, want ${4:-0}: $(cat "$WORK/out")"
}

A="\$ANSWERS"
label "no run yet" "" "echo '{\"workflow_runs\":[]}' >$A/$RUNS"
label "full jobs already in the run" "" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json completed/'"success"' null)' >$A/$JOBS"
label "finished run that skipped them" "run rerun 7 -R owner/repo;" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json completed/'"success"' '"skipped"')' >$A/$JOBS; echo '{\"status\":\"completed\"}' >$A/$RUN"
label "running run that skipped them" "run cancel 7 -R owner/repo;run rerun 7 -R owner/repo;" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json completed/'"success"' '"skipped"')' >$A/$JOBS; echo '{\"status\":\"in_progress\"}' >$A/$RUN.1; echo '{\"status\":\"in_progress\"}' >$A/$RUN.2; echo '{\"status\":\"completed\"}' >$A/$RUN.3"
label "gate still deciding, then takes the label" "" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json in_progress/null null)' >$A/$JOBS.1; echo '$(jobs_json completed/'"success"' null)' >$A/$JOBS.2"
label "gate still deciding, then skips them" "run rerun 7 -R owner/repo;" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json in_progress/null null)' >$A/$JOBS.1; echo '$(jobs_json completed/'"success"' '"skipped"')' >$A/$JOBS.2; echo '{\"status\":\"completed\"}' >$A/$RUN"
label "run cancelled before its gate decided" "" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json completed/'"cancelled"' '"cancelled"')' >$A/$JOBS"
label "run cancelled after its gate skipped them" "" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json completed/'"success"' '"skipped"')' >$A/$JOBS; echo '{\"status\":\"completed\",\"conclusion\":\"cancelled\"}' >$A/$RUN"
label "gate never finishes" "" "echo '{\"workflow_runs\":[{\"id\":7}]}' >$A/$RUNS; echo '$(jobs_json queued/null null)' >$A/$JOBS" 1

IOS_RUNS='repos_owner_repo_actions_workflows_ios.yml_runs_event_pull_request_head_sha_abc_per_page_1'
ios_label() {
	local name="$1" want="$2" release="$3" ios="${4:-skipped}" ran="${5:-ios-release}" force="${6:-}"
	rm -rf "$WORK/answers" "$WORK/calls"
	mkdir -p "$WORK/answers"
	touch "$WORK/calls"
	echo '{"workflow_runs":[{"id":7}]}' >"$WORK/answers/$IOS_RUNS"
	printf '{"jobs":[{"name":"changes","status":"completed","conclusion":"success"},{"name":"ios","status":"completed","conclusion":"%s"},{"name":"ios-release","status":"completed","conclusion":"%s"}]}' "$ios" "$release" >"$WORK/answers/$JOBS"
	echo '{"status":"completed"}' >"$WORK/answers/$RUN"
	env PATH="$WORK/bin:$PATH" ANSWERS="$WORK/answers" CALLS="$WORK/calls" GITHUB_REPOSITORY=owner/repo HEAD_SHA=abc POLL_SECONDS=0 POLL_LIMIT=3 RAN="$ran" FORCE="$force" \
		bash "$ROOT/scripts/ci-full-label.sh" ios.yml changes ios >"$WORK/out" 2>&1 || fail "$name: $(cat "$WORK/out")"
	local calls
	calls="$(tr '\n' ';' <"$WORK/calls")"
	[ "$calls" = "$want" ] || fail "$name: gh run calls '$calls', want '$want': $(cat "$WORK/out")"
}
ios_label "iOS inputs changed" "run rerun 7 -R owner/repo;" success
ios_label "no iOS input changed" "" skipped
ios_label "ui-all after ios ran its smoke tier" "run rerun 7 -R owner/repo;" success success ios 1
ios_label "ui-all with no iOS input changed" "" success skipped ios 1
ios_label "ios ran and nothing forces a rerun" "" success success ios

# gate <expected full> <head ref> <labels json> [event]: runs ci-full-gate.sh.
gate() {
	local want="$1" ref="$2" labels="$3" event="${4:-pull_request}"
	rm -rf "$WORK/answers" && mkdir -p "$WORK/answers"
	printf '{"labels":%s}' "$labels" >"$WORK/answers/repos_owner_repo_pulls_12"
	: >"$WORK/output"
	env PATH="$WORK/bin:$PATH" ANSWERS="$WORK/answers" CALLS="$WORK/calls" GITHUB_REPOSITORY=owner/repo GITHUB_OUTPUT="$WORK/output" GITHUB_EVENT_NAME="$event" \
		HEAD_REF="$ref" PR_NUMBER=12 bash "$ROOT/scripts/ci-full-gate.sh" >"$WORK/out" 2>&1 || fail "gate $ref $labels: $(cat "$WORK/out")"
	grep -qx "full=$want" "$WORK/output" || fail "gate $ref $labels: $(cat "$WORK/output"), want full=$want"
}

gate true feature/x '[{"name":"type:bug"},{"name":"full-ci"}]'
gate false feature/x '[{"name":"type:bug"}]'
gate false feature/x '[{"name":"full-ci-later"}]'
gate true train/42 '[]'
gate true '' '[]' push

if [ "$failures" -gt 0 ]; then
	exit 1
fi
echo "full-ci label ok"
