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

`session-read` is what a client sends when the person opens a conversation.
With Codex Desktop reachable, it loads the thread into Desktop's app-server
(`thread/resume`, unless Desktop already has it live) and marks it read on that
connection, whichever app created the thread. If the daemon's app-server has the
thread loaded, or is mid-turn on it, the thread stays there and is only marked,
through the daemon, so a thread never has two writers. With Desktop not
reachable, nothing is loaded: the thread is marked through the daemon's
app-server if it is already running, and nothing happens otherwise. Opening
never starts Codex's daemon.

Sends keep their own routing: the runtime that has the thread loaded takes it
first, so a thread an open loaded into Desktop is sent through Desktop.

The mark is made only when the conversation was unread or its receipt has moved
since the revision sent; opening a conversation already read at that revision
loads it and leaves the receipt alone. Opens of one conversation at one revision
that arrive together share one load. Opening a thread the daemon holds costs
three requests on the daemon's app-server (`initialize`, `thread/loaded/list`
and the mark) plus one `thread/loaded/list` on Desktop when it is up.

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
