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
`error.code` set to `history_gap` and the retained `earliestPosition`/`latestPosition`.

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

Every newest-page read starts the shared session source. A streamable session
whose journal was seeded before is served from the journal at once; what the
source then catches up gets later seqs and reaches the client on the session
stream it opens after the page's `journalSeq`. A journal written by an earlier
item derivation is not: the source's first pass rebuilds it, dropping rows the
stream has no removal for, so its page waits for that pass. Any other newest-page read waits
at most two seconds for the source's first pass, and a cold journal is seeded
by that pass. A seeded journal backed by a transcript (Claude, and Codex with a
local transcript) catches up by reading the transcript newest-first until it
meets the journal, so turns written while no source ran appear after it.
A Claude journal also keeps a transcript checkpoint in
`session_journal_state.transcript_checkpoint`: the byte offset and file
identity up to which it holds the transcript's items. The seed, each catch-up,
and each live stream window record it after the items they appended. Once a
journal write of a source has failed, that source clears the checkpoint at
each of these points instead, so the next source pages back to the journal and
appends what was lost. A source that starts
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

A Codex revert continues the thread in a new rollout whose
`session_meta.history_base` names the rollout it continues and where that
file's retained history ends. Discovery records the new rollout as the
session's transcript. Reads follow `history_base` back through each rollout,
verifying the thread id and offsets, and page the retained bytes as one
history, so provider cursors cross rollouts; a compressed `.jsonl.zst`
ancestor is read through a decompressed copy in the user cache directory. A
source, running or starting, accepts the new rollout when its lineage reaches
the rollout the journal last trusted: it journals the retained items it had
not seen and removes the reverted ones it had. A catch-up that reads the
thread back to its start clears the older-history boundary recorded against
the earlier rollout. When the lineage cannot be traced, the source rebuilds
the journal in one transaction from a fresh read of the new rollout, with a
`notice` item saying it was reloaded, removes the rows that read does not
hold, and starts older history at that read's cursor.

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

## Sent prompts

`GET /api/v1/prompts` (scope `read`) lists what a person typed into this host's
sessions, newest first, for composer history and prompt search. It takes
`sessionId` (omit it for every session), `q` (a literal substring, at most 200
characters), `limit` (1 to 200, default 50) and `before` (the `nextBefore` of
the previous page). The response is `{"prompts":[{sessionId, itemId, turnId,
ts, text}], "nextBefore"}`; an empty `nextBefore` is the end. `?host=` and
`host::id` session ids are routed like other per-session reads, and each daemon
answers only for its own sessions, so a client searching every computer asks
each host and merges by `ts`.

A prompt is a user message the journal recorded from a person: messages from
peers, task notifications, injected context and continuation summaries are
left out, and so are subagents, delegated sessions, sessions an agent started
and sessions mirrored from another host. A request scans at most 2000 journal
rows; when it stops short of `limit` it returns the cursor it reached, so a
rare `q` match may take several pages.

The prompts come only from the journal, and the journal records a session
only once something reads it: a session page read, a session stream, a
voice call, or a watch or relay that holds it. A session nothing has read
since the journal was added, such as a closed terminal Claude session or an
older Codex thread, has no sent prompts until then.

## Session files

`GET /api/v1/session-files?id=<session>` (scope `read`) lists file and
directory names under the session's working directory for `@` completion in
the composers. It takes `q` (matched with fzf's scorer, case-insensitive) and
`limit` (1 to 200, default 50); with no `q` the shallowest paths come first.
The response is `{"files":[{path, dir}], "truncated"}` with paths relative to
the working directory. `?host=` and `host::id` session ids are routed like
other per-session reads, so the files of a session on a paired host are listed
on that host.

In a git checkout the list is what `git ls-files --cached --others
--exclude-standard` prints, so the checkout's ignore rules apply, plus the
directories those paths imply. Elsewhere it is a walk that skips hidden
directories and `node_modules` and never follows a symbolic link. Either way
the listing stops at 50,000 entries or after 2 seconds and then sets
`truncated`. A listing is reused for 10 seconds unless the checkout's git index
changes, concurrent requests for one checkout share a single listing, and only
the 8 most recently used checkouts are kept. Only names are returned, never file contents. A session with no
working directory, or one that is not on this computer, answers `404` with
`no_cwd`.
