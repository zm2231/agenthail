# Session pages and metadata

`GET /api/v1/session?id=<session>&timeline=1&limit=40` returns the recent
session journal page. `limit` is between 4 and 40; `timelineBefore` requests
items strictly before the returned `timeline.nextBefore` cursor. Items retain
their identity, role, title, status and retained body reference. If retention
has pruned history needed by `timelineBefore`, the endpoint returns `409` with
`error.code` set to `history_gap` and the retained `earliestSeq`/`latestSeq`.

`timeline.nextBefore` is opaque; clients pass it back unchanged and treat `0`
as the end of history. The bounded seed records the provider's older-history
boundary. When a journal page reaches the oldest journal entry and the provider
reported older history, `nextBefore` addresses that provider history.
Requesting it reads one bounded provider page through the session's shared
source, serialized with its seed and refresh reads, and returns those items
with `readSource` set to the provider source and the next provider cursor. The
session open never preloads the whole transcript. A failed older read reports
`timeline.unavailableReason` instead of returning an empty page.

`journalSeq` is the session watermark captured in the same SQLite read
transaction as the page. Open `/api/v1/session-stream?id=<session>&after=<journalSeq>`
to receive subsequent changes rather than replaying the entire retained journal.
An older-page request does not change the active stream cursor.

A cold journal is initialized by the shared session source, with at most two
seconds of waiting for the initial page. The page never calls a provider's
metadata methods. Saved, non-streaming sessions can still initialize their
journal through that source; a warm journal page is served immediately while
the shared source refreshes it in the background. Provider read failures are reported separately
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
