#!/usr/bin/env bash
set -euo pipefail

min_free_gb=60
base_image="${BASE_IMAGE:-ghcr.io/cirruslabs/macos-tahoe-base:latest}"
xcode_app="${XCODE_APP:-/Applications/Xcode-27.app}"
runner_version=2.338.0
runner_sha256=df4cebda25c86a886ed204e49fee63f5c2e7cec5f447b5c98440a826bbdf9df2
scripts="$(cd "$(dirname "$0")/../../scripts" && pwd)"
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

free_gb() { df -g "$HOME" | awk 'NR == 2 { print $4 }'; }

if [ "$(free_gb)" -lt "$min_free_gb" ]; then
	echo "Refusing to build: $(free_gb) GB free, need $min_free_gb." >&2
	exit 1
fi

name="agenthail-ci-$(date +%Y%m%d)"
if tart list --source local --format json | jq -e --arg name "$name" 'any(.[]; .Name == $name)' >/dev/null; then
	name="$name-$(date +%H%M)"
fi
build="$name-building"

cleanup() {
	tart stop "$build" >/dev/null 2>&1 || true
	tart delete "$build" >/dev/null 2>&1 || true
}
trap cleanup EXIT

tart pull "$base_image"
tart clone "$base_image" "$build"
tart set "$build" --cpu 5 --memory 12288 --disk-size 80
tart run --no-graphics --dir="xcode:$xcode_app:ro" --dir="scripts:$scripts:ro" "$build" >/dev/null 2>&1 &
for _ in $(seq 1 90); do
	tart exec "$build" true >/dev/null 2>&1 && break
	sleep 2
done

tart exec "$build" /bin/bash -euo pipefail -c "
shared='/Volumes/My Shared Files'
yes | sudo diskutil repairDisk disk0 >/dev/null 2>&1 || true
sudo diskutil apfs resizeContainer disk0s2 0 >/dev/null 2>&1 || true
sudo rsync -a \"\$shared/xcode/\" /Applications/Xcode.app/
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept
sudo xcodebuild -runFirstLaunch
sudo pmset -a sleep 0 disksleep 0 displaysleep 0 powernap 0
sudo mdutil -a -i off >/dev/null
mkdir -p ~/actions-runner ~/actions-runner-hooks
curl -fsSL -o /tmp/runner.tgz https://github.com/actions/runner/releases/download/v$runner_version/actions-runner-osx-arm64-$runner_version.tar.gz
echo '$runner_sha256  /tmp/runner.tgz' | shasum -a 256 -c
tar xzf /tmp/runner.tgz -C ~/actions-runner
rm /tmp/runner.tgz
install -m 755 \"\$shared/scripts/ci-runner-job-started.sh\" ~/actions-runner-hooks/job-started.sh
printf 'LANG=en_US.UTF-8\nACTIONS_RUNNER_HOOK_JOB_STARTED=%s\n' ~/actions-runner-hooks/job-started.sh > ~/actions-runner/.env
echo /opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin > ~/actions-runner/.path
test \"\$(defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser)\" = admin
sudo -n true
xcodebuild -version
"

tart stop "$build"
tart rename "$build" "$name"
trap - EXIT

tart list --source local --format json |
	jq -r --arg keep "$name" '.[].Name | select(test("^agenthail-ci-[0-9]{8}(-[0-9]+)?$") and . != $keep)' |
	while read -r old; do
		tart delete "$old"
	done
repository="${base_image%%[:@]*}"
tart list --source oci --format json |
	jq -r --arg repository "$repository" '.[].Name | select(startswith($repository + ":") or startswith($repository + "@"))' |
	while read -r cached; do
		tart delete "$cached" 2>/dev/null ||
			! tart list --source oci --format json | jq -e --arg name "$cached" 'any(.[]; .Name == $name)' >/dev/null
	done

echo "Built $name. $(free_gb) GB free."
