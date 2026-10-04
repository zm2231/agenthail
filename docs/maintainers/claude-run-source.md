# Claude Code run-source contract

This document records the read-only local observation contract verified against
the installed Claude Code CLI `2.1.289` on 2026-10-04.

The local evidence commands were `claude --version`, `claude --help`,
`claude agents --help`, and `claude agents --json --all`; the latter was used
only to read the catalog. The primary behavior references are
[Commands](https://code.claude.com/docs/en/commands),
[Routines](https://code.claude.com/docs/en/routines), and
[Subagents](https://code.claude.com/docs/en/sub-agents).

## Producers

`claude agents --json --all` is the installed CLI session catalog. Its observed
JSON records contain `id`, `cwd`, `kind`, `name`, `sessionId`, `startedAt`, and
`state` for background agents, plus `pid` and `status` for interactive sessions.
The command is read-only and is the authoritative producer for the session
catalog, but it does not expose child relationships or wake times.

Claude Code also writes local background-job records at
`~/.claude/jobs/<job-id>/state.json`. The progressive `/api/session-metadata`
endpoint reads these records through `surface.ClaudeRunObserver` and filters them to matching
session or resume-session IDs. It exposes only fields observed in that
producer:

- `runType` is the exact `template` value, such as `bg`.
- `providerState` is the exact `state` value, such as `working`, `blocked`,
  `done`, `failed`, or `stopped`.
- `sessionId` and `resumeSessionId` are copied exactly.
- `createdAt` and `updatedAt` are parsed only as RFC3339 timestamps.

The job record's `children` array is not used for session relationship
observation. Its observed member was an artifact descriptor (`kind: frame`),
not a Claude agent identity.

Subagent relationships use a separate local producer. The Claude
progressive metadata API exposes only links whose validated parent ID matches the
requested session:
`~/.claude/projects/<encoded-cwd>/<parent-session-id>/subagents/agent-<agent-id>.jsonl`.
`ObserveClaudeSubagentLinks` derives the parent session and agent ID from that
path, then requires a JSONL record with matching exact `sessionId` and
`agentId`. This yields a validated parent-session -> local-agent link without
deriving state from transcript content. The CLI `agenthail claude runs` exposes
the same observer contract read-only.

## Deliberately unavailable

The verified job records contain no typed `waiting`, `wakeAt`, cron, or
schedule fields. Timeline records at `~/.claude/jobs/<job-id>/timeline.jsonl`
contain only `at`, `state`, `detail`, and `text`. Agenthail therefore does not
infer waiting from `blocked`, prose, or timestamps, and it does not invent a
wake time.

The installed CLI help exposes `--bg` and `claude agents --json --all`, but not
`/loop` or `/schedule`: those are interactive skills, not session-catalog
fields. The primary Claude Code docs describe `/loop` as repeating a prompt
while a session stays open and `/schedule` as managing cloud routines. Neither
document defines a local `~/.claude/jobs` schema that Agenthail can consume.

No cancellation or scheduling action is implemented here. Existing Claude
targets are never messaged, stopped, resumed, or otherwise mutated.

## Context-window provenance

Transcript `message.model` values are not a configured-window producer. The
provider therefore leaves `ContextUsage.ContextWindow` at zero unless an
explicit launch/configuration value is captured for the same session. The
provider-side validator in
`internal/surface/surfaces/claude_run_source.go` recognizes an explicit
`[1m]` launch value as a configured 1,000,000-token window; a plain model name
and a missing model both produce `source: "unknown"` and no denominator.

The shared API projection carries the provider source as the typed optional
field `contextWindowSource`. Native clients show `usedTokens` without a
percentage when the window is zero or an estimated denominator would exceed
100 percent. They must not reconstruct a window from transcript model names or
observed usage.

The captured launch model is not authoritative forever. A confirmed `/model`
control result updates the in-memory session model; a provider assistant record
with a different model base invalidates a previously configured `[1m]` window.
A transcript model that only omits the `[1m]` suffix does not prove a switch,
so the configured denominator is retained in that case. Successful dashboard
and CLI model controls persist the returned current model back into the session
registry; metadata reads remain best-effort and non-blocking.

## Shared-surface integration

`Claude.StartSession` captures the launch model in
`surface.Session.ConfiguredModel`. The registry persists it in
`sessions.configured_model`, preserving an existing value when a later
registration omits the model. Claude context projection resolves only that
captured value and emits `ContextUsage.ContextWindowSource`; the existing
session API carries the field without a separate provider endpoint.

Native and dashboard consumers treat a zero window, an estimated window, or
usage above the denominator as token-only state. They do not show a percentage
for those cases. A configured `[1m]` value is the only current Claude window
producer, and it is displayed as configured rather than estimated.

## Journal production wiring

The native client consumes `/api/v1/session-stream`. The source completes its
bounded `ReadSession` seed before replay, then shares one provider tail per
session. Claude UDS sessions with a reliable local transcript use the same
transcript reader for live polling across completed turns. The tail reads a
bounded overlap at handoff, uses stable timeline IDs shared with the seed, and
normalizes user, assistant, reasoning, tool-call, tool-result, and done events
through one source loop. A session without reliable live-stream capability is
still readable through its seeded page, while `/session-stream` returns typed
`stream_unsupported` (HTTP 409) instead of opening a fake stream.

The coverage is a first-replay seed assertion, a real Claude transcript
seed-and-two-turn tail assertion, a UDS transcript stream assertion including
future roles and tool activity, a UUID-specific UDS rejection, and a typed
seed-only API rejection without a provider call.

The API does not expose `waiting`, `wakeAt`, cron, schedule, or cancellation
fields. Those values have no reliable local producer in the installed records;
their absence is intentional and must remain an unavailable state.
