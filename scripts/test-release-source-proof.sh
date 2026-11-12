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
			body="{\"workflow_runs\":[{\"id\":7,\"status\":\"$status\",\"conclusion\":\"$CONCLUSION\",\"html_url\":\"https://example.test/runs/7\"}]}"
		fi
		;;
	*"actions/runs/7/jobs"*) body="{\"jobs\":$JOBS}" ;;
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
		out="$out${out:+,}{\"name\":\"$name\",\"conclusion\":\"$conclusion\"}"
	done
	echo "[$out]"
}

passing() {
	jobs gate success verify success verify-linux-rest success \
		"verify-linux-daemon (0)" success "verify-linux-daemon (1)" success "verify-linux-daemon (2)" success \
		verify-linux success linux-archives success package "${1:-success}"
}

prove() {
	: >"$work/output"
	rm -f "$work/polls"
	PATH="$work/bin:$PATH" STATE="$work" GITHUB_REPOSITORY=owner/repo GITHUB_OUTPUT="$work/output" POLL_SECONDS=0 POLL_LIMIT=5 \
		"$ROOT/scripts/release-source-proof.sh" abc >/dev/null
}

value() { sed -n "s/^$1=//p" "$work/output"; }

expect() {
	local want="$1" reason="$2"
	[ "$(value proven)" = "$want" ] || fail "$3: proven=$(value proven), want $want ($(value reason))"
	[[ "$(value reason)" == *"$reason"* ]] || fail "$3: reason '$(value reason)' lacks '$reason'"
}

STATUS=completed CONCLUSION=success JOBS="$(passing)" prove
expect true "" "every required job passed"
[ "$(value run)" = https://example.test/runs/7 ] || fail "the proof names its run"

STATUS=completed CONCLUSION=success JOBS="$(passing skipped)" prove
expect true "" "a job outside the required set does not count"

STATUS=none CONCLUSION="" JOBS="[]" prove
expect false "no CI run" "no push run"

STATUS=completed CONCLUSION=failure JOBS="$(passing)" prove
expect false "ended failure" "a failed run"

STATUS=completed CONCLUSION=cancelled JOBS="$(passing)" prove
expect false "ended cancelled" "a cancelled run"

STATUS=completed CONCLUSION=success JOBS="$(jobs verify skipped verify-linux-rest success "verify-linux-daemon (0)" success verify-linux success)" prove
expect false "verify skipped" "a skipped verify"

STATUS=completed CONCLUSION=success JOBS="$(jobs verify success verify-linux-rest success "verify-linux-daemon (0)" success "verify-linux-daemon (1)" failure verify-linux success)" prove
expect false "verify-linux-daemon (1) failure" "a failed shard"

STATUS=completed CONCLUSION=success JOBS="$(jobs verify success verify-linux-rest success verify-linux success)" prove
expect false "no verify-linux-daemon shard" "no shards"

STATUS=completed CONCLUSION=success JOBS="$(jobs verify success "verify-linux-daemon (0)" success verify-linux success)" prove
expect false "verify-linux-rest missing" "a missing job"

STATUS=completed CONCLUSION=success RUNNING_POLLS=2 JOBS="$(passing)" prove
expect true "" "a run still going is waited for"
[ "$(cat "$work/polls")" = 3 ] || fail "waited for $(cat "$work/polls") polls, want 3"

STATUS=completed CONCLUSION=success RUNNING_POLLS=9 JOBS="$(passing)" prove
expect false "still in_progress after 5 checks" "a run that outlasts the wait"

echo "release source proof tests passed"
