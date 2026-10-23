#!/usr/bin/env bash
# The daemon runs Node only from ROOT/runtime/node/bin/node, never from PATH.
set -euo pipefail

VERSION=22.23.1

repo="$(cd "$(dirname "$0")/.." && pwd)"
dist_only=0
if [ "${1:-}" = "--dist" ]; then
	dist_only=1
	shift
fi
if [ $# -eq 0 ] && [ "$dist_only" -eq 0 ]; then
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	arch="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
	root="$repo"
elif [ $# -eq 2 ] && [ "$dist_only" -eq 1 ]; then
	os="$1" arch="$2" root=""
elif [ $# -eq 3 ] && [ "$dist_only" -eq 0 ]; then
	os="$1" arch="$2" root="$3"
else
	cat >&2 <<'USAGE'
usage: scripts/fetch-node.sh                 stage this machine's Node into the checkout
       scripts/fetch-node.sh OS ARCH ROOT    stage into ROOT/runtime/node (darwin|linux, arm64|amd64)
       scripts/fetch-node.sh --dist OS ARCH  print the verified distribution, npm included
USAGE
	exit 2
fi

case "$os-$arch" in
	darwin-arm64) platform=darwin-arm64 sha=ef28d8fab2c0e4314522d4bb1b7173270aa3937e93b92cb7de79c112ac1fa953 ;;
	linux-amd64) platform=linux-x64 sha=7a8cb04b4a1df4eaf432125324b81b29a088e73570a23259a8de1c65d07fc129 ;;
	linux-arm64) platform=linux-arm64 sha=543fa39e57d4c07855939459a323f4deb9a79dd1bb45e6e99458b0f2de10db8d ;;
	*)
		echo "error: Agenthail ships no Node runtime for $os-$arch" >&2
		exit 1
		;;
esac

if [ "$(uname -s)" = Darwin ]; then
	cache="${AGENTHAIL_BUILD_CACHE:-$HOME/Library/Caches/agenthail-build}"
else
	cache="${AGENTHAIL_BUILD_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/agenthail-build}"
fi
name="node-v$VERSION-$platform"
archive="$cache/$name.tar.gz"
dist="$cache/$name"

checksum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

mkdir -p "$cache"
if [ ! -x "$dist/bin/node" ]; then
	if [ ! -f "$archive" ] || [ "$(checksum "$archive")" != "$sha" ]; then
		rm -f "$archive"
		curl -fsSL --retry 3 --retry-all-errors -o "$archive.part" "https://nodejs.org/dist/v$VERSION/$name.tar.gz"
		mv "$archive.part" "$archive"
	fi
	if [ "$(checksum "$archive")" != "$sha" ]; then
		echo "error: $archive does not match the pinned checksum" >&2
		exit 1
	fi
	unpacked="$(mktemp -d "$cache/unpack.XXXXXX")"
	tar -xzf "$archive" -C "$unpacked"
	rm -rf "$dist"
	mv "$unpacked/$name" "$dist"
	rmdir "$unpacked"
fi

if [ "$dist_only" -eq 1 ]; then
	echo "$dist"
	exit 0
fi
rm -rf "$root/runtime/node"
mkdir -p "$root/runtime/node/bin"
cp "$dist/bin/node" "$root/runtime/node/bin/node"
cp "$dist/LICENSE" "$root/runtime/node/LICENSE"
chmod 0755 "$root/runtime/node/bin/node"
