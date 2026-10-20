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
`~/.claude/jobs/<job-id>/state.json`. The session-metadata API reads these
records through `surface.ClaudeRunObserver` and filters them to matching
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

Subagent relationships use a separate local producer, documented with the
working-state rule and the cache in [subagents.md](subagents.md). The
session-metadata API reads only the requested session's subagents directory,
`<project>/<transcript-id>/subagents/`, derived from the session's transcript
path, and requires each transcript's identifying record to match its path.

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

A session the Claude host runs reports the window Claude itself says it runs
the main model with (`provider`, see `claude-host.md`), while that model is the
one the transcript last replied with. That outranks everything below.

Claude's transcript records no window size, only evidence of a 1M selection.
Otherwise the provider reports a 1,000,000-token window from the first of
these that holds for an assistant record, and an unknown window otherwise:

1. `request`: the newest `api-request` record for the reply's model base
   carried a `context-1m-*` entry in `params.betas`.
2. `transcript`: the record's `requestedModel` is a `[1m]` model, such as
   `claude-opus-5-5[1m]`, with the reply's model base. A record with a plain
   `requestedModel` leaves the window unknown, even after a `[1m]` launch.
3. `configured`: the record has no `requestedModel` and the launch model
   Agenthail captured is a `[1m]` model.

Missing evidence never means a smaller window. Claude runs some models at
1,000,000 tokens without the beta or the tag: Claude Code 2.1.304 reports
`contextWindow: 1000000` in the `modelUsage` of a plain `claude-opus-5-5` run
whose request had no 1M beta. That figure exists only in Claude's runtime
output, not on disk, so a session outside the host without 1M evidence shows
tokens only.
`message.model` never carries the `[1m]` selection, so it only rules out a
window chosen for another model. The validator in
`internal/surface/surfaces/claude_run_source.go` recognizes a `[1m]` model; a
plain model name and a missing model both produce `source: "unknown"` and no
denominator.

The shared API projection carries the provider source as the typed optional
field `contextWindowSource`. Native clients show `usedTokens` without a
percentage when the window is zero or an estimated denominator would exceed
100 percent. They must not reconstruct a window from transcript model names or
observed usage. The optional `autoCompact` object (`enabled`, `window`,
`threshold`, `decidedBy`, `source: "runtime"`) is when the running Claude
compacts, as it reports it; clients show it as is and never derive it from
settings files.

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
registration omits the model. Claude context projection resolves the window
from the evidence above and emits `ContextUsage.ContextWindowSource`; the existing
session-detail API carries the field without a separate provider endpoint.

Native and dashboard consumers treat a zero window, an estimated window, or
usage above the denominator as token-only state. They do not show a percentage
for those cases. A 1M window from any of the three sources is displayed as
reported, never as estimated.

## Journal production wiring

The native client consumes `/api/v1/session-stream`. The source completes its
bounded `ReadSession` seed before replay, then shares one provider tail per
session. Claude UDS sessions with a reliable local transcript use the same
transcript reader for live polling across completed turns; the source stays
open and does not reseed between turns. A reader without reliable live-stream
capability remains seed-only and stays open until its client cancels, avoiding
unsupported-stream retry and reconnect noise.

The coverage is a first-replay seed assertion, a real Claude transcript
seed-and-two-turn tail assertion, a UDS transcript stream assertion, a bounded
seed-only API replay assertion, and a no-provider-call seed-only assertion.

The API does not expose `waiting`, `wakeAt`, cron, schedule, or cancellation
fields. Those values have no reliable local producer in the installed records;
their absence is intentional and must remain an unavailable state.
