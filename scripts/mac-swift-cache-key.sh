#!/usr/bin/env bash
# Usage: mac-swift-cache-key.sh
# Prints the cache key for native/.build: the runner, the Swift toolchain, the
# macOS SDK and the resolved packages, everything a release build's
# dependencies depend on.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
toolchain="$(xcrun swift --version 2>&1 | head -1 | shasum -a 256 | cut -c1-16)"
sdk="$(xcrun --sdk macosx --show-sdk-version)"
resolved="$(shasum -a 256 "$ROOT/native/Package.resolved" | cut -c1-16)"
echo "agenthail-mac-swift-release-$(uname -s)-$(uname -m)-$toolchain-sdk$sdk-$resolved"
