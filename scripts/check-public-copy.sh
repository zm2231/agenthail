#!/bin/bash
set -euo pipefail

public_docs=(README.md SECURITY.md docs/native-apps.md)
plain_docs=(README.md docs/native-apps.md)
public_ui=(internal/daemon/dashboard/index.html internal/daemon/dashboard/app.js native/AgenthailApp.swift native/AgenthailViews.swift native/iOS/AgenthailIOSViews.swift native/AgenthailAPI.swift)

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

if rg -n '/Users/zain|/Volumes/4|Zains-Mac|session_[[:alnum:]]{12,}|--from zain|Zain.s iPhone' README.md docs internal native push-relay scripts --glob '!scripts/check-public-copy.sh'; then
	echo "Public source contains a local identity, path, or session value." >&2
	exit 1
fi

if grep -En 'DEVELOPMENT_TEAM[[:space:]]*[:=][[:space:]]*[A-Z0-9]{10}|Apple Distribution: [^(]+\([A-Z0-9]{10}\)|account_id[[:space:]]*=[[:space:]]*"[a-f0-9]+"' native/project.yml native/Agenthail.xcodeproj/project.pbxproj .github/workflows/*.yml push-relay/wrangler.toml; then
	echo "Public build configuration contains a maintainer account value." >&2
	exit 1
fi

private_host='merchant''zains'
private_signer='ZAIN SOHAIL'' MERCHANT'
if rg -n "$private_host|$private_signer" README.md docs internal native push-relay .github scripts --glob '!scripts/check-public-copy.sh'; then
	echo "Public source contains a maintainer-branded service or signer name." >&2
	exit 1
fi
