# Desktop parity: web dashboard vs Mac app

Goal (epic td-5f222f): the Mac app does everything the web dashboard does, so the web views can retire (td-5dc917) while the API stays.

Audited against `main` at 7734690. Web sources: `internal/daemon/dashboard/index.html` and `internal/daemon/dashboard/app.js`. Mac sources live in `native/macOS/` plus the shared `native/*.swift` files compiled into the `AgenthailMac` target (`native/Package.swift`). The installed app was also inspected read-only through its accessibility tree.

The web calls the unversioned routes and the Mac calls `/api/v1/*`. Each pair shares one handler (`internal/daemon/dashboard.go:253-263`, `internal/daemon/api_v1.go:40-60`, `internal/daemon/voice.go:41-43`), except the voice peer routes noted below. The Endpoint column names the web route; the Mac uses its twin:

| Web route | Mac (v1) route |
| --- | --- |
| GET /api/state | GET /api/v1/snapshot |
| POST /api/action | POST /api/v1/actions |
| GET /api/session | GET /api/v1/session |
| GET /api/session-metadata | GET /api/v1/session-metadata |
| GET /api/session-stream | GET /api/v1/session-stream |
| GET /api/session-attachment | GET /api/v1/session-attachment |
| GET /api/models | GET /api/v1/models |
| GET /api/search | GET /api/v1/search |
| GET /api/history | GET /api/v1/history |
| GET, POST /api/settings | GET, POST /api/v1/settings |
| GET /api/settings/remote-qr | none (the Mac draws the QR locally) |
| GET, POST /api/voice | GET, POST /api/v1/voice (same voice service) |
| GET /voice-peer.js (browser WebRTC script, `voicePeerScriptHandler`, internal/daemon/voice.go:152-161) | no twin. The Mac loads GET /api/v1/voice/peer instead, an authenticated HTML page with its own handler (`voicePeerHandler`, internal/daemon/voice.go:163-177), in its WebKit bridge (native/VoiceAudioBridge.swift). The Mac reaches it over loopback HTTP with the dashboard token. iOS runs a native WebRTC peer under CallKit (native/iOS/NativeVoiceAudio.swift) and loads no page |
| GET /api/v1/session-options | same route |

Status: **present** means the Mac covers the behavior; **partial** means part of it is missing; **missing** means the Mac has none of it; **decided** means the Mac omits it on purpose; **n/a** means the behavior belongs to another client.

## Summary

| Status | Count |
| --- | --- |
| Present | 39 |
| Partial | 11 |
| Missing | 3 |
| Decided | 1 |
| n/a | 1 |

Open gaps are tracked as td stories under td-5f222f (listed at the end). Each one blocks td-5dc917.

## Overview and global

| Feature | Web | Endpoint | Mac | Mac evidence |
| --- | --- | --- | --- | --- |
| Daemon connection indicator, slow and offline states (the Mac adds a Start action) | index.html:36-38; app.js:458-465, 878-936 | GET /api/state | present | macOS/DesktopWindow.swift:498-535, 558-584 |
| Summary line: surfaces connected, agents working, messages waiting, cached-data warning | app.js:436-465 | GET /api/state | present | macOS/Overview.swift:77-95; macOS/OverviewSettings.swift:9-13 |
| Surface cards: health, Working, Current with window, Queued | app.js:466-497 | GET /api/state | present | macOS/OverviewSettings.swift:30-95; macOS/Overview.swift:31-47 |
| Surface card opens the list filtered to that surface | app.js:1582-1595 | GET /api/state (filtered client-side) | partial | Mac surface cards are not clickable (macOS/OverviewSettings.swift:30-95); the same filter is set by hand in macOS/SessionRefinementMenu.swift:13-17 |
| Current conversations list | app.js scopedSessions | GET /api/state | present | SessionTree.swift includes; macOS/DesktopWindow.swift SessionSidebar |
| Needs attention: reason, requested action, Retry and Cancel | index.html:62-70; app.js:517-524, 1602-1639 | GET /api/state `attention`; POST /api/action `queue-retry`, `queue-cancel` | partial | Only `sessionId` is read, to mark the session "Needs you": native/AgenthailModel.swift:87. Reason and requested action are never shown. Retry and Cancel exist only in Settings > Delivery: macOS/DesktopSettings.swift:277-280 |
| Delivery problems with Dismiss | index.html:71-74; app.js:527-535, 1603-1616 | GET /api/state `deliveryProblems`; POST /api/action `delivery-dismiss` | present | macOS/DesktopWindow.swift:1414-1445 (banner, Edit and resend, Dismiss), 145-152 (Needs you) |
| Manual refresh button and "Synced" time | index.html:38-46; app.js:875-876, 1980-1982 | GET /api/state?fresh=1 | missing | `refresh(fresh:)` (native/AgenthailModel.swift:163) runs on connect, on the status timer, on stream events and after actions (native/AgenthailModel.swift:144, 813, 831), but no menu item, button or shortcut triggers it (no match for "Refresh" in macOS/CommandPalette.swift, macOS/SessionCommands.swift, native/AgenthailApp.swift) |
| Talk to orchestrator by voice: call, transfer, stop the routed session's turn, hang up | index.html:33-35, 498-521; app.js:1326-1442, 1983-1992 | GET, POST /api/voice `select`, `prepare`, `start`, `connected`, `transfer`, `target-interrupt`, `stop`; /voice-peer.js | present | The Voice window (macOS/VoiceWindow.swift, `VoiceCallView`) opens from the sidebar footer, the menu bar item and Session > Talk to Orchestrator (macOS/DesktopWindow.swift `ConnectionFooter`, native/AgenthailApp.swift, macOS/SessionCommands.swift). The call flow is shared with iOS in native/VoiceOperatorModel.swift over native/VoiceAPI.swift. The web dashboard and the Mac app are separate callers, so each sees the other's call as occupied (internal/daemon/voice_test.go `TestVoiceDashboardAndMacAppAreSeparateCallers`) |
| Empty states: no surfaces, no current conversations, nothing selected, empty queue, outcomes, audit | index.html:241-248; app.js:497, 516, 618, 670, 692, 817 | GET /api/state, GET /api/history | present | macOS/OverviewSettings.swift:16-17, 113; macOS/DesktopWindow.swift:179-184, 558-584; macOS/DesktopSettings.swift:261-263, 402-404 |
| Empty states: no saved exchanges, no channels, no handoffs | app.js:699, 707, 1167 | GET /api/session, GET /api/state | missing | The transcript has no empty branch (macOS/DesktopWindow.swift:684-705); channel and handoff lists render nothing when empty (macOS/DesktopSettings.swift:285-301, 323-336) |
| Error toasts, with network failures rewritten as restart and refresh guidance | app.js:321-334 | none | partial | Errors show verbatim from `error.localizedDescription` (native/AgenthailModel.swift:384, 395; macOS/DesktopSettings.swift:30-41; macOS/DesktopWindow.swift:1306-1310). The footer and empty pane offer Start when disconnected (macOS/DesktopWindow.swift:515-519, 578-580), but a failed action never says to reconnect |
| Settings load failure | app.js:738-747 | GET /api/settings | present | `loadOperations` sets `operationError` (native/AgenthailModel.swift:399-407), shown by `SettingsError` (macOS/DesktopSettings.swift:30-41) on every Settings tab except Keyboard, which has no model (macOS/ShortcutBindings.swift:37-76) |
| Navigation between Overview, Conversations and Operations, including Open inbox and Browse all | index.html:27-31, 58-60, 89-91; app.js:335-357, 1596-1601 | none | present | The main window holds the session list and conversation (macOS/DesktopWindow.swift:14-27); Overview, Delivery and Activity are Settings tabs reached from the footer or Command-comma (macOS/DesktopSettings.swift:9-24; macOS/DesktopWindow.swift:522-527) |
| Phone-width layout with a Back button | index.html:219-221; app.js:358-368, 1897-1902 | none | n/a | Phone use is served by the iOS app, not the Mac |

## Conversations

| Feature | Web | Endpoint | Mac | Mac evidence |
| --- | --- | --- | --- | --- |
| Session scopes and projects | index.html Conversation scope; app.js renderSessions, renderProjects | GET /api/state (`counts`, `projects`, row `current`/`archived`) | present (Running, Recent, All, Archived, grouped by project) | SessionTree.swift; macOS/DesktopWindow.swift SessionSidebar |
| Archive a session or project | app.js data-archive-action, data-project-action | POST /api/action `session-archive`, `project-archive` | present (context menus) | macOS/DesktopWindow.swift context menus; AgenthailModel.setArchived |
| Search, plus Codex history search from 3 characters | index.html:182-188; app.js:1726-1764 | GET /api/state (local match); GET /api/search (Codex history) | present | macOS/DesktopWindow.swift:114-131, 205-280; native/AgenthailModel.swift:323-346 |
| Surface and status filters | index.html:189-205; app.js:1878-1891 | GET /api/state (filtered client-side) | present | macOS/SessionRefinementMenu.swift:3-73 |
| Conversation count, filter summary, recent-exchange count | index.html:112; app.js:569, 587-594, 1113-1114 | GET /api/state; GET /api/session `exchanges`; GET /api/session-stream (rebuilds exchanges) | partial | Filter counts: macOS/DesktopWindow.swift:76; no exchange count or "Showing X of Y" summary |
| Conversation load failure | app.js:984-987 | GET /api/session | present | macOS/DesktopWindow.swift:764-770 |
| Show more | index.html:209-216; app.js:619-621, 1893-1896 | GET /api/state (paged client-side) | present (per project) | macOS/DesktopWindow.swift:166-175 |
| Session row: presence label, surface, queued count, shared badge | app.js:595-601 | GET /api/state | partial | macOS/DesktopWindow.swift:432-473 shows a status dot, subagents, shared badge and age, but no queued count, surface name or presence label (Working, Queued, Open, Recent) |
| New conversation form: surface, launcher, name, folder, model, approvals, Claude agent, worktree, permission mode, effort, mode, service tier, output schema, first message | index.html:118-168; app.js:1765-1877 | GET /api/state `surfaces`; GET /api/models; GET /api/v1/session-options; POST /api/action `session-create`, `notion-create` | partial on main | main has agent, launcher, folder and message only (native/AgenthailModel.swift:269-277). The full form is PR #39 (`feat/mac-parity-launch`, native/macOS/NewSessionSheet.swift:55-240 there), tracked by td-0ec020 |
| Chat header: surface, title, status, queued count, last active, model | app.js:427-432, 952-954, 1111 | GET /api/state (status, queueCount, lastActive); GET /api/session `model` | partial | macOS/DesktopWindow.swift:615-672 shows title, alias, status, surface, model and context, but no queued count or last active |
| Open session in its host | app.js:1080-1081 | GET /api/state `runtime.focusable`; POST /api/action `session-focus` | present | macOS/DesktopWindow.swift:647-652; native/AgenthailModel.swift:319-321 |
| Stop, Steer, Compact | app.js:1082-1091, 1443-1459 | GET /api/session `capabilities`; POST /api/action `interrupt`, `steer`, `compact` | present | macOS/DesktopWindow.swift:1268-1283, 1711-1715; macOS/SessionPane.swift:251-253, 350-352 |
| Slash commands, including the /model picker | app.js:1095-1102, 1464-1476, 1504-1521 | GET /api/session `models`; POST /api/action `alias`, `compact`, `interrupt`, `steer`, `model`, `goal-set` | present | macOS/SlashCommand.swift:3-76; macOS/DesktopWindow.swift:1343 |
| Per-session drafts, read-only state, busy hint | app.js:297-320, 942-962 | GET /api/state `readOnly`, `status`; GET /api/session `readOnly`, `capabilities` | present | macOS/DesktopWindow.swift:1204-1213, 1260-1266; native/AgenthailModel.swift:214 |
| Send with Queued/Sent status | app.js:1477-1495 | POST /api/action `send` | present | native/AgenthailModel.swift:543-575 |
| Transcript markdown, full-length replies, message Copy and Edit | app.js `renderMessage` | GET /api/session?timeline=1; GET /api/session-stream | present | Selectable markdown replies render in full with Copy; your own messages have Copy and Edit, user and peer bubbles collapse past 8 lines: macOS/DesktopWindow.swift `UserBubble`, `AssistantReply`; macOS/MessageActions.swift |
| Tool calls, results and reasoning, with Copy output | app.js:1043-1048, 1523-1532 | GET /api/session?timeline=1; GET /api/session-stream | present | macOS/DesktopWindow.swift:976-1175 |
| Peer messages labeled "From sender" | app.js:1035-1037 | GET /api/session?timeline=1; GET /api/session-stream | present | macOS/TranscriptBlock.swift:34-35; macOS/DesktopWindow.swift:937-948 |
| Image attachments | app.js:997-1004 | GET /api/session timeline `attachment`; GET /api/session-stream `attachment`; GET /api/session-attachment | present | macOS/DesktopWindow.swift:880-935 |
| Timeline item details: timestamp, call id, status, truncation reason, body reference | app.js:1014-1026 | GET /api/session?timeline=1; GET /api/session-stream | partial | Only the truncated flag and tool failure state show: macOS/DesktopWindow.swift:982-993, 1144-1151, 1167-1171; event items show title and status: macOS/TranscriptBlock.swift:53-57 |
| Live updates, in-progress turn and transcript read errors | app.js:206-221, 240-287 | GET /api/session (`journalSeq`, `readError`); GET /api/session-stream | present | Each stream event reloads the timeline within 250 ms (macOS/SessionPane.swift:403-417, 437-465); Working indicator macOS/DesktopWindow.swift:703-710; read errors 692-695 |
| Context usage and token breakdown | app.js:1205-1251 | GET /api/session `context`; GET /api/session-metadata; GET /api/session-stream `context` | present | macOS/DesktopWindow.swift:1644-1672; macOS/ContextBreakdown.swift |
| Goal: set, edit, pause, resume, clear, budget, metrics, needs-you status | app.js:1118-1127, 1190-1204, 1678-1689 | GET /api/session-metadata and stream `goal`; POST /api/action `goal-set`, `goal-edit`, `goal-pause`, `goal-resume`, `goal-clear`, `goal-budget` | present | macOS/GoalSection.swift:14-66, 120-145 |
| Conversation name | app.js:1117, 1678-1681 | GET /api/state `alias`; POST /api/action `alias` | present | macOS/RenameSessionSheet.swift; macOS/DesktopWindow.swift:356, 1681 |
| Observed Claude runs and subagents | app.js:1068-1075 | GET /api/session-metadata | present | macOS/ClaudeObservations.swift:3; macOS/SubagentsSection.swift |
| Call this session by voice | app.js:1130, 1580-1581 | GET, POST /api/voice `select` then the call flow above; GET /voice-peer.js | present | The waveform button in the conversation header and Session > Call This Session open the Voice window routed to that session (macOS/DesktopWindow.swift `ConversationHeader`, macOS/SessionCommands.swift, macOS/VoiceController.swift) |
| Codex turn effort and mode | index.html:251-256; app.js:2013-2018 | POST /api/action `send` | present | macOS/TurnSettingsMenu.swift:18-31 |
| Codex per-turn service tier and output schema | index.html:254-255; app.js:2016 | POST /api/action `send` | decided | Dropped from the Mac composer in d68aada8; native/tests/session_operations_test.swift:158 asserts sends carry effort and mode only |
| Fork, Codex native queue, Claude background lifecycle | index.html:257-267; app.js:2019-2056 | POST /api/action `session-fork`, `native-queue`, `session-lifecycle-status`, `-logs`, `-stop`, `-resume` | present | macOS/SessionOperationsSection.swift:4-290; macOS/SessionOperations.swift |

## Operations

| Feature | Web | Endpoint | Mac | Mac evidence |
| --- | --- | --- | --- | --- |
| Delivery queue: why each message waits, its last error, Retry for dead, Cancel for pending | index.html:341-350; app.js:640-670 | GET /api/state `queue`; POST /api/action `queue-retry`, `queue-cancel` | partial | macOS/DesktopSettings.swift:260-283 shows target, summary and raw status only, and offers Cancel for every status. The inspector labels every undelivered item "Queued": macOS/DesktopWindow.swift:1745, 1758 |
| Operations tab counts: Delivery, Handoffs, Audit (50+) | index.html:312-339; app.js:660-662 | GET /api/state | missing | Settings tabs carry no counts: macOS/DesktopSettings.swift:9-24 |
| Recent delivery outcomes | index.html:351-358; app.js:671-692 | GET /api/state `history` | present | macOS/OverviewSettings.swift:106-135; macOS/Overview.swift:97-147 |
| Channels: create, member list, add agent, send, delete (the Mac adds per-member Remove) | index.html:362-413; app.js:693-700, 1695-1725 | GET /api/state `channels`; POST /api/action `channel-create`, `channel-add`, `channel-send`, `channel-delete` | present | macOS/DesktopSettings.swift:323-368 |
| Automatic handoffs: add, remove, state | index.html:414-444; app.js:701-707 | GET /api/state `relays`; POST /api/action `relay-add`, `relay-remove` | present | macOS/DesktopSettings.swift:285-322, 374-380 |
| Audit: search, event type filter, load older | index.html:447-463; app.js:806-861, 1970-1979 | GET /api/history | present | macOS/DesktopSettings.swift:383-428; native/AgenthailModel.swift:410-422 |
| Audit event detail: sender to receiver route and full content | app.js:812-817 | GET /api/history | partial | macOS/DesktopSettings.swift:405-425 shows target only, the message cut to 2 lines and the error to 3, with no way to expand |
| Surface health, remediation, Repair | index.html:466-472; app.js:765-787, 1912-1926 | GET /api/state `surfaces`; POST /api/action surface `repairAction` | present | macOS/OverviewSettings.swift:30-95; macOS/Overview.swift:57-74; native/AgenthailModels.swift:92-101 |
| Phone access: enable, disable, QR reveal with auto-hide, copy link | index.html:473-481; app.js:748-800, 1927-1955 | GET /api/settings `remoteAccess`; POST /api/settings `remote-enable`, `remote-disable`; GET /api/settings/remote-qr | present (QR made locally) | macOS/DesktopSettings.swift:115-133; macOS/OverviewSettings.swift:137-191 |
| Codex recent window and busy delivery | index.html:487-491; app.js:1960-1969 | GET /api/settings `dashboard`; POST /api/settings `dashboard-config` | present | macOS/DesktopSettings.swift:68-91 |
| Local listener | index.html:492 | GET /api/settings | present | macOS/DesktopSettings.swift:58-62 |

## iOS where web and Mac differ

- Voice: iOS adds typed messages, a new orchestrator conversation and an inline activity timeline to the shared call (native/iOS/VoiceOperator.swift), and runs the call as a CallKit system call that continues in the background (native/iOS/NativeVoiceAudio.swift); Simulator builds activate the audio session directly because the Simulator refuses CallKit. The Mac matches the web dialog and links to the full timeline.
- Manual refresh: iOS uses pull to refresh with `refresh(fresh: true)` (native/iOS/AgenthailIOSViews.swift:310, native/iOS/SessionControls.swift:125).
- Queue last error: iOS shows `lastError` on queued items (native/iOS/SessionControls.swift:156).

## Gap stories

| Story | Covers |
| --- | --- |
| td-0ec020 (existing, PR #39) | New-session form |
| td-ea7bbf | Needs attention reason and requested action; delivery queue reason, last error, status-correct Retry and Cancel; inspector status labels |
| td-006fa3 | Audit route and full content |
| td-98d5f4 | Manual refresh; surface card click-to-filter; session row queued count, surface and presence label; header queued count and last active; long assistant reply collapse; exchange count and filter summary; Operations tab counts; timeline item details; empty states for transcript, channels and handoffs; reconnect guidance on network errors |
