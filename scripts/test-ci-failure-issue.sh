#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'SH'
#!/usr/bin/env bash
[ "${GH_REPO:-}" = owner/repo ] || { echo "gh ran without GH_REPO" >&2; exit 1; }
printf '%s\n' "$*" >>"$CALLS"
if [ "$1 $2" = "issue list" ]; then printf '%s\n' "$OPEN"; fi
SH
chmod +x "$work/bin/gh"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

title="Nightly iPhone UI tests are failing"
report() {
	local result="$1" open="$2" artifact="${3-https://example.test/artifact}" assignee="${4-}"
	: >"$work/calls"
	PATH="$work/bin:$PATH" CALLS="$work/calls" OPEN="$open" ARTIFACT_URL="$artifact" ISSUE_ASSIGNEE="$assignee" GH_REPO=owner/repo \
		GITHUB_SERVER_URL=https://github.com GITHUB_REPOSITORY=owner/repo GITHUB_RUN_ID=42 \
		"$ROOT/scripts/ci-failure-issue.sh" "$result" "$title" "nightly iPhone UI test run" >/dev/null
	sed 1d "$work/calls"
}

run="https://github.com/owner/repo/actions/runs/42"
matching="[{\"number\":7,\"title\":\"$title\"}]"
similar="[{\"number\":9,\"title\":\"$title on main\"}]"

calls="$(report failure '[]')"
[[ "$calls" == "issue create --title $title --body The nightly iPhone UI test run ended with \`failure\`: $run"*"Test results: https://example.test/artifact" ]] || fail "a first failure opens the issue: $calls"

calls="$(report failure '[]' https://example.test/artifact owner)"
[[ "$calls" == "issue create "*" --assignee owner" ]] || fail "a new issue is assigned so a failure reaches someone: $calls"

calls="$(report failure "$matching")"
[[ "$calls" == "issue comment 7 --body "*"$run"* ]] || fail "a repeated failure comments on the open issue: $calls"

calls="$(report failure "$similar")"
[[ "$calls" == "issue create "* ]] || fail "an issue with a different title is not this issue: $calls"

calls="$(report cancelled '[]' '')"
[[ "$calls" == *"\`cancelled\`: $run"*"No test results were saved"* ]] || fail "a run that stopped before its results is reported with the reason: $calls"

calls="$(report success "$matching")"
[ "$calls" = "issue close 7 --comment Passing again: $run" ] || fail "a passing run closes the open issue: $calls"

calls="$(report success '[]')"
[ -z "$calls" ] || fail "a passing run with no open issue does nothing: $calls"

echo "ci-failure-issue tests passed"
