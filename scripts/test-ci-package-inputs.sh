#!/usr/bin/env bash
# Checks which changes run the package job, and that every file the job's scripts name is one of its inputs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agenthail-package-inputs.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
failures=0
fail() {
	echo "FAIL: $*"
	failures=$((failures + 1))
}

repo="$WORK/repo"
git init -q -b main "$repo"
mkdir -p "$repo/scripts"
cp "$ROOT/scripts/ci-package-inputs.sh" "$ROOT/scripts/package-inputs.txt" "$repo/scripts/"
git -C "$repo" add -A
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -m base
git clone -q --bare "$repo" "$WORK/origin"
git -C "$repo" remote add origin "$WORK/origin"
base="$(git -C "$repo" rev-parse HEAD)"

# check <name> <want> <event> <head ref> <stacked> <stack base> <changed path>...
check() {
	local name="$1" want="$2" event="$3" head="$4" stacked="$5" stack_base="$6"
	shift 6
	git -C "$repo" checkout -q --detach "$base"
	local path
	for path in "$@"; do
		mkdir -p "$repo/$(dirname "$path")"
		echo "$name" >>"$repo/$path"
	done
	git -C "$repo" add -A
	git -C "$repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "$name"
	rm -f "$WORK/out"
	(env GITHUB_OUTPUT="$WORK/out" EVENT="$event" HEAD_REF="$head" BASE="$base" STACKED="$stacked" STACK_BASE_REF="$stack_base" bash "$repo/scripts/ci-package-inputs.sh" >/dev/null 2>&1) || fail "$name: the script failed"
	local got
	got="$(cat "$WORK/out" 2>/dev/null || true)"
	[ "$got" = "package=$want" ] || fail "$name: got '$got', want package=$want"
}

check "a push runs it" true push "" false "" README.md
check "a merge train runs it" true pull_request train/1 false "" README.md
check "a docs change skips it" false pull_request feature false "" docs/guide.md native/README.md
check "an iOS-only change skips it" false pull_request feature false "" native/iOS/App.swift native/Agenthail.xcodeproj/project.pbxproj native/project.yml
check "a Go change outside the commands the install test runs skips it" false pull_request feature false "" internal/cli/run.go
check "a registry change runs it" true pull_request feature false "" internal/registry/migrate.go
check "a daemon control change runs it" true pull_request feature false "" internal/daemonctl/daemonctl.go
check "a document the package ships runs it" true pull_request feature false "" THIRD_PARTY_NOTICES.md
check "a Mac app change runs it" true pull_request feature false "" native/macOS/App.swift
check "a packaging change runs it" true pull_request feature false "" packaging/scripts/postinstall
check "a build script change runs it" true pull_request feature false "" scripts/build-pkg.sh
check "a change to the input list runs it" true pull_request feature false "" scripts/package-inputs.txt
check "a stack with no base runs it" true pull_request feature true "" docs/guide.md
check "a stack diffs against its base" false pull_request feature true main docs/guide.md

# Every tracked file the package job or a script it runs names, transitively, must be an input or excluded by a ! line; a directory stands for its files, and NOT_PATHS lists words such as `npm ci` that only look like one.
spec=() excluded=()
while read -r line; do
	[ -n "$line" ] || continue
	case "$line" in
	!*)
		spec+=(":(glob,exclude)${line#!}")
		excluded+=(":(glob)${line#!}")
		;;
	*) spec+=(":(glob)$line") ;;
	esac
done <"$ROOT/scripts/package-inputs.txt"
{
	git -C "$ROOT" ls-files -- "${spec[@]}"
	[ "${#excluded[@]}" -eq 0 ] || git -C "$ROOT" ls-files -- "${excluded[@]}"
} >"$WORK/covered"
git -C "$ROOT" ls-files >"$WORK/tracked"
NOT_PATHS="ci"
refs() {
	sed -E 's/(^|[[:space:]])#.*$//' |
		{ grep -oE '[A-Za-z0-9_.${}/-]+' || true; } |
		sed -E -e 's#^.*\$\{?[A-Za-z_]*ROOT\}?/##' -e 's#^\./##' -e 's#[}/]+$##' |
		sort -u |
		awk -v words="$NOT_PATHS" 'NR == FNR { tracked[$0] = 1; files[++n] = $0; next }
			$0 == "" || $0 == "." || index(" " words " ", " " $0 " ") { next }
			$0 in tracked { print; next }
			{ prefix = $0 "/"; for (i = 1; i <= n; i++) if (index(files[i], prefix) == 1) print files[i] }' "$WORK/tracked" -
}
queue=($(awk '/^  package:$/ {on=1; next} on && /^  [a-z]/ {exit} on' "$ROOT/.github/workflows/ci.yml" | refs))
[ "${#queue[@]}" -gt 0 ] || fail "found no files the package job runs"
seen=" "
while [ "${#queue[@]}" -gt 0 ]; do
	path="${queue[0]}"
	queue=("${queue[@]:1}")
	case "$seen" in *" $path "*) continue ;; esac
	seen+="$path "
	grep -qxF "$path" "$WORK/covered" || fail "the package job reads $path, which scripts/package-inputs.txt does not name"
	case "$path" in *.sh) queue+=($(refs <"$ROOT/$path")) ;; esac
done

[ "$failures" -eq 0 ] || exit 1
echo "ci-package-inputs ok"
