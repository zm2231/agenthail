#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
swiftpm="${RUNNER_TEMP:?missing RUNNER_TEMP}/swiftpm"
packages=(-clonedSourcePackagesDirPath "$swiftpm/packages" -packageCachePath "$swiftpm/cache")

case "${1:-}" in
	resolve)
		attempts="${AGENTHAIL_DOWNLOAD_ATTEMPTS:-3}"
		backoff="${AGENTHAIL_DOWNLOAD_BACKOFF_SECONDS:-30}"
		for attempt in $(seq "$attempts"); do
			xcodebuild -resolvePackageDependencies -project "$root/native/Agenthail.xcodeproj" -scheme AgenthailIOS "${packages[@]}" && exit 0
			echo "ios-swiftpm: package resolution attempt $attempt of $attempts failed" >&2
			[ "$attempt" -lt "$attempts" ] && sleep $((backoff << (attempt - 1)))
		done
		exit 1
		;;
	xcodebuild)
		shift
		exec xcodebuild "$@" "${packages[@]}"
		;;
	*)
		echo "usage: $0 resolve | $0 xcodebuild ARGS..." >&2
		exit 2
		;;
esac
