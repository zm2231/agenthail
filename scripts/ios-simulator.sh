#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 [iphone|ipad] [--boot]" >&2
  echo "       $0 [iphone|ipad] run <command> [args...]" >&2
  exit 2
}

# guard_xcodebuild sets guarded to the command `run` executes. Derived data on
# an external volume hangs the test runner before it connects, and a failed
# test run otherwise leaves a log collection running in the simulator for
# hours, so an xcodebuild run refuses the one and turns off the other.
# xcodebuild started through a shell or another launcher cannot be checked and
# is refused.
guard_xcodebuild() {
  guarded=("$@")
  local first=0 arg derived="" testing="" diagnostics="" index value
  case "$(basename "$1")" in
    xcodebuild) first=1 ;;
    xcrun) [ "${2:-}" = xcodebuild ] && first=2 ;;
  esac
  if [ "$first" = 0 ]; then
    case "$(basename "$1")" in
      sh | bash | zsh | dash | ksh | env | nice | nohup | time | caffeinate | arch | sudo | xargs | xcrun)
        for arg in "${@:2}"; do
          case "$arg" in
            *xcodebuild*)
              echo "$0: run xcodebuild directly, not through $1, so its derived data and test diagnostics can be checked" >&2
              exit 2
              ;;
          esac
        done
        ;;
    esac
    return
  fi
  # Actions are bare words; an option's value is skipped so that, say, a
  # scheme named test is not read as the test action.
  index=$first
  while [ "$index" -le "$#" ]; do
    case "${!index}" in
      -derivedDataPath) value=$((index + 1)) && derived="${!value:-}" && index=$value ;;
      -collect-test-diagnostics) diagnostics=1 && index=$((index + 1)) ;;
      -*:* | -*=* | -quiet | -verbose | -json | -list | -usage | -help | -version | -license | -showsdks | -showBuildSettings | \
        -showdestinations | -showTestPlans | -allowProvisioningUpdates | -allowProvisioningDeviceRegistration | \
        -hideShellScriptEnvironment | -skipUnavailableActions | -skipPackagePluginValidation | -skipMacroValidation | \
        -skipPackageUpdates | -disableAutomaticPackageResolution | -onlyUsePackageVersionsFromResolvedFile | \
        -resolvePackageDependencies | -dry-run | -n) ;;
      -*) index=$((index + 1)) ;;
      test | test-without-building) testing=1 ;;
    esac
    index=$((index + 1))
  done
  [ -n "$derived" ] || derived="$(defaults read com.apple.dt.Xcode IDECustomDerivedDataLocation 2>/dev/null || true)"
  if [ -n "$derived" ]; then
    case "$derived" in
      /*) ;;
      *) derived="$PWD/$derived" ;;
    esac
    derived="$(physical_path "$derived")"
  fi
  case "$derived" in
    /Volumes/*)
      echo "$0: derived data $derived is on an external volume, where the test runner hangs before it connects; use ~/Library/Developer/Xcode/DerivedData/<name>" >&2
      exit 2
      ;;
  esac
  if [ -n "$testing" ] && [ -z "$diagnostics" ]; then
    guarded+=(-collect-test-diagnostics never)
  fi
}

# physical_path resolves links in the part of a path that exists; xcodebuild
# creates the rest.
physical_path() {
  local path="$1" rest="" base
  until [ -d "$path" ]; do
    rest="/$(basename "$path")$rest"
    path="$(dirname "$path")"
  done
  base="$(cd -P "$path" && pwd -P)"
  printf '%s%s\n' "${base%/}" "$rest"
}

kind="${1:-iphone}"
case "$kind" in
  iphone) name="Agenthail iPhone"; family="iPhone" ;;
  ipad) name="Agenthail iPad"; family="iPad" ;;
  *) usage ;;
esac
name="${AGENTHAIL_SIMULATOR_NAME:-$name}"

devices="$(xcrun simctl list devices available --json)"
udid="$(jq -r --arg name "$name" '[.devices[][] | select(.name == $name)] | first | .udid // empty' <<<"$devices")"

if [ -z "$udid" ]; then
  runtime="$(xcrun simctl list runtimes --json | jq -r '.runtimes | map(select(.isAvailable and (.identifier | contains("iOS")))) | last | .identifier')"
  device="$(xcrun simctl list devicetypes --json | jq -r --arg family "$family" '.devicetypes | map(select(.name | startswith($family))) | first | .identifier')"
  udid="$(xcrun simctl create "$name" "$device" "$runtime")"
fi

case "${2:-}" in
  "") ;;
  --boot)
    xcrun simctl boot "$udid" 2>/dev/null || true
    xcrun simctl bootstatus "$udid" -b >/dev/null
    ;;
  run)
    [ "$#" -ge 3 ] || usage
    shift 2
    guard_xcodebuild "$@"
    set -- "${guarded[@]}"
    # Fixed path, not TMPDIR: every session on this machine must contend for the same lock.
    lock="/tmp/agenthail-simulator-$udid.lock"
    xcrun simctl boot "$udid" 2>/dev/null || true
    xcrun simctl bootstatus "$udid" -b >/dev/null
    AGENTHAIL_SIMULATOR_UDID="$udid" exec lockf -k "$lock" "$@"
    ;;
  *) usage ;;
esac

echo "$udid"
