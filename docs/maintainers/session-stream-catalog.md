# Session stream and catalog design

## Scope

This design replaces provider reads made by session-detail and live-stream
viewers with an Agenthail-owned, per-session journal. It also separates the
saved session catalog from live provider discovery. Task orchestration, ZEN
protocols and automatic replay of uncertain external actions are outside this
change.

## Contracts

`GET /api/v1/snapshot` reads only the registry. It accepts `scope`, `projectId`
and `q` filters plus a stable page cursor, and returns saved rows plus
`hostEpoch` and `catalogSeq`, captured in the same registry read transaction.
Each row retains the existing session summary fields and adds
`hostProject` (`id`, `displayName`, and `commonDir` or `path`), `checkout`
(`id`, `path`, `branch` or `detachedHead`, `isMain`, and `dirty`), and
`freshness` (`generation`, `observedAt`, and `stale`). A failed
Git query records typed unavailable identity and never hides the session. A
surface discoverer performs provider `List` calls in the background and emits
full-row `session.upserted`, `session.removed`, `session.unavailable`, and
`surface.health` catalog deltas. A proven delivery problem is one idempotent
`delivery.problem` catalog event with `deliveryId`, target and source session
IDs, bounded message or body reference, reason and timestamp; it is committed
with the delivery failure/notice state. A failed discovery records
freshness/health without changing presence rows. A successful omission must
meet the configured removal threshold before producing `session.removed`.

`GET /api/v1/session?id=<id>` returns a bounded page from the session journal.
When `id` is not a session ID, an exact handle (with or without `@`) is
accepted; the response always carries the resolved session ID.
`GET /api/v1/session-stream?id=<id>&after=<sessionSeq>` is an SSE view over the
same journal. `Last-Event-ID` is accepted as the same per-session cursor.
Replay emits entries strictly after the cursor. When retention cannot satisfy a
cursor, the endpoint returns the typed `stream_gap` response so a client can
reload a bounded page. Session sequence numbers are never catalog sequence
numbers. A mutation of an existing provider key receives a new session
sequence, so a reconnect after its prior version replays the latest value.

Each session event has `itemId`, optional `providerKey`, mutation `version`,
operation (`append`, `upsert`, `remove`, or `phase`), `kind`, optional `turnId`
and `ts`, bounded `body`, `truncated`, and optional `bodyRef`. A body
reference is opaque, bound to the authenticated session, range-limited and
expires with journal retention; it never names a host path. A source rebuild is
a typed `source-error` reset with a bounded `reason`, not a removal. A provider absence or partial read never deletes
historical journal content.

Catalog event envelopes are `{stream:"catalog",seq,type,data}` and session event
envelopes are `{stream:"session",sessionId,seq,type,data}`. Their cursors are
separate. The following shapes are canonical:

```json
{"stream":"catalog","seq":41,"type":"session.upserted","data":{"session":{"id":"s1","surface":"codex","name":"Build","status":"busy","lastActive":"2026-10-03T12:00:00Z","hostProject":{"id":"host-project-1","displayName":"agenthail","commonDir":"/repo/.git"},"checkout":{"id":"checkout-1","path":"/repo/.worktrees/api","branch":"feat/api","isMain":false,"dirty":true},"freshness":{"generation":7,"observedAt":"2026-10-03T12:00:00Z","stale":false}}}}
{"stream":"catalog","seq":42,"type":"delivery.problem","data":{"deliveryId":"d1","sessionId":"s1","sourceSessionId":"s2","message":"Run the tests","reason":"target_not_writable","at":"2026-10-03T12:00:01Z"}}
{"stream":"session","sessionId":"s1","seq":9,"type":"item","data":{"itemId":"m1","providerKey":"m1","version":6,"op":"append","kind":"text","turnId":"u1","ts":"2026-10-03T12:00:02Z","body":"answer","truncated":false}}
```

## Session source lifecycle

A `SessionSource` is the only component allowed to read provider session
content for its session. It normalizes provider output, appends journal events
in sequence order, and fans out journal entries to subscribers. Codex renderer
records use their native sequence as raw-delta identity within a persisted
source epoch; it is not presented as a stable final-message ID. Claude transcript
records use immutable record identity, not a mutable text hash. Provider keys remain absent where a
source cannot prove identity or correlate an effect. The source is held while
any viewer subscribes, a relay/route watches the session, a managed turn is
active, delivery reconciliation is pending, or a voice call is active.
It may become cold only when all holders are gone; a cold read reacquires it.

The journal has count and byte retention limits. A slow subscriber is closed
and resumes using its last received session sequence. Restart preserves the
journal; a reconnect either replays without duplicates or receives
`stream_gap`.

## iOS behavior

The catalog event stream uses the catalog cursor. An open-session view uses its
own session stream cursor, reconnects after foregrounding, and reloads its
bounded journal page after a typed gap. It does not poll session content while
idle. Older activity remains a bounded, cursor-paged journal read.

## Error and authorization behavior

Missing sessions return `404`; unavailable session content returns a typed
availability error without synthesizing history. Every stream validates the
initial device scope and rechecks it before writing events or keepalives; a
revoked device stream closes. The implementation records uncertain provider
effects for reconciliation but never blindly retries external actions.

## Behavioral checks

- Three viewers of one session cause one upstream reader.
- A busy journal cannot evict a quiet session's replay tail.
- Restart yields replay or a typed gap, never silent loss or duplicate replay.
- Snapshot performs zero provider calls, including during discovery.
- A failed discovery leaves prior catalog rows present and marks freshness.
- An idle open phone session performs no periodic content reads.
- Revoked authorization closes catalog and session streams.
