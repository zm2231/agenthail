# Pinned sessions

A pinned session is one the person wants up. When its conversation ends, the
host that runs it brings it back without starting a turn, so nothing is sent
and no tokens are spent.

## Pin and unpin

`session-pin` and `session-unpin` take `sessionId`, through every front end
(`/api/action`, `/api/v1/actions`, `agenthail thread pin|unpin <target>`).
Both are idempotent; a pin keeps the time of the first pin. A session on a
paired host is pinned on that host, which is the one that keeps it.

Unpinning always works, a project's lead included. A project's first lead is
pinned when it is assigned. A lead that replaces another, by a new
conversation or a takeover, takes the pin of the lead before it, so an unpin
survives the swap. Upgrading to schema 59 pins every current lead once.

Rows carry `pinnedAt` (omitted when unpinned) and, after the keeper gave up,
`keepAlive: {failedAt, error}` with attention kind `keepAliveFailed`.

## The keeper

Every 30 seconds, and when the daemon starts, the keeper looks at this host's
pinned sessions. It skips one that is archived, the ledger's narrowing checker
(which only Agenthail drives), or stopped through `session-lifecycle-stop` and not active since. A
`session-lifecycle-resume` or a pin clears the stop.

It brings back only conversations it can open without input:

| Runtime | Ended when | Brought back by |
|---|---|---|
| `claude-host` | no reachable process holds the conversation | opening it in the Claude host with `resume`, no prompt |
| `codex-app-server` | the app-server does not have the thread loaded for direct input | `thread/resume`, no `turn/start` |

Other runtimes, terminal sessions among them, can be pinned but are not
brought back. A conversation an orphaned process still holds counts as up.

Each wake counts, whether it failed or the session ended again soon after.
After a wake the keeper waits 30s, 1m, 2m, 4m, then 8m. The fifth wake within
30 minutes stops the keeper for that session: the row needs the person and
one notification goes to the desktop and paired devices. Ten minutes up, a
resume or a new pin starts the count over.

State lives in `sessions.pinned_at_ms`, `sessions.stopped_at_ms` and
`session_keep_alive`.
