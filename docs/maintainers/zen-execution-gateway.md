# ZEN execution gateway

The protocol-2 gateway is the authenticated Agenthail surface used by ZEN for
session actions and replayable runtime events. It is an additive API contract:
the version response advertises protocol `2` with `minimumProtocol: 1` and
`maximumProtocol: 2`, so clients negotiating the iOS range `1-2` remain
compatible.

## Actions

`POST /api/v1/actions` accepts the existing authenticated action envelope when
it includes `idempotencyKey`. Supported actions are `send`, `steer`, and
`interrupt`. `interact` is normalized by the ZEN client to `send` before it
reaches this endpoint.

The idempotency key is reserved in the Agenthail registry before a native
write or durable queue insert. Reusing a key with the same action envelope
replays the stored receipt; reusing it with different session, action, message,
or source attribution returns `idempotency_conflict`. A known native rejection,
including `steer` or `interrupt` against an idle session, records a durable
`failed` receipt and returns typed `delivery_rejected` without issuing that
native action again on replay. An ambiguous transport failure is recorded as
an `unknown` receipt and must be reconciled before retrying.

Receipts are truthful at the boundary:

- `accepted` includes a native `turnId` when one is available.
- `queued` includes the durable `queueId` and may include a turn handle.
- `unknown` means the gateway cannot establish whether the native operation
  completed; it is not an implicit retry.

`sourceSessionId` is attribution, not routing. Values in the `zen:` namespace
require an authenticated credential with `read`, `control`, and `zen` scopes
(or the local dashboard administrator credential). An unprefixed value remains
an Agenthail registry session identifier.

## Session stream

`GET /api/v1/session-stream?id=<agenthail-session-id>` requires `read` access
and a stream-capable session. The response is SSE with `event: runtime_event`;
each data line contains a structured `{"event": ...}` envelope. Event IDs are
the persistent Agenthail event IDs, not connection-local counters. The gateway
replays retained events before following the live stream.

Clients resume with `Last-Event-ID`. If the cursor is malformed or no longer
available in the retained event journal, the gateway returns HTTP `409` with
error code `stream_gap`; the client must perform its full-replay/reconciliation
path. Keepalive comments are transport heartbeats and are not journal events.
With no cursor, the gateway replays the retained session tail. A nonzero cursor
older than that retained tail returns `stream_gap`, after which the no-cursor
replay is the explicit recovery path.

For Claude and Codex sessions, the daemon observes the native transcript through
`TimelineProvider` and pages until it reaches the durable item boundary. Complete
message, thought, tool-start, and tool-done items become canonical runtime events;
stored transcript truncation is marked `truncated: true`, not with the streaming
`chunk` field.
Per-item durable receipts prevent old transcript items from being republished
after the rolling 1024-event journal has pruned their frames. Sources without a
timeline provider or effective stream capability return `stream_unsupported`.
Observed busy, idle, and offline transitions are emitted as canonical `phase`
events (`running`, `idle`, and `stopped`); a turn completion without a native
turn index remains a phase transition rather than an invented `turn_end`.

## Optional live proof

The committed `gateway_live` test proves the Codex producer-to-consumer path
with a real dedicated Codex task, an isolated temporary registry, an ephemeral
HTTP server, and the ZEN Bun transport. Run it from this checkout with an
absolute ZEN checkout and the dedicated task ID:

```sh
AGENTHAIL_ZEN_LIVE_THREAD_ID=01a0b111... \
AGENTHAIL_ZEN_LIVE_ROOT=/Volumes/4/GitHub/zen \
go test -tags gateway_live ./internal/daemon -run TestZENLiveCodexGateway -v
```

The proof checks native session discovery, writable and streamable capability
projection, an accepted interaction receipt, identical idempotency replay, and
the assistant response arriving through the structured SSE stream with a
cursor. It does not start the installed daemon. It does not prove Claude
native execution or installed-daemon behavior.

If the initial timeline scan fails, session-stream establishment returns HTTP
503 with `stream_unavailable` and does not open an incomplete stream. If a
later timeline poll fails, the gateway emits a terminal `stream_error` SSE
frame containing the typed error and closes the stream. If the retained daemon
event journal cannot be loaded, both event-stream endpoints return HTTP 503
with `event_journal_unavailable`; they never present an empty history as valid.
