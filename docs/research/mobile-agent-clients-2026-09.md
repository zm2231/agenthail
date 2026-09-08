# Mobile agent clients: September 2026

Researched September 7, 2026. The most useful references for Agenthail are **Mimi Remote for native session continuity**, **Happy/HAPI for structured tool interaction**, and **Remodex/CC Pocket for mobile supervision**. This is a source and product-documentation comparison, not a hands-on reliability ranking.

The design target is a phone that can answer: What is happening? What changed? What needs my decision? Can I safely continue this exact session?

## Current landscape

| Project | Evidence checked | Relevant strengths | What to take into Agenthail |
|---|---|---|---|
| [Happy](https://github.com/slopus/happy) | Source at `ac64b9b4677870f7b7a9eacfd0780959229717f1`, Sept 7 | Tool-specific compact/full views; permission UI; file-edit navigation; mobile/web clients | Compact activity should lead to inspectable details. Do not hide unknown tools merely because there is no specialized renderer. |
| [HAPI](https://github.com/tiann/hapi) | Source at `3873e58496b01ade66271ad70f2cf4c24d55d90f`, Sept 6 | Registries for edits, plans, questions, patches and subagents; local/remote handoff; Web/PWA/Telegram | Treat commands, changes and questions as different content types. Keep raw inputs available. |
| [Mimi Remote](https://github.com/gaixianggeng/mimi-remote) | Source at `a823d08025dd4e65b5063849c4d966068f522a5e`, Sept 7 | SwiftUI client and Go host; stable timeline anchors, grouped work, explicit uncertain delivery; iPad workspace | Preserve identity through updates, distinguish queued/confirmed/uncertain messages, adapt layout without removing capability. |
| [Remodex](https://github.com/Emanuele-web04/remodex) | Project docs and creator's March 26 X launch | Codex-focused paired mobile control; local execution; thread/subagent/skill and Git features advertised at launch | A session is more than a transcript: adjacent actions matter, and the Mac remains the execution authority. |
| [CC Pocket](https://github.com/K9i-0/ccpocket) | Project listing and repository documentation | Flutter mobile client with WebSocket bridge for Claude/Codex | Useful additional reference for portable approval and question interactions; no implementation reliability verdict here. |
| [Happier](https://happier.dev/) | Current project site | Unified multi-agent client with explicit differences in handoff support between agents | Publish capability differences. A common UI must not imply universal support. |
| [OpenCodex](https://github.com/mjmkk/opencodex) | Repository README | Native iOS plus Node worker; chat, execution logs, approvals, files, terminal | Keep logs and file inspection reachable from the conversation; a terminal is an escape hatch with a different interaction cost. |
| [Polpo](https://github.com/pugliatechs/polpo) | Repository README | Mobile web dashboard spanning multiple local agent stores | Cross-agent discovery and preservation of existing sessions matter as much as creating sessions. |
| [Agmente](https://agmente.halliharp.com/docs/intro) | Official documentation | ACP and Codex app-server connections, tool outputs, images, multiple servers | Protocol capability and persistence differences belong in the product model. |
| [VibeTunnel](https://github.com/amantus-ai/vibetunnel) | Repository README | Browser access to real terminals | Terminal fidelity is a useful comparison, but small-screen supervision still needs semantic structure. |
| [AgentDeck](https://github.com/puritysb/AgentDeck/blob/master/docs/apple-app.md) | Apple app documentation | SwiftUI monitoring/control across phone, tablet and Mac | A compact overview can expose attention and host state before the user opens a transcript. |
| [Codecast](https://github.com/codecast-sh/codecast) | This exact repository README | Multi-agent session visibility, steering, shared memory and attribution | Keep product identity precise. Several unrelated projects share the Codecast name. |

The first three were shallow-cloned into ignored build output and read without executing their code. Commit dates are observations from `git log -1`, not claims about an App Store release. Other rows are documentation-level evidence. No third-party source code was copied into Agenthail.

## Source-level lessons

**Use semantic tool views with an inspection path.** Happy's `ToolView` chooses a known-tool presentation, links file edits to file views, and exposes permission handling. It also has hidden/minimal categories; Agenthail should preserve unknown activity instead of silently losing it. [Pinned source](https://github.com/slopus/happy/blob/ac64b9b4677870f7b7a9eacfd0780959229717f1/packages/happy-app/sources/components/tools/ToolView.tsx)

**Tool registries are a useful extension point.** HAPI's registry maps edits, plans, question prompts, patches, and agent operations to specialized views. Its duration calculation requires timestamps from the same clock and rejects negative durations. Agenthail should avoid inventing success states or timings from loosely related events. [Registry](https://github.com/tiann/hapi/blob/3873e58496b01ade66271ad70f2cf4c24d55d90f/web/src/components/ToolCard/views/_all.tsx), [duration calculation](https://github.com/tiann/hapi/blob/3873e58496b01ade66271ad70f2cf4c24d55d90f/web/src/components/ToolCard/toolDuration.ts)

**Preserve reading position separately from grouping.** Mimi's timeline types retain original message anchors beneath derived activity/work groups. Its delivery reconciler treats an interrupted send as uncertain and uses authoritative message/turn identity to resolve it. Agenthail should not interpret a network failure as proof a message was never accepted. [Timeline model](https://github.com/gaixianggeng/mimi-remote/blob/a823d08025dd4e65b5063849c4d966068f522a5e/ios/MimiRemote/Sources/Features/Conversation/Timeline/ConversationTimelineModels.swift), [delivery reconciler](https://github.com/gaixianggeng/mimi-remote/blob/a823d08025dd4e65b5063849c4d966068f522a5e/ios/MimiRemote/Sources/State/ConversationMessageDeliveryReconciler.swift)

## Twitter/X findings

Grok performed a read-only X/web research pass. The following post dates and text were independently fetched through FxTwitter's public JSON mirror; X's web pages did not expose readable bodies here. They establish what people announced or discussed on those dates, not a current feature guarantee.

| Date | Post | Observed signal |
|---|---|---|
| March 26 | [Remodex creator launch](https://x.com/emanueledpt/status/2037167701940900339) | Announced an iOS App Store release, QR pairing, threads, subagents/skills, encryption and Git actions. |
| May 14 | [OpenAI mobile preview announcement](https://x.com/OpenAI/status/2055016850849993072) | The official baseline includes starting work, reviewing results, steering, and approving next steps while execution stays on the computer. |
| August 10 | [Category discussion](https://x.com/stretchcloud/status/2086625327355167218) | Describes remote supervision and multiple-agent oversight as a distinct workflow. Its naming/protocol and popularity claims were not used as technical evidence. |
| August 17 | [HAPI workflow discussion](https://x.com/DanKornas/status/2089441171617480754) | Emphasizes continuing the same local session, remote approvals, and scoped workspace browsing. Cross-checked against HAPI's repository. |

The May 12 Remodex article link returned an empty text field through the mirror, so its detailed claims were not used. Grok also conflated other Codecast products with the requested `codecast-sh/codecast`; the table above uses the explicitly inspected repository. Missing X coverage for a project is not evidence of low adoption.

## Application to this worktree

The implementation prioritizes ordered activity, paging, stable identities, context/goal/model inspection, capability-derived controls, separate drafts, queued delivery feedback, older Codex discovery, and a native tablet layout. Commands, edit inputs and plans get focused presentations, with recorded input still available. Tool outputs and unknown activity remain inspectable.

Further parity requires transport contracts, not just UI: approving a particular outstanding request, answering a structured question with its request ID, retrieving bounded artifacts/images, navigating subagent relationships, and reviewing authoritative repository diffs. A historical tool record must not become a fake live approval button. These are explicit remaining product gaps rather than claims made by this change.

## Second pass: creation, compactness and operator controls

Mimi's actual new-session sheet chooses workspace and runtime, restores selection, disables creation while in flight, and handles an empty workspace catalog. Its flow makes starting work a primary mobile action. Agenthail's existing backend already exposed Codex session creation and Notion thread creation; the second pass adds those consumer paths to the phone, including workspace choices, model selection and uncertain-delivery handling. [Pinned new-session source](https://github.com/gaixianggeng/mimi-remote/blob/a823d08025dd4e65b5063849c4d966068f522a5e/ios/MimiRemote/Sources/Features/Shell/NewSessionSheet.swift)

HAPI's grouped presentation derives compact intent labels for inspection, search, changes and commands. The transferable lesson is progressive disclosure: summarize activity without dropping the underlying records or inferring success. Agenthail now groups contiguous non-message records with tool counts, error counts and raw ordered entries underneath. It does not split an activity run at an arbitrary record count. [Pinned grouping source](https://github.com/tiann/hapi/blob/3873e58496b01ade66271ad70f2cf4c24d55d90f/web/src/components/ToolCard/groupedPresentation.ts)

The second audit also traced existing Agenthail action producers against mobile consumers. Queue retry/cancel, goal set/clear and session aliasing were backend capabilities missing on phone. They are now reachable from Today or session details. This is distinct from the remaining protocol work above: those gaps could be closed through the existing authenticated API.

## Claude creation correction and integration

Claude Remote Control supports on-demand local session creation in server mode, including shared-directory and worktree spawn modes. The installed `claude remote-control --help` and [official Remote Control documentation](https://code.claude.com/docs/en/remote-control#start-a-remote-control-session) confirm this. Calling Claude creation a transport limitation was incorrect: the mobile branch lacked the adapter implementation, while the sibling Claude worktree already documented and implemented native background creation.

That implementation is now integrated. Agenthail starts Claude through the installed CLI’s `--bg` interface, resolves its returned identity through the native agent catalog and exposes the creation options on phone. This is a native background integration rather than an implementation of the Remote Control cloud endpoint. [Current operations](../maintainers/session-operations.md) and [historical capability audit](../maintainers/agent-capability-audit.md) document that distinction.
