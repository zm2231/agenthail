# Codex read state

Codex keeps a read receipt for each thread in its state database under
`CODEX_HOME`, so every Codex process and Codex Desktop see the same one. A
completed turn marks its thread unread. Agenthail shows that receipt and lets
the person change it; it keeps no read state of its own.

## Rows

Session rows carry `readState`:

| Field | Meaning |
|---|---|
| `firstUnread` | The turn id the conversation became unread at, or `threadStart` when it was marked unread as a whole. Omitted when read. |
| `revision` | The receipt's revision. A mark must send the revision the client showed. |

`readState` is omitted for conversations without a receipt: Claude and Notion
sessions, and a Codex thread before its first turn.

Each catalog pass reads receipts from `thread/list` (its `readStates` map) and
`thread/read`. A `thread/readState/changed` notification on the managed
app-server starts a pass at once. A mark made in Codex Desktop reaches rows on
the next pass, within 30 seconds. Receipts are stored in `codex_read_states`
(schema 121).

## Marking

`session-read` and `session-unread` take `sessionId` and `revision`, through
`/api/action` and `/api/v1/actions`. The mark goes through Codex Desktop when it
is reachable, so its sidebar updates at once, and otherwise through the managed
app-server.

| Reply | When |
|---|---|
| `200` with the new `readState` | Codex took the mark. |
| `400 invalid_request` | `revision` is missing. |
| `403 person_only` | An agent session sent it. Agents reading a conversation never mark it. |
| `409 read_state_conflict` | The receipt changed since `revision`. The row is refreshed and nothing is retried. |

Codex accepts a conflicting mark only on a fresh action by the person, so a
client never resends after a `409` on its own.

## When a client marks

A client marks a conversation read when the person can see it: the
conversation is open, the window is focused and visible, and the row shows it
unread. That covers opening it, a turn that completes while it is open, and
coming back to the window with it open. A conversation open in a background
window, or one the person is not looking at, stays unread.

After the person marks a conversation unread, the client marks it read again
only when the person opens it or comes back to the window, not while they keep
looking at it. The same holds after a `409`.
