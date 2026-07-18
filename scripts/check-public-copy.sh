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
