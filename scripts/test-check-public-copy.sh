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

for planted in '/Users/someone/project' '/Volumes/Backup/cache' '/home/someone/.config' 'zains-mac.tail1234.ts.net' "Zain's MacBook" "Zain$(printf '\xe2\x80\x99')s iPhone" 'session_0123456789abcdef'; do
  printf 'first line\nsee %s\n' "$planted" >"$REPO/docs/planted.md"
  check 1
  grep -qx "docs/planted.md:2:see $planted" "$WORK/out" || fail "the planted $planted was not named: $(cat "$WORK/out")"
done
echo "case \"\$path\" in /Volumes/*) ;; esac /Volumes/*/x" >"$REPO/docs/planted.md"
check 1
rm "$REPO/docs/planted.md"

echo "see /Users/someone/project" >"$REPO/docs/untracked.md"
check 1
rm "$REPO/docs/untracked.md"
check 0

mkdir -p "$WORK/bin"
for tool in bash env git grep; do
  ln -s "$(type -P "$tool")" "$WORK/bin/$tool"
done
check 2 PATH="$WORK/bin"
grep -q perl "$WORK/out" || fail "a missing perl was not named: $(cat "$WORK/out")"

mv "$REPO/SECURITY.md" "$WORK/SECURITY.md"
check 2
mv "$WORK/SECURITY.md" "$REPO/SECURITY.md"
check 0

if [ "$failures" -gt 0 ]; then
  exit 1
fi
echo "public copy check ok"
