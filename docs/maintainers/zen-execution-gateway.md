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
or source attribution returns `idempotency_conflict`. A failed native delivery
is a typed `delivery_rejected` error. A non-terminal failure is recorded as an
`unknown` receipt and must be reconciled before retrying.

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

This slice verifies the authenticated action path, durable replay, source
namespace authorization, stable SSE IDs, and stream response shape with fake
surfaces. It does not prove live native execution, installed-daemon behavior,
or producer-to-consumer ZEN E2E.
