#!/usr/bin/env bash
set -euo pipefail

min_free_gb=60
base_image="${BASE_IMAGE:-ghcr.io/cirruslabs/macos-tahoe-base:latest}"
xcode_app="${XCODE_APP:-/Applications/Xcode-27.app}"
runner_version=2.338.0
runner_sha256=df4cebda25c86a886ed204e49fee63f5c2e7cec5f447b5c98440a826bbdf9df2
hooks="${AGENTHAIL_CI_HOOKS_DIR:-$HOME/actions-runner-agenthail-hooks}"
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

free_gb() { df -g "$HOME" | awk 'NR == 2 { print $4 }'; }

hook_sha="$(shasum -a 256 "$hooks/job-started.sh" | cut -d' ' -f1)"
[ -n "$hook_sha" ] || { echo "No job-started hook at $hooks/job-started.sh." >&2; exit 1; }

if [ "$(free_gb)" -lt "$min_free_gb" ]; then
	echo "Refusing to build: $(free_gb) GB free, need $min_free_gb." >&2
	exit 1
fi

name="agenthail-ci-$(date +%Y%m%d)"
if tart list --source local --format json | jq -e --arg name "$name" 'any(.[]; .Name == $name)' >/dev/null; then
	name="$name-$(date +%H%M)"
fi
build="$name-building"
check="$name-checking"

discard() {
	tart stop "$1" >/dev/null 2>&1 || true
	tart delete "$1" >/dev/null 2>&1 || true
}

boot() {
	tart run --no-graphics "$@" >/dev/null 2>&1 &
	for _ in $(seq 1 90); do
		tart exec "${@: -1}" true >/dev/null 2>&1 && return
		sleep 2
	done
	tart exec "${@: -1}" true >/dev/null
}

cleanup() {
	discard "$check"
	discard "$build"
}
trap cleanup EXIT

tart pull "$base_image"
tart clone "$base_image" "$build"
tart set "$build" --cpu 5 --memory 12288 --disk-size 80
boot --dir="xcode:$xcode_app:ro" "$build"

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
networksetup -listallnetworkservices | tail -n +2 | while read -r service; do sudo networksetup -setdnsservers \"\$service\" 1.1.1.1 8.8.8.8; done
mkdir -p ~/actions-runner
curl -fsSL -o /tmp/runner.tgz https://github.com/actions/runner/releases/download/v$runner_version/actions-runner-osx-arm64-$runner_version.tar.gz
echo '$runner_sha256  /tmp/runner.tgz' | shasum -a 256 -c
tar xzf /tmp/runner.tgz -C ~/actions-runner
rm /tmp/runner.tgz
ln -s \"\$shared/hooks\" ~/actions-runner-hooks
printf 'LANG=en_US.UTF-8\nACTIONS_RUNNER_HOOK_JOB_STARTED=%s\n' ~/actions-runner-hooks/job-started.sh > ~/actions-runner/.env
echo /opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin > ~/actions-runner/.path
test \"\$(defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser)\" = admin
sudo -n true
xcodebuild -version
sync
"
tart stop "$build"

tart clone "$build" "$check"
boot --dir="hooks:$hooks:ro" "$check"
if ! tart exec "$check" /bin/bash -uo pipefail -c "
fail() { echo \"image check failed: \$1\" >&2; exit 1; }
test -s ~/actions-runner/.path || fail 'runner PATH file'
grep -qx \"ACTIONS_RUNNER_HOOK_JOB_STARTED=\$HOME/actions-runner-hooks/job-started.sh\" ~/actions-runner/.env || fail 'runner .env hook setting'
test \"\$(readlink ~/actions-runner-hooks)\" = '/Volumes/My Shared Files/hooks' || fail 'hooks link to the shared hooks directory'
test \"\$(shasum -a 256 ~/actions-runner-hooks/job-started.sh | cut -d' ' -f1)\" = $hook_sha || fail 'job-started hook in the guest matching the host copy'
test -x ~/actions-runner/run.sh || fail 'runner'
export PATH=\"\$(cat ~/actions-runner/.path)\"
test \"\$(~/actions-runner/config.sh --version)\" = $runner_version || fail 'runner version'
python3 -c 'import sys; sys.exit(sys.version_info < (3, 10))' || fail 'python3 3.10 or newer'
xcodebuild -version >/dev/null || fail 'xcodebuild'
networksetup -listallnetworkservices | tail -n +2 | while read -r service; do networksetup -getdnsservers \"\$service\" | head -1 | grep -qx 1.1.1.1 || fail \"public DNS resolver for \$service\"; done || exit 1
"; then
	echo "The new image failed its checks in a fresh clone; not publishing it." >&2
	exit 1
fi
discard "$check"
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
