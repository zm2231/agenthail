# Mobile session experience

September 12, 2026. This audit supersedes the September 7 local layout verdict. The physical-device audit reproduced a session-list tap that did not navigate and an empty nested turn-duration card. Passing model tests and inspecting a fixture had not covered that user path.

The [research comparison](../research/mobile-agent-clients-2026-09.md) records pinned source inspection for Happy, HAPI, Mimi, Agent Phone and Inkbox, plus the maintainer’s concrete ChatGPT Remote and Claude mobile references. [Native design](../../native/DESIGN.md) records the resulting interaction choices. The minimum version is deliberately iOS/iPadOS 18 for Textual 0.5.0.

## Product paths

| Need | Implementation path | Verification |
|---|---|---|
| Find and open work | Session registry and adapter discovery → snapshot/search `cwd` → workspace groups → explicit compact navigation or iPad selection | `mobile_workspace_test.go`, `ActivityPresentationTests`, `SessionNavigationTests` |
| Open from Inbox | Queue target → requested session identity → the main Sessions route | UI test opens an Inbox target, returns to Sessions, then opens another session |
| Understand tool activity | Local Claude/Codex JSONL → `TimelineProvider` → authenticated session API → semantic run summary → individual invocation and paired results | Parser tests, out-of-order parallel-result grouping test, UI expansion through recorded output |
| Read conversation content | Messages → Textual native Markdown, code/table rendering and system serif assistant prose | Phone/tablet visual inspection, public fixtures and local captured transcripts |
| Inspect raw sequence | Session menu → All events → original record order, identifiers and truncation | Grouping order checks and UI menu test |
| Read older work | Bounded reverse reader → byte cursor → API → history merge | Existing 450-record paging and model history tests; captures do not simulate uncaptured pages |
| Recover from history errors | Independent Tail and Timeline requests → sanitized warning → local activity or saved exchanges | `session_history_failure_test.go`; empty timeline with nonempty saved messages UI test |
| Explain lifecycle | Claude `durationMs` → rounded duration → quiet inline event | `session_duration_test.go`, including 114536 ms → 1m55s |
| Explain delivery | Queued receipt → queue ID → refreshed terminal state → Latest instruction receipt; all instructions remain in Inbox | `SessionRecoveryTests` covers reconnect, expiry, failed refresh and unknown delivery without resending |
| Separate current decisions from history | Persistent queue → all active rows plus latest 100 terminal rows → Inbox Current/History | `mobile_queue_history_test.go` retains older active rows across a terminal flood; native expiry/retry UI test |
| Start and control sessions | Existing capability/settings contracts → creation sheet, model composer, inspector and action API | Existing Claude/Codex/Notion creation and workflow parity tests |

Sessions, Inbox and Settings have distinct jobs. Sessions groups by reported workspace and offers Running, Recent and All. Recent follows the host’s current-session catalog; it is not a chronological “today” feed. Inbox contains delivery work and history. A timeout is historical unless an uncertain delivery still requires a decision.

Tool runs do not cross message or lifecycle boundaries. A collapsed summary can read “3 commands, 2 file reads (1 failed)”. Individual calls retain the original input, full recorded output and IDs. “No result yet” does not assert execution is still running. All events preserves raw order rather than the paired inspection order. Stop and steer are capability-derived; the composer does not call a steer operation “queue after this turn”.

## Actual-session inspection

The read-only host capture contained 90 sessions. The old API returned no activity or exchanges for the current Codex task after its Desktop history request timed out. Calling the new local transcript reader directly recovered **54 Codex records** in its bounded read window. The inspected Claude session returned **200 records**. These are captured local records, not evidence that a repaired daemon was installed or a live turn succeeded.

Debug-only `--preview-session --preview-capture` loads `Documents/AgenthailCapture.json` into the actual native views using an in-process URLProtocol. It serves captured snapshot, queue and selected session responses; unrecorded requests, older pages and actions fail explicitly. Private captures remain in ignored build output and simulator data, never source control. Public fixtures exercise chat, creation, Inbox and the iPad sidebar without pairing. Release excludes these paths.

## Verification receipts

Repair receipts live in ignored `build/`: native result bundles, `go-final.log`, `vet-final.log`, `race-final.log`, `macos-final.log`, `web-controls-final.log`, graph analysis and screenshots. Earlier September 7 receipts remain historical and are not evidence of current physical-phone quality. The signed final native receipt is `repair-tests-8.xcresult` / `ios-tests-8.log`: 28 native tests and five UI journeys, zero failures. The subsequent `repair-inbox-return-2.xcresult` / `ios-inbox-return-2.log` adds the inspector/composer return journey and passes all five navigation tests; six distinct UI journeys are covered across these final runs. Go tests, vet, daemon/surface race checks, the ad-hoc Mac package and dashboard control checks passed. The final independent unanchored review found no reproducible source blockers. GitNexus reported 327 changed symbols and 20 affected flows, with no partial/truncated result; aggregate risk is critical and the shared refresh/API paths received targeted checks. This is a source/review verdict, not a physical-device release receipt.

Visual checks use the owned iPhone 17 Pro and iPad Pro 11-inch simulators. Normal text size is `large`; accessibility sizes are separate stress checks. An earlier iPad screenshot used `accessibility-extra-extra-extra-large` and must not be presented as the normal layout. At accessibility text sizes iPad uses a single column, the composer footer stacks, and button icons stay inside 44-point targets. Reduced Motion disables the rotating work indicator.

## Distribution and operational boundaries

[The TestFlight runbook](testflight.md) documents signing, App Store Connect access, internal tester management, diagnostics and GitHub release automation. A `v*` tag starts the release workflow; ordinary main builds do not upload to TestFlight. The workflow also publishes the Mac package and deploys the relay. A UI source commit alone does not update the phone or the running Mac daemon.

The repair introduces no daemon restart, model instruction, pairing replacement or notification registration during local verification. Physical inspection of the old installed app and rendering captured data establish different facts. A corrected TestFlight build and the actual paired phone path require a separate release/install receipt.

## Remaining capability boundaries

This release identifies images but does not retrieve their pixels; a placeholder does not match the reference gallery. Live approvals, structured question replies, subagent navigation, artifact browsing and authoritative repository diff review need explicit supported contracts. Recorded permission tools are not live permission requests. The voice operator has a separate implementation and live-audio verification task; no inert microphone control is shown here.

Pages remain bounded to approximately 512 KiB, up to 200 items, a 4 MiB read window and 16 KiB per text item. JSONL is expected to be append-only. A cursor beyond a shortened file returns a refresh error. An adapter schema change may require parser work. An open session refreshes every four seconds plus event updates, with explicit stale/error states. A stale but readable catalog does not block connecting to the live event stream; StaleConnectionTests covers that recovery boundary. VoiceOver end-to-end, real push delivery and a fresh camera pairing round trip are outside this repair’s completed local checks.
