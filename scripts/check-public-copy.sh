#!/bin/bash
set -euo pipefail

public_docs=(README.md SECURITY.md docs/native-apps.md)
plain_docs=(README.md docs/native-apps.md)
public_ui=(internal/daemon/dashboard/index.html internal/daemon/dashboard/app.js native/AgenthailApp.swift native/macOS/DesktopWindow.swift native/macOS/DesktopSettings.swift native/macOS/CommandPalette.swift native/macOS/NewSessionSheet.swift native/iOS/AgenthailIOSViews.swift native/AgenthailAPI.swift)

if grep -n '—' "${public_docs[@]}" "${public_ui[@]}"; then
  echo "Public copy contains an em dash." >&2
  exit 1
fi

if grep -Ein 'sidecar|renderer bridge|bearer authentication|protocol compatibility|launchd|supervised|App Attest|Durable Object|Cloudflare Worker' "${plain_docs[@]}"; then
  echo "Public documentation contains maintainer terminology." >&2
  exit 1
fi

if grep -En 'The daemon is unavailable|Daemon unavailable|Restart Daemon|agenthail daemon start|local daemon|daemon activity|running without supervision|is supervised' "${public_ui[@]}"; then
  echo "Public UI contains internal service terminology." >&2
  exit 1
fi

public_source=(README.md SECURITY.md docs skills internal native push-relay scripts ci .github)
placeholder_users='demo|example|remote|agent|me|a|test|dev|fixture|runner|user|you'

if rg -n -P "/(Users|home)/(?!(${placeholder_users})(/|\\b))[a-z][a-z0-9._-]*|/persist/|\\btail[0-9a-f]{4,}\\.ts\\.net|\\b[A-Z][a-z]+(['’]s |s-)(Mac|MacBook|iPhone|iPad)\\b|session_[[:alnum:]]{12,}" "${public_source[@]}" --glob '!scripts/check-public-copy.sh'; then
	echo "Public source contains a personal home path, machine path, tailnet name, device name, or session value." >&2
	exit 1
fi

if grep -En 'DEVELOPMENT_TEAM[[:space:]]*[:=][[:space:]]*[A-Z0-9]{10}|(Apple Distribution|Developer ID Application): [^(]+\([A-Z0-9]{10}\)|account_id[[:space:]]*=[[:space:]]*"[a-f0-9]+"' native/project.yml native/Agenthail.xcodeproj/project.pbxproj .github/workflows/*.yml push-relay/wrangler.toml; then
	echo "Public build configuration contains a maintainer account value." >&2
	exit 1
fi

