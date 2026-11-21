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

`session-read` and `session-unread` take `sessionId` and `revision`, the
`readState.revision` of the row the client showed, through `/api/action` and
`/api/v1/actions`.

`session-read` is what a client sends when the person opens a conversation. It
first loads the thread into its writer with the same call a send makes, so the
conversation is live and writable at once, then marks it read on that same
connection. The writer is chosen as for a send: the runtime that has the thread
loaded, otherwise Codex Desktop for a thread Desktop created or whose creator
is unknown, otherwise the daemon's app-server, which is started when it is not
running. A thread whose writer is Desktop while Desktop is down is marked
through the daemon without being loaded, so it never takes Desktop's writer.
A thread that is already live and writable is not resumed again.

`session-unread` only marks; it loads nothing.

| Reply | When |
|---|---|
| `200` with the new `readState` | Codex took the mark. |
| `400 invalid_request` | `revision` is missing. |
| `403 person_only` | An agent session sent it, directly or through a paired host. Agents reading a conversation never mark it. |
| `409 read_state_conflict` | The receipt changed since `revision`. The row is refreshed and nothing is retried. |

Codex accepts a conflicting mark only on a fresh action by the person, so a
client never resends after a `409` on its own.

## When a client sends them

A client sends `session-read` once a Codex conversation with a `readState` has
stayed open, with the window focused and visible, for one second: when the
person opens it, whether it shows read or unread, and when they come back to the
window with it open. A conversation flicked past within the second gets
nothing. While the person keeps looking, a row update that shows it unread, such
as a turn that just completed, sends `session-read` after the same second. A
conversation open in a background window stays unread. A client sends each
revision at most once.

After the person marks a conversation unread, the client sends `session-read`
again only when the person opens it or comes back to the window, not while they
keep looking at it. The same holds after a `409`.
