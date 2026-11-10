# Pinned sessions

A pinned session is one the person wants kept in view. A pin keeps the session
listed; it does not keep its process running. Nothing starts a pinned session
on its own: one whose process ended stays down until a message reaches it or
the person resumes it, and the message resumes it (see
[Reaching a session whose process ended](#reaching-a-session-whose-process-ended)).

## Pin and unpin

`session-pin` and `session-unpin` take `sessionId`, through every front end
(`/api/action`, `/api/v1/actions`, `agenthail thread pin|unpin <target>`).
Both are idempotent; a pin keeps the time of the first pin. A session on a
paired host is pinned on that host (see [Paired hosts](#paired-hosts)).

Unpinning always works, a project's lead included. A project's first lead is
pinned when it is assigned. A lead that replaces another, by a new
conversation or a takeover, takes the pin of the lead before it, so an unpin
survives the swap. Upgrading to schema 59 pins every current lead once.

Rows carry `pinnedAt`, omitted when unpinned. A pinned session counts as
current, so it stays in the `current` and `recent` scopes however long it has
been idle. Its catalog row is never retired, whether or not its process is up,
and pinning a retired row brings it back.

The pin lives in `sessions.pinned_at_ms`. Schema 94 dropped the state of the
keeper that used to wake pinned sessions (`session_keep_alive` and
`sessions.stopped_at_ms`).

## Reaching a session whose process ended

Every way of sending resumes a Claude conversation or Codex thread whose
process ended, and delivers once:

| Path | What happens |
|---|---|
| Agent send (`agenthail_send`, `agenthail send`), native peer or queued | A Claude conversation no process holds is resumed in the Claude host; a Codex thread is loaded with `thread/resume`. A queued send to an ended Claude conversation reads it as idle and sends now. |
| The person's send from the dashboard or an app | Sent as the person's turn, which resumes it. |
| Relay | Queued; the observer drains the queue, which resumes it. |
| Channel post | A stored channel reaches every member. A project post reaches this host's non-archived project sessions that are pinned or were used within `surface.ActiveWindow`, process up or not. |
| Voice delegation | Sent as the person's turn, which resumes it. A session another process holds without a way in fails as busy; voice does not queue. |

A message queued for a Codex app-server thread drains whether the thread is
unloaded, reads as offline, or its app-server has stopped: the observer sends
it, and the send starts the app-server and loads the thread as a direct send
does. Reads never start Codex.

## Paired hosts

A pin or unpin of a mirrored session is routed to the host that runs it, like
any session action, and fails with that host's reachability error while it is
down. The owner's catalog row carries `pinnedAt`, so mirrored rows show its
pin.

A session moved to another host keeps its pin, or its lack of one: the move
carries `pinnedAt`, and when the session resumes on the new host that host
takes it in place of any pin it had for the session. The old host drops its
pin with the session. A move that fails or is refused leaves the pin where it
was.
