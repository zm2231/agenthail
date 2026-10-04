# Shared stream consumers

## Contract

The daemon's `SessionSource` is the only live provider reader for a session.
Voice delegation and CLI consumers must subscribe to that source before sending
or otherwise starting work. The subscription returns the journal sequence that
was current when the subscription became ready. The consumer then dispatches,
replays entries after that cursor, and keeps the subscription open until the
authoritative delivery turn reaches a terminal event.

The ordering is deliberate:

```text
prepare subscription -> source seed/replay ready -> capture cursor
  -> deliver once -> obtain authoritative turn ID
  -> replay buffered entries + consume journal entries
```

Entries received before the receipt identifies the turn are buffered. Once the
receipt supplies its authoritative `TurnID`, only entries with that exact
`TurnID` are eligible for output. Entries from older, newer, or unrelated
turns are ignored. This prevents a fast reply between dispatch and consumer
startup from being lost and prevents another worker's activity from being
spoken or printed as the requested reply.

## Event mapping

The journal payload is the consumer protocol. `itemId`, `providerKey`,
`version`, `op`, `kind`, `turnId`, `role`, `body`, `status`, `reason`, and
`final`, `truncated`, and `bodyRef` remain intact. A seed record may carry a
turn only when the provider supplies an explicit turn identity. `callId` is a
tool/call correlation field and is never promoted to `turnId`. `kind=done` is
the only turn-terminal event; it is unsuccessful when its `status` is
`failed`, `error`, `cancelled`, or `canceled` (for example a Claude interrupt
marker), and consumers return that as an error rather than completion.
`kind=source-error` is a terminal source failure. A body
reference is surfaced as a reference, not dereferenced through a provider
read. Stable item identity and version are used for deduplication during
replay. A journal upsert contains the current body for that stable item; a
consumer speaks or prints only the suffix not already emitted for that item.
The provider's `final` bit marks that item's body lifecycle only. It never
ends the turn: providers may mark interim, reasoning, tool, and multiple
assistant items final before the separate `done` event.

## CLI and voice routing

When the daemon is running, CLI `send --stream`, `send --reply`, and `stream`
consume the authenticated session-stream endpoint. A failure from that active
daemon path is returned to the caller; it must not silently fall back to a
second provider reader. With no daemon, the existing bounded provider path is
retained for offline use.

Voice delegation uses the same source subscription; it reads the target's
provider stream directly only when no daemon session stream is available, with
the same terminal and per-item deduplication rules. The voice operator remains
a separate Codex realtime audio plane; only target-session journal entries matching the delivery
turn are appended to it. Cancellation releases the source subscription and
does not interrupt the target unless the existing explicit stop path requests
that action.

## Failure and lifetime rules

Preparation failure, stream gap, authorization failure, source error, and
deadline are distinct caller-visible errors. A source error ends the active
consumer without pretending completion. Subscription cancellation is required
on every dispatch failure, queued/submitted result without an authoritative
turn, timeout, and normal terminal completion. The source manager may become
cold only after all subscribers and holders are released.

Tests must cover the pre-dispatch fast-reply race, exact-turn filtering,
replay/deduplication, terminal and source-error mapping, active-daemon zero
provider reads, and offline bounded behavior.
