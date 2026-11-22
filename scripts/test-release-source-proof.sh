#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

cat >"$work/bin/gh" <<'SH'
#!/usr/bin/env bash
[ "$1" = api ] || { echo "unexpected gh $*" >&2; exit 1; }
path="$2" filter="."
[ "${3:-}" = --jq ] && filter="$4"
case "$path" in
	*"workflows/ci.yml/runs?head_sha=abc&event=push&branch=main&"*)
		count="$(cat "$STATE/polls" 2>/dev/null || echo 0)"
		echo $((count + 1)) >"$STATE/polls"
		status="$STATUS"
		[ "$count" -ge "${RUNNING_POLLS:-0}" ] || status=in_progress
		if [ "$status" = none ]; then
			body='{"workflow_runs":[]}'
		else
			body="{\"workflow_runs\":[{\"id\":7,\"status\":\"$status\",\"html_url\":\"https://example.test/runs/7\"}]}"
		fi
		;;
	*"actions/runs/7/jobs"*)
		jobs="$JOBS"
		[ "$(cat "$STATE/polls")" -gt "${RUNNING_POLLS:-0}" ] || jobs="${JOBS_RUNNING:-$JOBS}"
		body="{\"jobs\":$jobs}"
		;;
	*) echo "unexpected gh $*" >&2; exit 1 ;;
esac
jq -r "$filter" <<<"$body"
SH
chmod +x "$work/bin/gh"

jobs() {
	local out="" name conclusion
	while [ $# -gt 0 ]; do
		name="$1" conclusion="$2"
		shift 2
		if [ "$conclusion" = running ]; then
			out="$out${out:+,}{\"name\":\"$name\",\"status\":\"in_progress\",\"conclusion\":null}"
		else
			out="$out${out:+,}{\"name\":\"$name\",\"status\":\"completed\",\"conclusion\":\"$conclusion\"}"
		fi
	done
	echo "[$out]"
}

passing() {
	jobs gate success verify success verify-linux-rest success \
		"verify-linux-daemon (0)" success "verify-linux-daemon (1)" success "verify-linux-daemon (2)" success \
		verify-linux success linux-archives success package "${1:-success}"
}

building() {
	jobs gate success verify success verify-linux-rest success \
		"verify-linux-daemon (0)" success "verify-linux-daemon (1)" running "verify-linux-daemon (2)" success \
		verify-linux running package running
}

prove() {
	: >"$work/output"
	: >"$work/summary"
	rm -f "$work/polls"
	PATH="$work/bin:$PATH" STATE="$work" GITHUB_REPOSITORY=owner/repo GITHUB_OUTPUT="$work/output" GITHUB_STEP_SUMMARY="$work/summary" POLL_SECONDS=0 POLL_LIMIT=5 \
		"$ROOT/scripts/release-source-proof.sh" abc >/dev/null
}

value() { sed -n "s/^$1=//p" "$work/output"; }

expect() {
	local want="$1" reason="$2"
	[ "$(value proven)" = "$want" ] || fail "$3: proven=$(value proven), want $want ($(value reason))"
	[[ "$(value reason)" == *"$reason"* ]] || fail "$3: reason '$(value reason)' lacks '$reason'"
}

STATUS=completed JOBS="$(passing)" prove
expect true "" "every required job passed"
[ "$(value run)" = https://example.test/runs/7 ] || fail "the proof names its run"
for line in "- verify: success" "- verify-linux-rest: success" "- verify-linux: success" "- verify-linux-daemon (2): success"; do
	grep -qxF -- "$line" "$work/summary" || fail "the summary does not show '$line':\n$(cat "$work/summary")"
done
! grep -q -- "- package" "$work/summary" || fail "the summary lists a job outside the required set"

STATUS=completed JOBS="$(passing failure)" prove
expect true "" "a failed job outside the required set does not count"

STATUS=none JOBS="[]" prove
expect false "no CI run" "no push run"

STATUS=completed JOBS="$(jobs verify cancelled verify-linux-rest cancelled verify-linux cancelled)" prove
expect false "verify cancelled" "a cancelled run"

STATUS=completed JOBS="$(jobs verify skipped verify-linux-rest success "verify-linux-daemon (0)" success verify-linux success)" prove
expect false "verify skipped" "a skipped verify"
grep -qxF -- "- verify: skipped" "$work/summary" || fail "a failed proof does not show the job that failed it"

STATUS=completed JOBS="$(jobs verify success verify-linux-rest success "verify-linux-daemon (0)" success "verify-linux-daemon (1)" failure verify-linux success)" prove
expect false "verify-linux-daemon (1) failure" "a failed shard"

STATUS=completed JOBS="$(jobs verify success verify-linux-rest success verify-linux success)" prove
expect false "no verify-linux-daemon shard" "no shards"

STATUS=completed JOBS="$(jobs verify success "verify-linux-daemon (0)" success verify-linux success)" prove
expect false "verify-linux-rest missing" "a missing job"

STATUS=completed RUNNING_POLLS=9 JOBS="$(passing)" prove
expect true "" "a run still going is not waited for once its required jobs passed"
[ "$(cat "$work/polls")" = 1 ] || fail "took $(cat "$work/polls") polls, want 1"

STATUS=completed RUNNING_POLLS=9 JOBS="$(jobs verify failure verify-linux-rest running verify-linux running)" prove
expect false "verify failure" "a required job that fails while the run goes on"
[ "$(cat "$work/polls")" = 1 ] || fail "a failed required job took $(cat "$work/polls") polls, want 1"

STATUS=completed RUNNING_POLLS=2 JOBS_RUNNING="$(building)" JOBS="$(passing)" prove
expect true "" "required jobs still running are waited for"
[ "$(cat "$work/polls")" = 3 ] || fail "waited for $(cat "$work/polls") polls, want 3"

STATUS=completed RUNNING_POLLS=9 JOBS_RUNNING="$(building)" JOBS="$(passing)" prove
expect false "still running verify-linux in_progress after 5 checks" "required jobs that outlast the wait"

echo "release source proof tests passed"
