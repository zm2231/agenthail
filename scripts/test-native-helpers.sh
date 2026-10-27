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
  ComposerCompletion.swift
  DuplicateApplicationPolicy.swift
  EventRetryBackoff.swift
  CatalogBatch.swift
  CatalogInbox.swift
  CatalogFeed.swift
  HolderPins.swift
  OperationsRefreshPolicy.swift
  RequestAnswer.swift
  QuestionPager.swift
  QuestionPagerDrafts.swift
  QuestionPagerView.swift
  RequestCard.swift
  RequestNotifications.swift
  SessionSelection.swift
  ProjectActions.swift
  HostNames.swift
  ClaudeRootLabels.swift
  SessionTree.swift
  MetadataOverlay.swift
  ClaudeSubagentList.swift
  SessionStore.swift
  StatusRefreshPolicy.swift
  ToolPresentation.swift
  TextDiff.swift
  ToolDiffCache.swift
  DiffPresentation.swift
  ToolRunSummary.swift
  TranscriptRows.swift
  AgentMessage.swift
  ClampedText.swift
  TurnClock.swift
  TimelineMetadata.swift
  TimelineEventLabel.swift
  TurnSettings.swift
  UIConfig.swift
  SurfaceMode.swift
  PaletteTokens.swift
  TextColors.swift
  RoleStyle.swift
  VoiceAPI.swift
  VoiceAudioBridge.swift
  VoiceCallDiagnostics.swift
  VoiceOperatorModel.swift
  macOS/AccentContrast.swift
  macOS/DesktopStyle.swift
  macOS/AppearanceDraft.swift
  macOS/AgenthailLink.swift
  macOS/AgenthailProcess.swift
  macOS/AuditFacts.swift
  macOS/DesktopPalette.swift
  macOS/SelectionMark.swift
  macOS/InterfaceStore.swift
  Backdrop.swift
  BackdropLayers.swift
  macOS/BoundedDock.swift
  UsageMeterText.swift
  UsageAccounts.swift
  UsageProxies.swift
  macOS/ContextRingGauge.swift
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
  GlassSolve.swift
  macOS/DesktopGlass.swift
  macOS/SessionFacts.swift
  macOS/SessionHandle.swift
  macOS/SessionLaunch.swift
  macOS/SessionOperationControllers.swift
  macOS/SessionOperations.swift
  macOS/NewSessionDraft.swift
  macOS/NewSessionLabels.swift
  macOS/SessionPane.swift
  macOS/SessionRefinementMenu.swift
  macOS/SessionSidebarHeader.swift
  macOS/SessionTabs.swift
  macOS/ShortcutStore.swift
  macOS/SlashCommand.swift
  macOS/SnapshotBudget.swift
  macOS/SpotlightIndex.swift
  macOS/StatusStripFit.swift
  macOS/TranscriptBlock.swift
  macOS/TranscriptAgentBlocks.swift
  macOS/TranscriptFollow.swift
  macOS/TranscriptRowAction.swift
  macOS/VoiceController.swift
  macOS/AncestorFrames.swift
  macOS/BackdropLayerView.swift
  macOS/BackdropSurface.swift
  macOS/FloatingGlass.swift
  macOS/GlassPane.swift
  macOS/PaneFrame.swift
  macOS/GlassColumnMask.swift
  macOS/ScrimAnchor.swift
  macOS/ScrimMask.swift
  macOS/ScrimRegion.swift
  macOS/ScrimText.swift
  macOS/ScrollScrim.swift
  macOS/SplitDividerHider.swift
  macOS/WindowBackdrop.swift
  macOS/WindowBackdropHost.swift
  macOS/WindowImage.swift
  macOS/WindowRectReader.swift
  macOS/WindowButtons.swift
  macOS/WindowButtonBand.swift
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
  # Actor data-race checks trap when main-actor code runs off the main actor,
  # so a race in a test fails every run instead of crashing now and then.
  if swiftc -parse-as-library -Xfrontend -enable-actor-data-race-checks -I "$OUT" -L "$OUT" -lAgenthailHelpers "$test" -o "$OUT/$name" >"$OUT/logs/$name.log" 2>&1 &&
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
