#!/usr/bin/env bash
# Runs scripts/ci-go-select.sh against stub go and git, to show which selection each kind of run asks for and which jobs it lets skip.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-go-select.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat >"$WORK/bin/go" <<'SH'
#!/usr/bin/env bash
if [ "$*" = "list -m" ]; then echo example.com/m; exit 0; fi
echo "${*:3}" >"$WORK/args"
printf '%s\n' $SELECTED
SH
cat >"$WORK/bin/git" <<'SH'
#!/usr/bin/env bash
echo "$*" >"$WORK/git"
SH
chmod +x "$WORK/bin/go" "$WORK/bin/git"

failures=0
# check <name> <want args> <want outputs> <event> <head ref> <stacked> <stack base> <selected>
check() {
	local name="$1" want_args="$2" want="$3"
	rm -f "$WORK/out" "$WORK/args"
	(cd "$ROOT" && env PATH="$WORK/bin:$PATH" WORK="$WORK" GITHUB_OUTPUT="$WORK/out" EVENT="$4" HEAD_REF="$5" BASE=base1 STACKED="$6" STACK_BASE_REF="$7" SELECTED="$8" bash scripts/ci-go-select.sh >/dev/null)
	local got args
	got="$(tr '\n' ';' <"$WORK/out")"
	args="$(cat "$WORK/args")"
	[ "$args" = "$want_args" ] || { echo "FAIL: $name: selector given '$args', want '$want_args'"; failures=$((failures + 1)); }
	[ "$got" = "$want" ] || { echo "FAIL: $name: outputs '$got', want '$want'"; failures=$((failures + 1)); }
}

M=example.com/m
check "a push selects everything" "-all" "daemon=true;surfaces=true;packages=$M/a $M/b;skippable=[];selected=$M/a $M/b $M/internal/daemon $M/internal/surface/claudesurface;" \
	push "" false "" "$M/a $M/b $M/internal/daemon $M/internal/surface/claudesurface"
check "a merge train selects everything" "-all" "daemon=false;surfaces=false;packages=$M/a;skippable=[\"verify-linux-daemon\",\"verify-linux-surfaces\"];selected=$M/a;" \
	pull_request train/1 false "" "$M/a"
check "a pull request diffs against its base" "-base base1" "daemon=true;surfaces=false;packages=$M/a;skippable=[\"verify-linux-surfaces\"];selected=$M/a $M/internal/daemon;" \
	pull_request feature false "" "$M/a $M/internal/daemon"
check "a Node host change runs the surfaces job" "-base base1" "daemon=false;surfaces=true;packages=;skippable=[\"verify-linux-daemon\"];selected=suite:node-hosts;" \
	pull_request feature false "" "suite:node-hosts"
check "a stack diffs against the stack's base" "-base origin/main" "daemon=false;surfaces=true;packages=;skippable=[\"verify-linux-daemon\"];selected=$M/internal/surface/codexsurface;" \
	pull_request feature true main "$M/internal/surface/codexsurface"
check "a stack with no base selects everything" "-all" "daemon=false;surfaces=false;packages=;skippable=[\"verify-linux-daemon\",\"verify-linux-surfaces\"];selected=;" \
	pull_request feature true "" ""
[ "$failures" -eq 0 ] || exit 1
echo "ci-go-select ok"
