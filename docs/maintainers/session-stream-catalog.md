# Session stream and catalog design

## Scope

This design replaces provider reads made by session-detail and live-stream
viewers with an Agenthail-owned, per-session journal. It also separates the
saved session catalog from live provider discovery. Task orchestration, external
control-plane protocols and automatic replay of uncertain actions are outside this
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
with the delivery failure/notice state. Every queue mutation that changes a
session's pending or in-flight count (enqueue, drain, cancel, retry, dead
letter, expiry sweep) commits a `session.queue` event with `sessionId` and the
new `queueCount` in the same transaction, so `catalogSeq` moves with the paging
membership that reads the live queue and stale page cursors are rejected.
Clients update the known row's `queueCount` and presence (`current` and
`currentReason`) from it and ignore it for unknown sessions; the following
background projection pass also emits a full-row `session.upserted`. The registry
validates the queue count inside the projection transaction before persisting
that full-row event; if the producer's count is stale, the daemon rebuilds the
row from the transaction's count before retrying. A failed discovery records
freshness/health without changing presence rows. A successful omission must
meet the configured removal threshold before producing `session.removed`.

## Runtime launchers

Each catalog session may include `runtime`:

```json
{"launcher":"cmux","location":{"workspace":"...","surface":"..."},"focusable":true}
```

The launcher is one of `cmux`, `tmux`, `claude-bg`, `codex-app-server`,
`claude-sdk`, or `external`. cmux locations use `workspace` and `surface`;
tmux locations use `session` and `pane`. A location is focusable only after
the authoritative launcher `Locate` path correlates the discovered Agenthail
session. Persisted locations are revalidated before focus, so stale panes or
reused process IDs are never focused. `claude-sdk` remains unavailable until
its real transport is registered; no peer fallback is allowed.

`GET /api/v1/session-options` also returns `launchers` with `{id,label,agents,
available,detail}`. Explicit `session-create` launcher requests return the
terminal result `{ok,launcher,location,sessionId?}` only after discovery
correlation. An omitted launcher preserves the existing creation path.

## Durable delivery problems

Snapshot responses include `deliveryProblems`, the newest 50 undismissed
durable problems. Each problem carries its numeric `deliveryId`, an RFC3339
timestamp, and the persisted status/evidence fields. `POST /api/action` with
`{"action":"delivery-dismiss","deliveryId":123}` atomically marks that
problem dismissed; the operation is idempotent and never resends the message.

`GET /api/v1/session?id=<id>` returns a bounded page from the session journal.
`GET /api/v1/session-stream?id=<id>&after=<sessionSeq>` is an SSE view over the
same journal. `Last-Event-ID` is accepted as the same per-session cursor.
Replay emits entries strictly after the cursor. When retention cannot satisfy a
cursor, the endpoint returns the typed `stream_gap` response so a client can
reload a bounded page. Session sequence numbers are never catalog sequence
numbers. A mutation of an existing provider key receives a new session
sequence, so a reconnect after its prior version replays the latest value.

Each session event has `itemId`, optional `providerKey`, mutation `version`,
operation (`append`, `upsert`, `remove`, or `phase`), `kind`, optional `turnId`
and `ts`, bounded `body`, `truncated`, optional `truncationReason`, and optional `bodyRef`. A body
reference is opaque, bound to the authenticated session, range-limited and
expires with journal retention; it never names a host path. A source rebuild is
a typed `source-error` reset with a bounded `reason`, not a removal. A provider absence or partial read never deletes
historical journal content.

Body range reads use byte offsets with UTF-8 boundaries: `start` must begin at
a code-point boundary, while `end` is reduced to the preceding boundary when
needed. The response's `end` is the actual returned byte offset, so clients can
concatenate successive ranges without splitting a code point.

Catalog event envelopes are `{stream:"catalog",seq,type,data}` and session event
envelopes are `{stream:"session",sessionId,seq,type,data}`. Their cursors are
separate. The following shapes are canonical:

```json
{"stream":"catalog","seq":41,"type":"session.upserted","data":{"session":{"id":"s1","surface":"codex","name":"Build","status":"busy","lastActive":"2026-10-03T12:00:00Z","hostProject":{"id":"host-project-1","displayName":"agenthail","commonDir":"/repo/.git"},"checkout":{"id":"checkout-1","path":"/repo/.worktrees/api","branch":"feat/api","isMain":false,"dirty":true},"freshness":{"generation":7,"observedAt":"2026-10-03T12:00:00Z","stale":false}}}}
{"stream":"catalog","seq":42,"type":"delivery.problem","data":{"deliveryId":1,"sessionId":"s1","sourceSessionId":"s2","message":"Run the tests","reason":"message expired after 1 hour","at":"2026-10-03T12:00:01Z"}}
{"stream":"session","sessionId":"s1","seq":9,"type":"item","data":{"itemId":"m1","providerKey":"m1","version":6,"op":"append","kind":"text","turnId":"u1","ts":"2026-10-03T12:00:02Z","body":"answer","truncated":false}}
```

## Session source lifecycle

A `SessionSource` is the only component allowed to read provider session
content for its session. It normalizes provider output, appends journal events
in sequence order, and fans out journal entries to subscribers. Codex Desktop
content is read from its local JSONL transcript as one incremental source: the
seed ends at the last complete newline, and the live tail starts at that exact
byte offset. Seed and tail use the same record parser and stable item keys,
including durable offset-based attachment references; an incomplete final line
is left for the tail. Codex managed app-server sessions use their separate
native transport path. The Desktop content stream does not use renderer event
records or a renderer cursor as a second content source. Claude transcript
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
idle. Older activity remains a bounded, cursor-paged journal read. If retention
has pruned the requested cursor, the page returns typed `history_gap` with the
retained sequence bounds so the client can reload from the retained window.

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
