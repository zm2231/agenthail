#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ -n "${1:-}" ]; then
  OUT="$1"
else
  # A private directory, so concurrent runs from other checkouts cannot
  # rebuild the library under this one.
  OUT="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agenthail-native-helpers.XXXXXX")"
  trap 'rm -rf "$OUT"' EXIT
fi
JOBS="${AGENTHAIL_TEST_JOBS:-$(sysctl -n hw.ncpu)}"

SOURCES=(
  AgenthailAPI.swift
  AgenthailModel.swift
  AgenthailModels.swift
  ComposerAttachment.swift
  ComposerTextSync.swift
  DuplicateApplicationPolicy.swift
  EventRetryBackoff.swift
  HolderPins.swift
  OperationsRefreshPolicy.swift
  RequestAnswer.swift
  RequestCard.swift
  RequestNotifications.swift
  SessionSelection.swift
  SessionTree.swift
  MetadataOverlay.swift
  StatusRefreshPolicy.swift
  ToolPresentation.swift
  TurnSettings.swift
  UIConfig.swift
  PaletteTokens.swift
  VoiceAPI.swift
  VoiceAudioBridge.swift
  VoiceCallDiagnostics.swift
  VoiceOperatorModel.swift
  macOS/AccentContrast.swift
  macOS/AgenthailLink.swift
  macOS/AgenthailProcess.swift
  macOS/AuditFacts.swift
  macOS/DesktopPalette.swift
  macOS/InterfaceStore.swift
  Backdrop.swift
  macOS/BoundedDock.swift
  macOS/CatalogPosition.swift
  macOS/ComposerKeys.swift
  macOS/ComposerNSTextView.swift
  macOS/ComposerPaste.swift
  macOS/ContextBreakdown.swift
  macOS/LocalSend.swift
  macOS/Overview.swift
  macOS/PeerEnvelope.swift
  macOS/QueueFacts.swift
  ScrimField.swift
  macOS/SessionFacts.swift
  macOS/SessionHandle.swift
  macOS/SessionLaunch.swift
  macOS/SessionOperationControllers.swift
  macOS/SessionOperations.swift
  macOS/NewSessionDraft.swift
  macOS/SessionPane.swift
  macOS/SessionTabs.swift
  macOS/ShortcutStore.swift
  macOS/SlashCommand.swift
  macOS/SpotlightIndex.swift
  macOS/ToolRunSummary.swift
  macOS/TranscriptBlock.swift
  macOS/TranscriptFollow.swift
  macOS/TranscriptRows.swift
  macOS/VoiceController.swift
)

rm -rf "$OUT"
mkdir -p "$OUT/logs"
swiftc -parse-as-library -emit-library -static -enable-testing \
  -module-name AgenthailHelpers \
  -emit-module -emit-module-path "$OUT/AgenthailHelpers.swiftmodule" \
  -o "$OUT/libAgenthailHelpers.a" \
  "${SOURCES[@]/#/$ROOT/native/}" 2>"$OUT/logs/AgenthailHelpers.log" || {
  cat "$OUT/logs/AgenthailHelpers.log"
  exit 1
}

run_test() {
  local test="$1" name
  name="$(basename "$test" .swift)"
  if swiftc -parse-as-library -I "$OUT" -L "$OUT" -lAgenthailHelpers "$test" -o "$OUT/$name" >"$OUT/logs/$name.log" 2>&1 &&
    "$OUT/$name" >>"$OUT/logs/$name.log" 2>&1; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    sed 's/^/     /' "$OUT/logs/$name.log"
    return 1
  fi
}
export -f run_test
export OUT

printf '%s\0' "$ROOT"/native/tests/*_test.swift | xargs -0 -n1 -P "$JOBS" bash -c 'run_test "$1"' _
