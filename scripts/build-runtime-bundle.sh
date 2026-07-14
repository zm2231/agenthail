#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT="${1:-$ROOT/dist/runtime-bundle}"
CACHE="${AGENTHAIL_BUILD_CACHE:-$HOME/Library/Caches/agenthail-build}"
PYTHON_ARCHIVE="cpython-3.13.14+20260623-aarch64-apple-darwin-install_only_stripped.tar.gz"
PYTHON_URL="https://github.com/astral-sh/python-build-standalone/releases/download/20260623/cpython-3.13.14%2B20260623-aarch64-apple-darwin-install_only_stripped.tar.gz"
PYTHON_SHA256="795a5aeeb050f00aa8a2214d779bad9f1b9113edb6923317a80c042a11a087d7"
NODE_ARCHIVE="node-v22.23.1-darwin-arm64.tar.gz"
NODE_URL="https://nodejs.org/dist/v22.23.1/$NODE_ARCHIVE"
NODE_SHA256="ef28d8fab2c0e4314522d4bb1b7173270aa3937e93b92cb7de79c112ac1fa953"

if [ "$(uname -s)" != "Darwin" ] || [ "$(uname -m)" != "arm64" ]; then
	echo "error: the bundled runtime currently targets Apple silicon macOS" >&2
	exit 1
fi

mkdir -p "$(dirname "$OUTPUT")"
OUTPUT="$(cd "$(dirname "$OUTPUT")" && pwd)/$(basename "$OUTPUT")"
mkdir -p "$CACHE"
download() {
	url="$1"
	path="$2"
	sha="$3"
	if [ ! -f "$path" ] || ! printf '%s  %s\n' "$sha" "$path" | shasum -a 256 -c - >/dev/null 2>&1; then
		rm -f "$path"
		curl -fL --retry 3 --retry-all-errors -o "$path" "$url"
	fi
	printf '%s  %s\n' "$sha" "$path" | shasum -a 256 -c -
}

download "$PYTHON_URL" "$CACHE/$PYTHON_ARCHIVE" "$PYTHON_SHA256"
download "$NODE_URL" "$CACHE/$NODE_ARCHIVE" "$NODE_SHA256"

rm -rf "$OUTPUT"
mkdir -p "$OUTPUT/runtime" "$OUTPUT/pydeps"
tar -xzf "$CACHE/$PYTHON_ARCHIVE" -C "$OUTPUT/runtime"
node_stage="$(mktemp -d)"
trap 'rm -rf "$node_stage"' EXIT
tar -xzf "$CACHE/$NODE_ARCHIVE" -C "$node_stage"
mv "$node_stage/node-v22.23.1-darwin-arm64" "$OUTPUT/runtime/node"

"$OUTPUT/runtime/python/bin/python3" -m pip install \
	--disable-pip-version-check \
	--no-compile \
	--only-binary=:all: \
	--require-hashes \
	--target "$OUTPUT/pydeps" \
	-r "$ROOT/packaging/runtime-requirements.txt"

cp "$ROOT/sidecar/package.json" "$ROOT/sidecar/package-lock.json" "$OUTPUT/"
PATH="$OUTPUT/runtime/node/bin:/usr/bin:/bin" "$OUTPUT/runtime/node/bin/npm" ci --omit=dev --prefix "$OUTPUT"
rm -rf "$OUTPUT/runtime/python/include" "$OUTPUT/runtime/python/share"
rm -rf "$OUTPUT/runtime/python/lib/python3.13/ensurepip" "$OUTPUT/runtime/python/lib/python3.13/site-packages/pip" "$OUTPUT/runtime/python/lib/python3.13/site-packages/pip-"*.dist-info
rm -rf "$OUTPUT/runtime/python/lib/tcl9" "$OUTPUT/runtime/python/lib/tcl9.0" "$OUTPUT/runtime/python/lib/tk9.0" "$OUTPUT/runtime/python/lib/itcl4.3.5" "$OUTPUT/runtime/python/lib/thread3.0.4"
rm -rf "$OUTPUT/runtime/python/lib/python3.13/idlelib" "$OUTPUT/runtime/python/lib/python3.13/tkinter" "$OUTPUT/runtime/python/lib/python3.13/turtledemo"
rm -f "$OUTPUT/runtime/python/lib/libtcl9.0.dylib" "$OUTPUT/runtime/python/lib/libtcl9tk9.0.dylib" "$OUTPUT/runtime/python/lib/python3.13/turtle.py" "$OUTPUT/runtime/python/lib/python3.13/lib-dynload/_tkinter.cpython-313-darwin.so"
rm -f "$OUTPUT/runtime/python/bin/idle3" "$OUTPUT/runtime/python/bin/idle3.13" "$OUTPUT/runtime/python/bin/pip" "$OUTPUT/runtime/python/bin/pip3" "$OUTPUT/runtime/python/bin/pip3.13" "$OUTPUT/runtime/python/bin/pydoc3" "$OUTPUT/runtime/python/bin/pydoc3.13" "$OUTPUT/runtime/python/bin/python3-config" "$OUTPUT/runtime/python/bin/python3.13-config"
rm -rf "$OUTPUT/runtime/node/include" "$OUTPUT/runtime/node/lib" "$OUTPUT/runtime/node/share"
rm -f "$OUTPUT/runtime/node/bin/corepack" "$OUTPUT/runtime/node/bin/npm" "$OUTPUT/runtime/node/bin/npx" "$OUTPUT/runtime/node/CHANGELOG.md" "$OUTPUT/runtime/node/README.md"
find "$OUTPUT" -type d -name __pycache__ -prune -exec rm -rf {} +
find "$OUTPUT" -name .DS_Store -delete

"$OUTPUT/runtime/python/bin/python3" --version
"$OUTPUT/runtime/node/bin/node" --version
PYTHONPATH="$OUTPUT/pydeps" "$OUTPUT/runtime/python/bin/python3" -c 'from curl_cffi import requests; print(requests.__name__)'
(cd "$OUTPUT" && PATH="$OUTPUT/runtime/node/bin:/usr/bin:/bin" "$OUTPUT/runtime/node/bin/node" -e "import('@steipete/sweet-cookie').then(() => console.log('sweet-cookie'))")
du -sh "$OUTPUT"
