#!/bin/bash
set -euo pipefail

public_docs=(README.md SECURITY.md docs/native-apps.md)
plain_docs=(README.md docs/native-apps.md)
public_ui=(internal/daemon/dashboard/index.html internal/daemon/dashboard/js/*.js native/AgenthailApp.swift native/macOS/DesktopWindow.swift native/macOS/DesktopSettings.swift native/macOS/CommandPalette.swift native/macOS/NewSession.swift native/iOS/AgenthailIOSViews.swift native/AgenthailAPI.swift)

for tool in git grep mktemp perl rm; do
  command -v "$tool" >/dev/null || {
    echo "$0: $tool is required" >&2
    exit 2
  }
done

# matches runs a search that exits 0 on a match and 1 on none. Any other exit
# means the search did not run, as with a missing file, so the check fails
# rather than reading it as clean.
matches() {
  local status=0
  "$@" || status=$?
  case "$status" in
    0) return 0 ;;
    1) return 1 ;;
  esac
  echo "$0: $1 could not search (exit $status)" >&2
  exit 2
}

if matches grep -n '—' "${public_docs[@]}" "${public_ui[@]}"; then
  echo "Public copy contains an em dash." >&2
  exit 1
fi

if matches grep -Ein 'sidecar|renderer bridge|bearer authentication|protocol compatibility|launchd|supervised|App Attest|Durable Object|Cloudflare Worker' "${plain_docs[@]}"; then
  echo "Public documentation contains maintainer terminology." >&2
  exit 1
fi

if matches grep -En 'The daemon is unavailable|Daemon unavailable|Restart Daemon|agenthail daemon start|local daemon|daemon activity|running without supervision|is supervised' "${public_ui[@]}"; then
  echo "Public UI contains internal service terminology." >&2
  exit 1
fi

public_source=(README.md SECURITY.md docs skills internal native push-relay scripts ci .github)
placeholder_users='demo|example|remote|agent|me|a|test|dev|fixture|runner|user|you'
placeholder_volumes='work|My Shared Files'

# machine_paths searches the public source git would publish: tracked files
# and new files it does not ignore, except this check and its test, which
# name machine paths on purpose. A guard's own case pattern for every
# volume, /Volumes/*), is not a machine path.
machine_paths() {
  local list status=0
  list="$(mktemp "${TMPDIR:-/tmp}/agenthail-public-copy.XXXXXX")"
  git ls-files -z --cached --others --exclude-standard -- "${public_source[@]}" ':!scripts/check-public-copy.sh' ':!scripts/test-check-public-copy.sh' >"$list" || status=$?
  if [ "$status" != 0 ] || [ ! -s "$list" ]; then
    rm -f "$list"
    echo "$0: git could not list the public source (exit $status)" >&2
    exit 2
  fi
  PLACEHOLDER_USERS="$placeholder_users" PLACEHOLDER_VOLUMES="$placeholder_volumes" perl -0 -e '
      my ($users, $volumes) = @ENV{qw(PLACEHOLDER_USERS PLACEHOLDER_VOLUMES)};
      my $pattern = qr{(?i:/(?:Users|home)/(?!(?:$users)(?![A-Za-z0-9._-]))[a-z][a-z0-9._-]*)|/Volumes/(?!(?:$volumes)(?:/|\x27|"|$)|\*\))|/persist/|\btail[0-9a-f]{4,}\.ts\.net|\b[A-Z][a-z]+(?:\x27s |\xe2\x80\x99s |s-)(?:Mac|MacBook|iPhone|iPad)\b|session_[[:alnum:]]{12,}|\b(?:forge-agents|mini-?2|vps2)\b|\bzains-};
      my $found = 0;
      while (my $file = <>) {
        chomp $file;
        die "$file: listed by git but missing from the worktree\n" unless -e $file;
        next if -d $file || -B $file;
        open(my $in, "<", $file) or die "$file: $!\n";
        local $/ = "\n";
        while (my $line = <$in>) {
          if ($line =~ $pattern) {
            print "$file:$.:$line";
            $found = 1;
          }
        }
        close $in;
      }
      exit($found ? 0 : 1);
    ' "$list" || status=$?
  rm -f "$list"
  return "$status"
}

if matches machine_paths; then
	echo "Public source contains a personal home path, machine path, machine name, tailnet name, device name, or session value." >&2
	exit 1
fi

if matches grep -En 'DEVELOPMENT_TEAM[[:space:]]*[:=][[:space:]]*[A-Z0-9]{10}|(Apple Distribution|Developer ID Application): [^(]+\([A-Z0-9]{10}\)|account_id[[:space:]]*=[[:space:]]*"[a-f0-9]+"' native/project.yml native/Agenthail.xcodeproj/project.pbxproj .github/workflows/*.yml push-relay/wrangler.toml; then
	echo "Public build configuration contains a maintainer account value." >&2
	exit 1
fi

