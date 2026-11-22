#!/usr/bin/env bash
# Runs scripts/ci-darwin-tests.sh against stub go and ci-concurrently.sh, to show which parts each selection runs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-darwin-tests.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin" "$WORK/repo/scripts" "$WORK/repo/internal/daemon"
cp "$ROOT/scripts/ci-darwin-tests.sh" "$WORK/repo/scripts/"
cat >"$WORK/repo/scripts/ci-concurrently.sh" <<'SH'
#!/usr/bin/env bash
while [ $# -gt 0 ]; do echo "$1|$2"; shift 2; done >"$WORK/parts"
SH
cat >"$WORK/bin/go" <<'SH'
#!/usr/bin/env bash
case "$*" in
"list -m") echo example.com/m ;;
"run ./scripts/go-darwin-packages ./...") printf '%s\n' example.com/m/a example.com/m/b example.com/m/internal/daemon ;;
"test -c -o "*) printf '#!/usr/bin/env bash\nprintf "TestA\\nTestB\\n"\n' >"$4"; chmod +x "$4" ;;
"run ./scripts/go-test-shard -names 0 2 ./internal/daemon") echo TestA ;;
"run ./scripts/go-test-shard -names 1 2 ./internal/daemon") echo TestB ;;
"run ./scripts/go-test-shard "*) echo "^Test$3\$" ;;
*) echo "unexpected go $*" >&2; exit 1 ;;
esac
SH
chmod +x "$WORK/bin/go" "$WORK/repo/scripts/ci-concurrently.sh"

failures=0
# check <name> <want parts, joined by ;> <select result> <selected> <full>
check() {
	local name="$1" want="$2" got
	rm -f "$WORK/parts"
	if ! (cd "$WORK/repo" && env PATH="$WORK/bin:$PATH" WORK="$WORK" SHARDS=2 GUARD_CACHE=/cache SELECT_RESULT="$3" SELECTED="$4" FULL="$5" bash scripts/ci-darwin-tests.sh >"$WORK/out" 2>&1); then
		echo "FAIL: $name: the script failed:"
		sed 's/^/    /' "$WORK/out"
		failures=$((failures + 1))
		return
	fi
	got=""
	[ ! -f "$WORK/parts" ] || got="$(cut -d'|' -f1 "$WORK/parts" | tr '\n' ';')"
	[ "$got" = "$want" ] || { echo "FAIL: $name: ran '$got', want '$want'"; failures=$((failures + 1)); }
}

M=example.com/m
all="packages other than internal/daemon;internal/daemon shard 0;internal/daemon shard 1;dashboard readability guard;native helper tests;Textual table tests;"
check "a selection that did not run runs everything" "$all" failure "" true
check "a selection with no result runs everything" "$all" "" "" true
check "one selected package runs only the macOS package tests" "packages other than internal/daemon;" success "$M/a $M/c" true
check "a daemon change runs the shards and the guard" "internal/daemon shard 0;internal/daemon shard 1;dashboard readability guard;" success "$M/internal/daemon" true
check "a native change runs the helper tests" "native helper tests;" success "suite:native-helpers" true
check "a Textual change runs the Textual tests" "Textual table tests;" success "suite:textual" true
check "without full CI the native suites stay off" "" success "suite:native-helpers suite:textual" false
check "an empty selection runs nothing" "" success "" true

rm -f "$WORK/parts"
(cd "$WORK/repo" && env PATH="$WORK/bin:$PATH" WORK="$WORK" SHARDS=2 GUARD_CACHE=/cache SELECT_RESULT=success SELECTED="$M/b $M/internal/daemon" FULL=true bash scripts/ci-darwin-tests.sh >/dev/null 2>&1)
tested="$(grep '^packages other' "$WORK/parts" | cut -d'|' -f2)"
[ "$tested" = "go test $M/b  -count=1 -timeout 25m" ] || { echo "FAIL: the macOS package tests ran '$tested', want only $M/b"; failures=$((failures + 1)); }

[ "$failures" -eq 0 ] || exit 1
echo "ci-darwin-tests ok"
