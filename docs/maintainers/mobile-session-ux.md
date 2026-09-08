# Mobile session fidelity audit

September 7, 2026. Scope: the native phone/tablet client and its authenticated session API. [Research comparison](../research/mobile-agent-clients-2026-09.md) covers twelve related projects, with pinned source inspection for Happy, HAPI and Mimi Remote and independently checked X announcements.

## Implemented paths

| User need | Producer and consumer | Verification |
|---|---|---|
| Understand agent work | Claude/Codex local JSONL → `TimelineProvider` → opt-in session API → Swift timeline → expandable tool, reasoning and event rows | Parser fixtures, API tests, signed iOS decode tests, simulator tool expansion |
| Read older activity | Bounded reverse file reader → byte cursor → API → model history merge → Load older activity | 450-record paging fixture, stable IDs after append, iOS model history fixture |
| Inspect context and controls | Existing session capabilities/context/goal/model → shared Swift detail → summary, composer and inspector | Swift decode/model tests, phone and tablet inspector inspection |
| Send to the intended session | Session-specific drafts + authoritative detail capabilities → action API → accepted/queued receipt | URLProtocol test switches sessions, challenges stale capabilities and exercises queued delivery |
| Recover from missing data | Typed unavailable timeline/session errors → history fallback or retry UI | Missing/malformed transcript fixtures, sanitized API failure, iOS load failure/recovery |
| Find saved conversations | Existing Codex search → authenticated search route → debounced results → direct detail route | API route guard test; source trace through native API/model/navigation |
| Read comfortably on phone/tablet | Native split/navigation layouts, semantic tool summaries, Dynamic Type, paused following and Jump to latest | iPhone 17 Pro and iPad Pro 11-inch simulators; light/dark and largest accessibility text inspected |

The timeline is optional on the existing session endpoint. Mac callers retain the default message detail request. The shared Mac app compiles and packages with the added models/API methods.

## Completed local checks

- `go test ./...`
- `go vet ./...`
- `go test -race ./internal/surface/surfaces ./internal/daemon`
- Signed `xcodebuild test` for `AgenthailIOS`: 15 tests, zero failures, iOS 26.3 simulator.
- `xcodebuild ... -configuration Release -destination 'generic/platform=iOS Simulator' build`.
- `AGENTHAIL_CODESIGN_IDENTITY=- scripts/build-macos-app.sh build/Agenthail.app`, including strict signature verification.
- `git diff --check` and GitNexus change analysis against the worktree's own refreshed index.

Local receipts are in ignored `build/`: `go-all-tests.log`, `go-vet.log`, `go-race.log`, `ios-tests-final.log`, `final-session-tests.xcresult`, `ios-release.log`, `macos-build.log`, and `screenshots/`. Public fixture previews use the actual session views; Debug builds accept `--preview-session`, optionally with `--preview-inspector` or `--preview-workspace`. Release builds exclude these previews.

## Remaining boundaries

This is local implementation and verification, not a deployed TestFlight release. No live daemon restart, real agent instruction, physical-device Tailscale connection, push delivery, camera permission round trip, or VoiceOver session was exercised. Simulator screenshots establish layout, not those integration guarantees.

The new activity path preserves supported transcript records, not every possible agent protocol feature. Live approval/question replies, artifact/image retrieval, subagent navigation and authoritative repository diff review need additional request/response contracts. Recorded tool inputs are inspectable but do not become live permission controls. Goal information is viewable; goal editing remains outside the phone controls.

Pages and text are deliberately bounded and disclose truncation. JSONL files must remain append-only for byte cursors to retain meaning; a cursor beyond a shortened file returns a refresh error. Agent transcript schema changes can require parser updates. Remote-only sessions without a local transcript display available message history and an explanation.
