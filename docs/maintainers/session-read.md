# Session pages and metadata

`GET /api/v1/session?id=<session>&timeline=1&limit=10` returns the recent
session journal page. `limit` counts exchanges, between 1 and 40 (default 10).
An exchange is one turn: the prompt that opened it (a message with role `user`
or `peer`) and the turn's last assistant message, with the prompt's timestamp.
A reply from a different turn, or one before any prompt on the page, forms an
exchange without a prompt. `timeline.items` holds every item of those turns, so
the page starts at the oldest included prompt; a page holds at most 400 items,
and a longer turn starts mid-turn. `exchanges` and `timeline.items` describe
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
Requesting it reads one bounded provider page through the session's shared
source, serialized with its seed and refresh reads, and returns those items
with `readSource` set to the provider source and the next provider cursor. The
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
presence in one process-list call. Queue counts exclude expired pending work
without performing expiration writes.

Snapshots accept `scope=all|recent|current|running`, `projectId`, and `q`.
Optional `limit` (1–200) returns `nextCursor`; pass it as `cursor` with the same
filters. A changed catalog or snapshot returns `409 catalog_changed` rather
than silently skipping or repeating rows. Cache entries are invalidated by
catalog changes and state-relevant events, not transcript output.
