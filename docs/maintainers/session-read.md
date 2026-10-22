# Session pages and metadata

`GET /api/v1/session?id=<session>&timeline=1&limit=10` returns the recent
session journal page. `limit` counts exchanges, between 1 and 50 (default 10);
any other value returns `400` with `error.code` set to `invalid_limit`.
An exchange is one turn, as the provider's turn markers record it: items
carry the turn's `turnId`, and a `done` item closes the turn. Its prompt is
the item that opened the turn, with that item's timestamp: a message from the
person, or one the provider injected, which the exchange labels with `origin`
(`peer` with its `sender`, `task-notification`, `auto-continuation`, and for
Claude `command`, `interrupt` or `system`). A prompt from the same sender
later in the same turn, such as the question after Codex's app context, is
appended to it; a message injected into a running turn by someone else stays
in the turn's timeline and does not open an exchange. Items recorded without
a turn group by role: a person's message opens an exchange, and a reply
before any prompt forms an exchange without one. The exchange's reply is the
turn's last assistant message. `timeline.items` holds every item of those
turns, so the page starts at the oldest included prompt. A page reads at most
the session's retained journal and returns the whole exchanges that fit 1 MB
of journal rows; a single turn longer than that starts mid-turn and is marked
truncated. Exchange text shares a 512 KB budget across every exchange of the
page, so no exchange is dropped. `exchanges` and `timeline.items` describe
the same turns. `timelineBefore` requests turns strictly before the returned
`timeline.nextBefore` cursor. Items retain
their identity, role, title, status and retained body reference. If retention
has pruned history needed by `timelineBefore`, or the journal has no older-history
boundary, the endpoint returns `409` with
`error.code` set to `history_gap` and the retained `earliestSeq`/`latestSeq`.

`timeline.nextBefore` is opaque; clients pass it back unchanged and treat `0`
as the end of history. A seed into an empty journal records the provider's
older-history boundary. When a journal page reaches the oldest journal entry and
the provider reported older history, `nextBefore` addresses that provider
history; when the provider reported none, it is `0`. A journal whose oldest
entries predate any recorded boundary keeps `nextBefore` on its oldest entry,
and requesting it returns the `409` `history_gap` response because older
activity is not retained.
Requesting it reads whole provider pages, newest first, through the session's
shared source, serialized with its seed and refresh reads, until they hold
`limit` exchanges or the next page would pass 400 items; that page is left for
the returned provider cursor. The items come back with `readSource` set to the
provider source. The
session open never preloads the whole transcript. A failed older read reports
`timeline.unavailableReason` instead of returning an empty page.

`journalSeq` is the session watermark captured in the same SQLite read
transaction as the page. Open `/api/v1/session-stream?id=<session>&after=<journalSeq>`
to receive subsequent changes rather than replaying the entire retained journal.
An older-page request does not change the active stream cursor.

Every newest-page read goes through the shared session source and waits at
most two seconds for its first pass. A cold journal is seeded by that pass. A
seeded journal backed by a transcript (Claude, and Codex with a local
transcript) catches up by reading the transcript newest-first until it meets
the journal, so turns written while no source ran appear on the next read.
A Claude journal also keeps a transcript checkpoint in
`session_journal_state.transcript_checkpoint`: the byte offset and file
identity up to which it holds the transcript's items. The seed, each catch-up,
and each live stream window record it after the items they appended; a
window in which a journal append failed clears it instead. A source that starts
with a checkpoint reads forward from that offset once, deriving items the same
way a page does. It pages newest-first instead when there is no checkpoint, the
file identity changed, or more than 32 MiB was written since the checkpoint.
A Claude journal written by an earlier version of the item derivation is
rebuilt from the transcript once, at a source start: in one transaction, its
retained rows are replaced by the items the transcript records from the
oldest retained offset, after the newest sequence, and rows the transcript
does not hold stay after the item they followed. The journal records the
derivation, so it is not rebuilt again. A stream client resuming from the
newest cursor receives the rebuilt rows as upserts of the same items; an
older cursor gets the `409` `stream_gap` response and reloads the page.
Concurrent reads share one source and one pass, and a source that is already
running returns at once. The page never calls a provider's metadata methods. Provider read failures are reported separately
from an empty transcript. Sources preserve the provider's truncation flag and
bound inline bodies; retained body fetches remain session-scoped. Full body
references count toward the journal byte budget. If a full body cannot fit,
the bounded inline preview remains with `truncated: true`,
`truncationReason: "full_body_not_retained"`, and no body reference is
emitted.

The initial-page source has a five-second handoff lease so opening its stream
reuses the same reader instead of fetching and journaling the history twice.

`GET /api/v1/session-metadata?id=<session>` returns independent optional
`context`, `goal`, `model`, and `models` fields, plus an `errors` map when a
category is unavailable. The request has its own three-second deadline.
Metadata failure does not erase session content. Native and browser clients
load this after showing the page and ignore results belonging to a previous
selection. Codex model reads use read-only thread metadata, never resume.

For Claude sessions, metadata also contains optional `claudeRuns` and
`claudeSubagents` arrays. The daemon filters background-job records and
validated local subagent links to the requested session. Browser conversation
details and native session details display these observations without
inferring wake times or adding cancellation controls. They are metadata
observations, not session-stream lifecycle events.

The browser and native app consume the shared journal. There is no separate
per-viewer provider stream in the dashboard.

## Catalog reads

Snapshot assembly reads saved catalog identity and batches aliases and queue
counts. It does not call provider discovery or launch a process for each row.
Background discovery reads configuration once and gathers Claude process
presence in one process-list call. Queue counts include every pending and
in-flight message.

Snapshots accept `scope=running|current|recent|all|archived` (defined in
`projects.md`), `projectId`, and `q`.
Optional `limit` (1–200) returns `nextCursor`; pass it as `cursor` with the same
filters. A changed catalog or snapshot returns `409 catalog_changed` rather
than silently skipping or repeating rows. Cache entries are invalidated by
catalog changes and state-relevant events, not transcript output.
