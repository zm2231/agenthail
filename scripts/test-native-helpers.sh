#!/usr/bin/env bash
set -euo pipefail

owned=""
trap '[ -z "$owned" ] || rm -rf "$owned"' EXIT
stop() {
  local child
  for child in $(pgrep -P $$ || true); do kill -TERM -- "-$child" 2>/dev/null || kill -TERM "$child" 2>/dev/null || true; done
  exit "$1"
}
# bash skips the EXIT trap when a signal ends it, as a timeout or a closed session does.
trap 'stop 129' HUP
trap 'stop 130' INT
trap 'stop 143' TERM

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ -n "${1:-}" ]; then
  OUT="$1"
else
  # Private to this run; one assignment creates and records it, so a signal cannot fall between the two.
  owned="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agenthail-native-helpers.XXXXXX")"
  OUT="$owned"
fi
JOBS="${AGENTHAIL_TEST_JOBS:-$(sysctl -n hw.ncpu)}"

SOURCES=(
  AgenthailAPI.swift
  KeptDelivery.swift
  InboxMessage.swift
  Decisions.swift
  PeerEnvelope.swift
  TaggedResponses.swift
  AgenthailModel.swift
  AgenthailModelPaging.swift
  AgenthailModels.swift
  ComposerAttachment.swift
  ComposerTextSync.swift
  ComposerCompletion.swift
  DuplicateApplicationPolicy.swift
  EventRetryBackoff.swift
  ObservationWait.swift
  ObservedScope.swift
  SessionCells.swift
  ConnectionGrace.swift
  CatalogBatch.swift
  CatalogInbox.swift
  CatalogFeed.swift
  HolderPins.swift
  OperationsRefreshPolicy.swift
  RequestAnswer.swift
  RequestAnswerFailure.swift
  SessionEndTurn.swift
  QuestionPager.swift
  QuestionPagerDrafts.swift
  QuestionPagerView.swift
  QuestionPagerParts.swift
  ChipFlow.swift
  RequestCard.swift
  RequestNotifications.swift
  SessionSelection.swift
  ProjectActions.swift
  ProjectContents.swift
  ProjectNow.swift
  ScopePages.swift
  ScopeReader.swift
  SessionDisplayKind.swift
  SessionDetailState.swift
  SnapshotParts.swift
  ViewOrder.swift
  SessionTreeOrder.swift
  SessionAttention.swift
  OrderHold.swift
  CatalogNews.swift
  LeadReplacement.swift
  SessionRowTime.swift
  HostNames.swift
  ClaudeRootLabels.swift
  SessionTree.swift
  MetadataOverlay.swift
  ClaudeSubagentList.swift
  SessionStore.swift
  StatusRefreshPolicy.swift
  SnapshotRefreshPolicy.swift
  ToolPresentation.swift
  TextDiff.swift
  ToolDiffCache.swift
  DiffPresentation.swift
  ToolRunSummary.swift
  TranscriptRows.swift
  TranscriptScrim.swift
  TranscriptScrimMask.swift
  TranscriptScrimRowLayers.swift
  TranscriptScrimRows.swift
  TranscriptTail.swift
  TranscriptRowID.swift
  AgentMessage.swift
  ClampedText.swift
  TurnClock.swift
  TimelineMetadata.swift
  TimelineEventLabel.swift
  TurnNoticePreference.swift
  LaunchSettings.swift
  SessionTurnSettings.swift
  QueueSteer.swift
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
  ProxyPrefer.swift
  macOS/ContextRingGauge.swift
  macOS/CatalogPosition.swift
  macOS/ComposerKeys.swift
  macOS/ComposerNSTextView.swift
  macOS/ComposerPaste.swift
  macOS/ContextBreakdown.swift
  macOS/LocalSend.swift
  macOS/MainWindowShow.swift
  macOS/Overview.swift
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
  macOS/MacDecisions.swift
  macOS/DecisionFeed.swift
  macOS/SidebarRow.swift
  macOS/StatusItemsState.swift
  macOS/StatusItems.swift
  macOS/ReadableButtonStyle.swift
  macOS/SessionRefinementMenu.swift
  macOS/SessionSidebarHeader.swift
  macOS/SessionTabs.swift
  macOS/ShortcutStore.swift
  macOS/SlashCommand.swift
  macOS/SnapshotBudget.swift
  macOS/SpotlightIndex.swift
  macOS/SpotlightCatalog.swift
  macOS/StatusStripFit.swift
  macOS/TranscriptBlock.swift
  macOS/TurnSettingConfirmation.swift
  macOS/TranscriptAgentBlocks.swift
  TranscriptEnd.swift
  macOS/TranscriptClip.swift
  macOS/TranscriptEndCheck.swift
  macOS/TranscriptFollow.swift
  macOS/TranscriptSelectionHold.swift
  macOS/TranscriptFeed.swift
  macOS/RevealedRow.swift
  macOS/TranscriptSlots.swift
  macOS/TranscriptRowAction.swift
  macOS/TranscriptScrimSource.swift
  macOS/TranscriptTrace.swift
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
  macOS/ScrimRows.swift
  macOS/ScrimRowsOrigin.swift
  macOS/ScrimText.swift
  macOS/ScrollScrim.swift
  macOS/SplitDividerHider.swift
  macOS/WindowBackdrop.swift
  macOS/WindowBackdropHost.swift
  macOS/WindowImage.swift
  macOS/WindowRectReader.swift
  macOS/WindowButtons.swift
  macOS/WindowButtonBand.swift
  macOS/WorkingSpinner.swift
)

# Helper tests run on a person's Mac: a window ordered on screen appears on their display.
shown=0
grep -nE '[oO]rderFront|[oO]rderBack|orderWindow|orderedIndex[[:space:]]*=[^=]|setIsVisible|showWindow|runModal|beginSheet|beginCriticalSheet|beginWithCompletionHandler|NSSavePanel|NSOpenPanel' "$ROOT"/native/tests/*_test.swift || shown=$?
if [ "$shown" -eq 0 ]; then
  echo "helper tests lay out windows without putting them on screen"
  exit 1
elif [ "$shown" -ne 1 ]; then
  echo "could not scan the helper tests for windows put on screen"
  exit 1
fi

[ -n "$owned" ] || rm -rf "$OUT"
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

# In the background and in its own process group, so a signal is handled at once and stops every worker.
set -m
xargs -0 -n1 -P "$JOBS" bash -c 'run_test "$1"' _ < <(printf '%s\0' "$ROOT"/native/tests/*_test.swift) &
set +m
wait $!
