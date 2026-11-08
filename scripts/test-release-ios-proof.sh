#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

repo="$work/repo"
mkdir -p "$repo/scripts" "$repo/native/iOS" "$work/bin"
cp "$ROOT/scripts/ios-inputs.sh" "$ROOT/scripts/release-ios-proof.sh" "$repo/scripts/"
printf 'targets:\n  AgenthailIOS:\n    sources:\n      - path: iOS\n      - path: Shared.swift\n  AgenthailIOSTests:\n    sources:\n      - path: ../scripts/fixtures/colours.json\n' >"$repo/native/project.yml"
echo a >"$repo/native/iOS/App.swift"
git -C "$repo" init -q -b main
git -C "$repo" -c user.name=t -c user.email=t@example.test add -A
git -C "$repo" -c user.name=t -c user.email=t@example.test commit -qm "ios"
ios_commit="$(git -C "$repo" rev-parse HEAD)"
echo docs >"$repo/README.md"
git -C "$repo" -c user.name=t -c user.email=t@example.test add -A
git -C "$repo" -c user.name=t -c user.email=t@example.test commit -qm "docs"
docs_commit="$(git -C "$repo" rev-parse HEAD)"

cat >"$work/bin/gh" <<'SH'
#!/usr/bin/env bash
[ "$1" = api ] || { echo "unexpected gh $*" >&2; exit 1; }
path="$2" filter="."
[ "${3:-}" = --jq ] && filter="$4"
case "$path" in
	*"workflows/ios.yml/runs?head_sha=$WANT_SHA&"*) body="${RUNS:-[]}"; body="{\"workflow_runs\":$body}" ;;
	*"workflows/ios.yml/runs?head_sha="*) body='{"workflow_runs":[]}' ;;
	*"actions/runs/11/jobs"*) body="{\"jobs\":[{\"name\":\"changes\",\"conclusion\":\"success\"},{\"name\":\"ios\",\"conclusion\":\"$JOB_11\"}]}" ;;
	*"actions/runs/12/jobs"*) body="{\"jobs\":[{\"name\":\"ios\",\"conclusion\":\"$JOB_12\"}]}" ;;
	*) echo "unexpected gh $*" >&2; exit 1 ;;
esac
jq -r "$filter" <<<"$body"
SH
chmod +x "$work/bin/gh"

runs() {
	local out="" id conclusion
	while [ $# -gt 0 ]; do
		id="$1" conclusion="$2"
		shift 2
		out="$out${out:+,}{\"id\":$id,\"html_url\":\"https://example.test/runs/$id\",\"conclusion\":\"$conclusion\"}"
	done
	echo "[$out]"
}

find_proof() {
	: >"$work/output"
	: >"$work/summary"
	PATH="$work/bin:$PATH" GITHUB_REPOSITORY=owner/repo GITHUB_OUTPUT="$work/output" GITHUB_STEP_SUMMARY="$work/summary" \
		"$repo/scripts/release-ios-proof.sh" find "$1" >/dev/null
}

value() { sed -n "s/^$1=//p" "$work/output"; }

WANT_SHA="$ios_commit" RUNS="$(runs 11 cancelled 12 success)" JOB_11=cancelled JOB_12=success find_proof "$docs_commit"
[ "$(value proven)" = true ] && [ "$(value commit)" = "$ios_commit" ] && [ "$(value run)" = https://example.test/runs/12 ] ||
	fail "a docs-only tag is proven by the green run at the last iOS change: $(cat "$work/output")"
grep -q "iOS run https://example.test/runs/12 passed there, its ios job included" "$work/summary" || fail "the proof is not in the job summary: $(cat "$work/summary")"

WANT_SHA="$ios_commit" RUNS="$(runs 11 cancelled 12 failure)" JOB_11=cancelled JOB_12=failure find_proof "$docs_commit"
[ "$(value proven)" = false ] && [ "$(value commit)" = "$ios_commit" ] && [[ "$(value reason)" == "no iOS run for a push of $ios_commit passed with its ios job" ]] ||
	fail "a cancelled and a failed run prove nothing: $(cat "$work/output")"

for run in cancelled failure; do
	WANT_SHA="$ios_commit" RUNS="$(runs 11 "$run")" JOB_11=success JOB_12= find_proof "$docs_commit"
	[ "$(value proven)" = false ] || fail "a run that ended $run proved the tag because its ios job passed: $(cat "$work/output")"
done

WANT_SHA="$ios_commit" RUNS="$(runs 11 success)" JOB_11=skipped JOB_12= find_proof "$docs_commit"
[ "$(value proven)" = false ] || fail "a run whose ios job was skipped proved the tag: $(cat "$work/output")"

WANT_SHA="$ios_commit" RUNS="" JOB_11= JOB_12= find_proof "$docs_commit"
[ "$(value proven)" = false ] || fail "a commit with no iOS run is not proven: $(cat "$work/output")"

echo "# changed" >>"$repo/scripts/ios-inputs.sh"
git -C "$repo" -c user.name=t -c user.email=t@example.test commit -qam "inputs"
inputs_commit="$(git -C "$repo" rev-parse HEAD)"
WANT_SHA="$inputs_commit" RUNS="$(runs 11 success)" JOB_11=success JOB_12= find_proof "$inputs_commit"
[ "$(value proven)" = true ] && [ "$(value commit)" = "$inputs_commit" ] ||
	fail "an edit to the iOS input list is an iOS change: $(cat "$work/output")"
WANT_SHA="$ios_commit" RUNS="$(runs 11 success 12 success)" JOB_11=success JOB_12=success find_proof "$inputs_commit"
[ "$(value proven)" = false ] || fail "a green run before the input list changed proved the tag: $(cat "$work/output")"

changed="$work/changed"
(cd "$repo" && scripts/ios-inputs.sh list) | grep -qx scripts/fixtures/colours.json || fail "a source outside native/ is not listed by its repository path"
for path in native/iOS/App.swift native/Shared.swift native/project.yml scripts/ios-inputs.sh scripts/release-ios-proof.sh .github/workflows/ios.yml scripts/fixtures/colours.json; do
	echo "$path" >"$changed"
	[ "$(cd "$repo" && scripts/ios-inputs.sh touched "$changed")" = true ] || fail "$path is an iOS input"
done
printf 'README.md\nnative/macOS/Window.swift\nnative/iOSX/App.swift\n' >"$changed"
[ "$(cd "$repo" && scripts/ios-inputs.sh touched "$changed")" = false ] || fail "a change outside the iOS inputs counted as one"

require() {
	: >"$work/summary"
	GITHUB_SHA="$docs_commit" GITHUB_REPOSITORY=owner/repo GITHUB_RUN_ID=99 GITHUB_SERVER_URL=https://github.com GITHUB_STEP_SUMMARY="$work/summary" \
		PROOF_COMMIT="$ios_commit" PROOF_RUN=https://example.test/runs/12 PROOF_REASON="${REASON-}" \
		"$repo/scripts/release-ios-proof.sh" require "$@" >"$work/log" 2>&1
}

require true skipped || fail "a proven commit is refused: $(cat "$work/log")"
grep -q "proven for $docs_commit by run https://example.test/runs/12 at $ios_commit" "$work/summary" || fail "the proving run is not named: $(cat "$work/summary")"
require false success || fail "a release whose own suite passed is refused: $(cat "$work/log")"
grep -q "ran the iOS suite at $docs_commit and it passed (https://github.com/owner/repo/actions/runs/99)" "$work/summary" || fail "the suite run is not named: $(cat "$work/summary")"
for suite in failure cancelled ""; do
	if REASON="no iOS run for a push of $ios_commit passed with its ios job" require false "$suite"; then
		fail "an unproven release with suite '$suite' was allowed"
	fi
	grep -q "### Release left as a draft" "$work/summary" && grep -q "no iOS run for a push of $ios_commit passed with its ios job" "$work/summary" ||
		fail "suite '$suite': the draft summary does not say why: $(cat "$work/summary")"
	grep -q "::error::Release left as a draft" "$work/log" || fail "suite '$suite': no error annotation: $(cat "$work/log")"
done

echo "release iOS proof tests passed"
