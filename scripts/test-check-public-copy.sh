#!/usr/bin/env bash
# Runs scripts/check-public-copy.sh in a scratch repository holding the files
# it reads, to show it fails on a planted machine path and fails closed when a
# file or tool it needs is missing.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHECK="$ROOT/scripts/check-public-copy.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-public-copy.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
REPO="$WORK/repo"

failures=0
fail() {
  echo "FAIL: $*"
  failures=$((failures + 1))
}

mkdir -p "$REPO"
(
  cd "$REPO"
  git init -q
  for file in README.md SECURITY.md docs/native-apps.md internal/daemon/dashboard/index.html internal/daemon/dashboard/app.js \
    native/AgenthailApp.swift native/macOS/DesktopWindow.swift native/macOS/DesktopSettings.swift native/macOS/CommandPalette.swift \
    native/macOS/NewSession.swift native/iOS/AgenthailIOSViews.swift native/AgenthailAPI.swift native/project.yml \
    native/Agenthail.xcodeproj/project.pbxproj .github/workflows/ci.yml push-relay/wrangler.toml skills/x/SKILL.md ci/x scripts/x.sh; do
    mkdir -p "$(dirname "$file")"
    echo "plain text" >"$file"
  done
  # shellcheck disable=SC2016
  printf '%s\n' 'cache under /Volumes/work/cache and /Users/demo/project' 'case "$path" in /Volumes/*) refuse ;; esac' >scripts/guard.sh
  git add -A
)

# check <expected exit> [env...]: runs the check in the scratch repository.
check() {
  local expected="$1" status=0
  shift
  (cd "$REPO" && env "$@" bash "$CHECK") >"$WORK/out" 2>&1 || status=$?
  [ "$status" = "$expected" ] || fail "exit $status, want $expected with ${*:-the normal PATH}: $(cat "$WORK/out")"
}

check 0

for planted in '/Users/someone/project' '/Volumes/Backup/cache' '/home/someone/.config' 'zains-mac.tail1234.ts.net' "Zain's MacBook" "Zain$(printf '\xe2\x80\x99')s iPhone" 'session_0123456789abcdef' 'ssh forge-agents' 'the mini2 runner' 'agenthail-mini-2-1' 'vps2' 'zains-mac-mini'; do
  printf 'first line\nsee %s\n' "$planted" >"$REPO/docs/planted.md"
  check 1
  grep -qx "docs/planted.md:2:see $planted" "$WORK/out" || fail "the planted $planted was not named: $(cat "$WORK/out")"
done
echo "case \"\$path\" in /Volumes/*) ;; esac /Volumes/*/x" >"$REPO/docs/planted.md"
check 1
rm "$REPO/docs/planted.md"

printf 'forge a release\nthe gemini2 model\nmini2x and vps20\n' >"$REPO/docs/words.md"
check 0
rm "$REPO/docs/words.md"

echo "see /Users/someone/project" >"$REPO/docs/untracked.md"
check 1
rm "$REPO/docs/untracked.md"
check 0

# tools <dir> <tool...>: a PATH directory holding only the named tools.
tools() {
  local dir="$1" tool
  shift
  mkdir -p "$dir"
  for tool in "$@"; do
    ln -s "$(type -P "$tool")" "$dir/$tool"
  done
}
for missing in git grep mktemp perl rm; do
  dir="$WORK/without-$missing"
  for tool in bash env git grep mktemp perl rm; do
    [ "$tool" = "$missing" ] || tools "$dir" "$tool"
  done
  check 2 PATH="$dir"
  grep -q "$missing is required" "$WORK/out" || fail "a missing $missing was not named: $(cat "$WORK/out")"
done

tools "$WORK/failing-git" bash env grep mktemp perl rm
printf '#!/bin/sh\nexit 1\n' >"$WORK/failing-git/git"
chmod +x "$WORK/failing-git/git"
check 2 PATH="$WORK/failing-git"
grep -q 'git could not list' "$WORK/out" || fail "a failing git listing was not reported: $(cat "$WORK/out")"

(cd "$REPO" && echo "plain text" >docs/tracked.md && git add docs/tracked.md && rm docs/tracked.md)
check 2
grep -q 'docs/tracked.md' "$WORK/out" || fail "a tracked file missing from the worktree was not named: $(cat "$WORK/out")"
(cd "$REPO" && git rm -q --cached docs/tracked.md)

mv "$REPO/SECURITY.md" "$WORK/SECURITY.md"
check 2
mv "$WORK/SECURITY.md" "$REPO/SECURITY.md"
check 0

if [ "$failures" -gt 0 ]; then
  exit 1
fi
echo "public copy check ok"
