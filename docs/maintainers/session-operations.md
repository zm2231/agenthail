# Session creation, lifecycle and Codex native controls

Agenthail exposes these operations through the CLI and the authenticated dashboard API. The web dashboard has creation controls, model, effort and mode chips, Codex service tier and output schema, and a session operations form. The native iPhone companion supports Claude background creation with name, worktree, named-agent, model, effort and permission options, plus ordinary session and Agenthail queue controls. The Mac app has Claude background lifecycle, Codex forks, native Codex queue editing, and model, effort and mode chips; see [native apps](../native-apps.md). On iPhone, use the web dashboard, Mac app or CLI for those.

The immediate send status in CLI JSON and dashboard/mobile API results is `sent`, `queued`, or `submitted`, followed by the target. `sent` means the selected transport accepted the request, `queued` means Agenthail durably accepted delayed work, and `submitted` means Agenthail recorded the intent without asserting provider acceptance. Human/API sends without an agent sender use the durable operator identity. Channel sends count `submitted` members separately from `sent` and `queued`. Receipt copy does not ask the sender to investigate read or confirmation state. Success is silent; proven delivery problems appear as notices. Internal audit evidence retains `transport_accepted`, `held`, `delivered`, `reply_observed`, `failed`, `unknown`, and `canceled`. Socket acceptance never proves model completion. A receipt for a message delivered now carries `deliveredAs`: `note` when it took the native peer socket, so the model reads it as a note from Agenthail, and `turn` for every other route; it is the route the send actually took, so a Remote Control attachment found gone mid-send reads `note`. A queued receipt carries none, because the route is chosen when the message is delivered.

`agenthail list` renders the daemon's session view and needs the daemon; with
the daemon stopped it returns an error. The daemon owns the scope, counts,
ordering and each row's status text: `current` by default, or `--recent`,
`--running`, `--all` (every catalog family that is not archived) or
`--archived`. Counts are reported as conversations (families) and sessions, so
a subagent appears with its root; a parent shown only because a child matched
is marked "Parent of a match". The PROJECT column and the JSON `project` field
are the daemon's project for the session, and `--project <name|id>` or
`--cwd <path>` keeps one project; a name shared by several projects is an error
that lists their ids. See [projects](projects.md) for the identity and archive
rules.

`agenthail list --json` returns the view (`view.scope`, `view.counts`,
`view.families`, `view.revision`) and the `sessions` it lists, each with a
`display` object (`kind`, `label`, `detail`, and UTC instants such as
`resetsAt`). Surface health is reported by `agenthail doctor`, not by `list`.

`agenthail project list [--all]`, `project add <path>`, and
`project <archive|unarchive> <name|id>` manage projects. `project list` names
each project's coordinator in its LEAD column.
`agenthail thread <archive|unarchive> <target>` archives one session. Both route
to the host that owns the project or session.

`agenthail list --project <name|id>` lists one project's sessions on every
host. `agenthail list --cwd <path>` lists the project a session working in that
folder would belong to, resolved the way the daemon resolves sessions (see
`projects.md`): a session in a linked worktree counts for its project wherever
the worktree lives, and a folder no project holds is an error. Both need the
daemon and cannot be combined. `queue list` takes the same two filters for its
targets. `--wide` prints each full normalized workspace. JSON always retains
the complete `cwd` field. `--cwd` narrows the list; it never selects a caller
identity.

`agenthail last <target> [count] --timeout 30s` and
`agenthail reply <target> --timeout 30s` use one bounded session reader. The
newest page is returned first, text and JSON identify the source, and JSON
includes `nextBefore`. `count` is between 1 and 50. Text output gives each
exchange's time and who sent its prompt: `you`, `from <agent>`, `task
notification`, or the provider's label. A page holds at most `count` exchanges and the activity
recorded alongside them, and `nextBefore` addresses the record before the oldest
exchange on the page, so `--before <nextBefore>` reads the preceding page with
no gap. Past the oldest journal entry, the cursor continues into the provider
history recorded at seed time.
`--max-bytes N` cuts each prompt and reply after the read, at a character
boundary, and records the original size; `--report` replaces the text with the
parsed completion report (`internal/report`) and cannot be combined with it.
Both run in the CLI on whatever page the reader returned, so the daemon page,
paired-host reads and offline provider reads behave the same.

Each exchange carries `turn` (`surface.TurnMetadata`, version 1). The done item
that ends a turn carries what the provider recorded, through the journal like
any other field, and `surface.BoundSessionRead` gives an exchange that no done
item ended `running` (the newest exchange of a busy session) or `unknown`.
`unavailable` names each field the provider did not record.

- Codex: a meter reads the rollout. `turn_context` gives the model,
  `task_complete.duration_ms` the duration, and `token_count.last_token_usage`
  each request's tokens. Codex repeats a `token_count` without a new request; a
  record that leaves `total_token_usage` unchanged is not counted again. A
  managed session read from the app server takes the rollout turn with the
  same turn ID, and the app server's turn status otherwise. A turn whose
  rollout has no end record is `interrupted` at the next turn's `task_started`,
  with the usage and compactions the meter counted for it; its done item sits
  on that start record and carries the cut-off turn's ID.
- Claude: the turn tracker sums each assistant message's usage, from the
  message's last record, since earlier records hold partial counts.
  `inputTokens` adds `cache_read_input_tokens` and
  `cache_creation_input_tokens` to `input_tokens`, so both providers count
  input the same way. `durationMs` is the wall time from the prompt record to
  the turn's last record. `contextTokens` is the last assistant record's input
  (`<synthetic>` records are not requests and are skipped),
  or the `postTokens` of a compaction after it. Each `compact_boundary` gives
  a compaction: `compactMetadata.trigger` `auto` is `threshold` and `manual` is
  `manual`, and an informational record starting "Compacted while idle" or
  "Compacted after an hour idle", which Claude writes just after the boundary,
  makes it `idle`. The compaction summary Claude writes as a user record
  neither starts a turn nor is a prompt; it shows as a `Compaction summary`
  event. A turn ends at `turn_duration`, or at the first record after its
  `end_turn` block that is not another block of the same message. When
  Claude's last reply in a turn is empty it writes no block, and the turn
  ends at the main thread's `stop_hook_summary` if the Stop hooks let Claude
  stop; a blocking Stop hook leaves its feedback in `hookErrors` and the turn
  goes on, its "Stop hook feedback" record staying inside that turn rather
  than starting another. A turn Claude replied in that is still open when the next prompt
  starts ends at that prompt, with its usage and compactions.
- Codex compactions: a `compacted` record is one compaction. `preTokens` is the
  size of the request before it and `postTokens` the next `token_count` that is
  smaller; `contextTokens` is the last `token_count`'s `total_tokens`. Two
  compactions with no `token_count` between them leave the size between them
  unrecorded, so the first has no `postTokens` and the second no `preTokens`.
  The rollout does not say why Codex compacted, so `trigger` is `unknown`.
- Notion: nothing is recorded, so every field is unavailable.

A reader that starts mid-turn, a page or a stream window, replays from the
record that started the turn before it, at most 64 MiB back, so it holds the
compactions since that turn ended and the context size they started from. A
turn that started further back reports usage and model as unavailable rather
than a partial count, and a turn whose predecessor is out of reach reports
`compactions` as unavailable.

These numbers are accounting inputs, not a bill. They omit subagent turns,
which run in their own transcripts, and a request cancelled before the provider
recorded its usage. They do not price cached or cache-written tokens. Claude's
duration includes tool runs and permission waits; Codex's is the provider's own.
With the daemon running, phone detail, `last`, and `reply` read the same bounded
per-session journal page. CLI JSON includes `journalSeq`; an active-daemon read
error does not fall back to a second provider reader. Metadata loads independently.
The shared session source seeds and updates the journal rather than each viewer
reading the provider. Only when the daemon is offline does the CLI use the bounded
provider reader: Claude reads its local transcript, and Codex tries its native
RPC then its local transcript. The source and any read warning remain explicit.
A read failure never resends a message.

The shared session source writes a retained journal for each session.
`/api/v1/session-stream` replays that journal using its session cursor;
`/api/v1/catalog-events` publishes catalog changes using a separate catalog
cursor. `/api/v1/events` retains the general event view. None of these viewers
starts a separate provider poller. See [session stream and catalog](session-stream-catalog.md)
for paging, replay, gaps and source lifetime.

Busy-target behavior is explicit: `send` delivers immediately when idle and follows
the persisted `busyDelivery` setting (`queue` by default, or `steer` when the
target advertises steering) when busy. A settings file that can't be read means
`queue` until it is fixed, never a stalled queue; the daemon logs it and `doctor`
names the file. A steer-policy target without steering capability is queued instead. An agent's message to a Claude session with a
native peer socket is the exception: the socket delivers it to a running or
waiting turn with the sender's name and reply address, and Claude holds it in
its own queue until the turn can read it, so it is sent at once whatever the
policy. Only an explicit `queue`, or a message with per-message options, waits
for that turn to end. `send --no-queue` refuses delayed delivery, while
`queue` always creates the durable pending item and `steer` is an explicit active-
turn control. The setting is exposed by the dashboard/API settings endpoint and
the existing dashboard settings write path. Read-only targets fail before
dispatch. An unknown delivery outcome must be inspected before an explicit retry;
it is never resent automatically. A queued message records only a per-message
choice (`queue`, or `steer` for a steer held behind a question). One without a
choice, such as a relay, follows the setting as it stands when the daemon drains
it, so changing the setting applies to messages already waiting.

A busy target can be waiting on its user. `surface.Waiting` on the session and
on its turn observation carries the kind (`question`, `approval`, `input`,
`dialog`), a stable id and when it was first seen. Claude reads it from the
live process's record, `sessions/<pid>.json`: `"status":"waiting"` with
`waitingFor` as Claude itself classes it. `input needed` (`AskUserQuestion`, an
MCP server's question) is kind `question`. `permission prompt` (a plan approval
or a tool permission), `sandbox request`, `worker request` and `goal proposal`
are kind `approval`, as is any value Claude adds later. `dialog open` (a panel
open in the terminal, such as `/model` or a settings review) is kind `dialog`:
only the terminal can settle it, so every client shows where to settle it even
in a session that takes answers through Agenthail. `statusUpdatedAt` is when it
started. The id is
`<conversation>:<kind>:<statusUpdatedAt>`. A session the
[Claude host](claude-host.md#requests) runs reads it from the host instead: its
oldest open request, under Claude's own request id. Claude 2.1.303 writes a pending tool
call to the transcript only once it resolves, so the transcript cannot show an
open question. Codex reads it from the thread status, `active` with
`waitingOnApproval` or `waitingOnUserInput` in `activeFlags`, and names it
`<thread>:<kind>` in both listings and observations. The registry keeps the
current question in `session_runtime`. Each read of a turn and the write of what
it waits on happen as one step per session, so a slower read never overwrites a
newer one. A catalog row is published inside that same step, so an answer
recorded while a row is being written waits for it and then asks for a fresh
discovery pass. A catalog listing that disagrees with the record is confirmed by
observing the turn before anything changes. Each new question is announced on
the desktop and to paired devices (`session.waiting`) and marked announced once
both were attempted; a question recorded but not yet announced when the daemon
stops is announced after it restarts. Both notifications carry a key derived
from the session and question: the desktop notification identifier and the APNs
`apns-collapse-id`, so an announcement repeated after a stop replaces the one
already shown rather than adding a second. When the session's provider takes
answers through Agenthail, the announcement reads the open request at that
moment. The desktop notification shows its prompt, the first question's for a
request with several, and its buttons: `allow`, `always`, `deny` and `reply`
for an approval, with `never` (always deny) and `stop` (deny and end the turn)
where the request offers them and no `reply` where the provider keeps no reason
with a denial, as Codex does; or `o0` to `o3` for a single question's options
and `reply`.
Apple documents only that a banner shows the first two actions; what the
expanded view shows of five is checked on a device. Several questions, or one
that takes several answers, get no buttons, since a notification answer
carries one choice or one line of text; opening the notification opens the
session with the request above its message box. The push stays generic and
adds only the request ID; the phone fetches the prompt and buttons from
`GET /api/v1/requests`, which returns each open request with `prompt` and
`actions`. It answers for every session whose provider takes answers through
Agenthail, with an empty list when none is open, as for a terminal session
right after its question was answered. A button answers with `respond` and
`decision.choice`, plus
`decision.message` for `reply`, which answers a question with that text or
denies an approval with it as the reason. A choice is resolved against the
request as it is then: one it no longer offers is refused, and an answered
request is `stale`. `respond` returns once the session's catalog row no longer
shows the request it answered. While a target waits, nothing is
steered into it, and a peer socket message is not held: Claude keeps it queued
behind the question or prompt without answering or dismissing it. `send` under
the `steer` policy to a target without a peer socket, and `steer`, queue a steer,
with a receipt that names the wait. The daemon steers queued steers once the
question is answered and delivers other messages when the turn ends. Queue rows
report `heldBy`: `waiting`, `busy`, `offline`, `unknown`, `refused`,
`retrying`, `notLoaded` (an unloaded conversation that only Codex Desktop can
load), `hostUnreachable` (a send a paired host has not received), or `behind`
when an earlier message to the same session is still open. `offline` covers a
target discovery no longer reports; a closed Claude session is the exception,
since the daemon resumes it to deliver its queue. `refused` means the
target refused to start, such as Claude failing to resume the conversation in
the Claude host. The row's last error carries Claude's message, the row raises
an attention item, and the refusal is announced once on the desktop and paired
devices; an agent sender gets a notice with the reason. The message stays
queued and is delivered on the first attempt after the cause is fixed. Empty means
the next scan claims that message: a drain takes only a session's earliest open
message, or its earliest steer while a turn runs and the target can steer.

## Claude sessions

```sh
agenthail thread create claude "Investigate the failing build" --cwd /path/to/repo --alias investigator --name investigator --effort high --permission-mode plan --json
agenthail thread status @investigator --json
agenthail thread stop @investigator --json
agenthail thread resume @investigator --json
```

Creation opens a new conversation in the [Claude host](claude-host.md) and sends the message as its first turn, a person's turn under the delivery intent's uuid. The host session has no Remote Control. Its tool approvals, questions and MCP servers' requests for input wait for a person: `agenthail requests <target>` lists them, `agenthail answer <target> allow|always|deny|never|stop|"answer"` answers the oldest (`never` and `stop` only where the request offers them) (or `--request <id>`), and the dashboard shows them above the composer. An MCP form is answered with `allow --answers '{"field":value}'`, a link with `allow` once it is open. Answers go through the `respond` action, and a request already answered or expired is a conflict (`stale`). `agenthail permission-mode <target> <mode>` and `agenthail model <target> <name>` change a running host session; both take the same `control` scope as creating a session, and each change is recorded in the session's history with who made it. The daemon must be running, since it runs the host; without the host nothing is created. The `claude-bg` launcher still starts a `claude --bg` background job when a client selects it. Optional flags are `--model`, `--name`, `--worktree <name>`, `--agent <name>`, `--permission-mode` and `--effort`. The working directory defaults to the caller's directory. Supported permission modes are `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk` and `plan`; Claude effort values are `low`, `medium`, `high`, `xhigh` and `max`. Model availability and native permission behavior remain Claude's responsibility.

A Claude CLI in a terminal is answered through its `PermissionRequest` hook. `agenthail hooks install` merges one entry (`matcher` `*`, command `exec '<agenthail>' hook permission-request`, timeout 86400) into `settings.json` of every root `surface.ClaudeConfigRoots` returns, by path with gjson/sjson so every other key keeps its text; the same pass adds the session-context hooks ([agent card](agent-card.md#telling-a-claude-session-its-card)). `hooks uninstall` removes only Agenthail's commands, and `hooks status` and `doctor` report each root as installed, missing, or outdated (a hook written without `exec`, with another timeout or under a narrower matcher, or only some of Agenthail's hooks; `hooks install` updates them in place). `hooks install --remote-control on|off` also sets `remoteControlAtStartup` in each root's `settings.json` (`surface.WriteClaudeRemoteControl`, same read, atomic write and mode as the hooks; a file that is not a JSON object is refused); `GET`/`POST /api/v1/claude/remote-control` (settings scope) reads it per root and sets one root for the Mac app. Both refuse to set it when the Claude surface has Remote Control disabled. Claude runs hook commands with `/bin/sh -c`, and the hook takes its parent pid for the Claude process; `exec` matters because dash, `/bin/sh` on Debian and Ubuntu, does not replace itself with a lone command, so without it the hook's parent would be the shell. Claude runs every matching hook in parallel and takes the first decision, so the terminal dialog, Remote Control, any other `PermissionRequest` hook (cmux installs one, for example) and Agenthail's all stay live. The hook posts its input and its parent pid to `POST /api/v1/claude/permission-request`, which accepts only the local dashboard token. The daemon holds the prompt only for a live `kind: interactive`, `entrypoint: cli` record whose pid matches, so SDK-host and `claude --bg` sessions get no decision and fall through to their own handling. A `claude-cli` session whose config root has the hook installed offers `respond` whether or not a prompt is held (`claudehooks.StateAt`, read again only when that `settings.json` changes), since clients read capabilities at different times and one that read them before the prompt must still show its card; `waitingOn.answerable` says whether the current wait can be answered. While held, the session waits on the request's own `<session>:<kind>:held:<run>-<n>` id, where `<run>` is random per daemon run so a prompt held after a restart never takes the ID of one clients already cached, and `requests`/`respond` work as for a host session. The hold ends with the person's decision, or with no output when the record shows the prompt settled (a new `statusUpdatedAt`, or the process gone) or Claude exits. A stopping daemon drops each held connection without a response (`http.ErrAbortHandler`), since an empty 200 would read as settled; while the daemon's launchd or systemd service is loaded, the hook posts the same prompt again every 2s until a daemon answers, Claude exits or Claude ends the hook, so a prompt held across an update is answered through the new daemon. It gives up with no output once no daemon has held the prompt for `claudePromptHookUnheldLimit` (2 minutes), as when the service crash-loops or runs a missing binary; a call that fails within 2s does not count as held. With no service loaded, nothing would start a daemon again, so a failed connection returns no output at once. Claude shows its own dialog while the hook runs, so either way the prompt stays answerable in the terminal. The hook sends `askedAt`, when it read the prompt, and the daemon dates the request from it; a re-posted prompt whose session record now shows a wait that began more than `claudePromptRecordWait` later is refused with 409, since the session moved on to another prompt while no daemon held this one. The record keeps showing an answered prompt until Claude acts on it; that wait is dropped. Whenever the recorded wait changes or clears after being announced, the daemon withdraws its notifications: the desktop helper's `withdraw` command, and a background push through the relay's `/v1/withdraw` that the iPhone app matches by `collapseId`.

Model discovery and background lifecycle commands share executable selection and environment: explicit `AGENTHAIL_CLAUDE_BIN`, then `claude` on PATH, then the native installation at `~/.local/bin/claude`. An invalid explicit choice fails without choosing another installation. `agenthail daemon install` saves the explicit choice in its launchd configuration. Native installation discovery works when macOS launches Agenthail without the interactive shell's PATH. These commands run without the caller's `CMUX_` variables. Inside a cmux terminal, cmux's `claude` wrapper otherwise injects a temporary `--settings` file and an `--mcp-config` that Claude saves as the job's respawn flags; after a reboot the file is gone and every resume fails before init.

Use `@alias` or `claude:<session-id>` as a target. The `claude/name` transcript heading is a display label, not a routable address. Read the session's `id` from `agenthail list --json`; do not construct an ID from a truncated table label. Duration arguments require units, such as `--timeout 8s`.

For native messaging, Agenthail resolves the session first, then reads its PID registration and selects `messagingSocketPath`. It verifies session identity, a live PID, the expected `/tmp/cc-socks/<pid>.sock` path, socket ownership, and the process-start identity. Agenthail's verifier and peer registrations use `LC_ALL=C TZ=UTC`; native Claude records from 2.1.267 onward use UTC, while older records use local time. A stale process identity is rejected. Relays store canonical session IDs and use the shared queue and adapter delivery path; they do not retain a socket path across process changes.

Socket messaging and Remote Control have different capabilities. Native socket-only sessions support messages and transcript inspection, but not compact, model switching, interrupt or steering; a socket message to a busy session still reaches its running turn at the next tool result. An agent never steers a Claude session or posts to it over Remote Control: its steer goes to the socket, and an agent's message to a session with no socket waits in the queue. `delivery.Dispatcher.MaySteer` lets an agent steer only a session it leads or started (`coordinator.Service.Oversees`), in the CLI and the daemon alike; anyone else's steer is queued as a message whose receipt says so. Sessions with a Remote Control attachment can use those typed controls even when ordinary messages use the native socket. The `control` commands (context, usage, status, effort and rename) also need Remote Control, and a session's `commands` capability says whether it has them, apart from `model`: a host session changes its model through the host but has no `control` commands. An install without Remote Control advertises none of these controls for sessions outside the host. Controls are never encoded as queued slash-text messages. Status, stop and resume of a host session go through the host; a `claude-bg` job uses the Claude CLI lifecycle interface, which also has logs. Socket delivery is not evidence that a Remote Control operation works, and queue acceptance is not proof that Claude consumed the message. Composer messages from the web dashboard and native apps declare `"author": "person"` and use the Remote Control attachment when the session has one, so they arrive as the user's own turn with `delivered` rather than `transport_accepted` evidence.

Claude compact requests are stored as typed queue operations. The daemon waits
for the target to become idle, invokes `/compact` through the Claude host or
Remote Control, and
marks the queue row delivered only after the local transcript records a new
`compact_boundary`. If the daemon loses confirmation after submitting the
command, the row becomes `unknown` for operator review instead of being retried
as either a control or a message.

### Idle compaction

Claude's prompt cache lasts an hour when a request asks for it; a prompt after
it expires writes the whole context into the cache again. `Daemon.compactIdle`
(`internal/daemon/idle_compaction.go`) runs every minute and queues a compact
for each catalog Claude session where all of these hold:

- the session is idle, not ended, waits on nothing and runs no background task;
- `Claude.IdleCompactionState` reads the transcript tail: the last main-thread
  assistant record (sidechain and `<synthetic>` records skipped) is 50 to 60
  minutes old, the newest cache write was to the one-hour cache
  (`cache_creation.ephemeral_1h_input_tokens`), its input, cache-write and
  cache-read tokens add up to at least the root's threshold, and no
  `compact_boundary` follows it;
- the session's config root has idle compaction on (`claudeIdleCompaction` in
  `~/.agenthail/config.json`, keyed by root, on at 200000 tokens by default)
  and no session under the root shows a usage limit that has not reset, since
  the account would refuse the compact;
- the session's capabilities include compact: a host session or one with a
  Remote Control attachment. A terminal session without one cannot compact.

`Registry.QueueIdleCompaction` writes the `idle_compactions` row for the
session and request time with the queue row, once per request, and queues
nothing when the session already has a queued or in-flight item. The queue
delivers it like any compact. The compact's request uuid is stored on that row,
and `sessionSourceManager.publishedTurn` relabels the compaction whose
`/compact` record has that uuid from `manual` to `idle` wherever a turn is
journaled or read from history; the transcript itself records a manual
compact.

The transcript rules are checked first. A session whose transcript shows a
due request but that is held by any other rule has the first one recorded
once for that request: the daemon logs `idle compaction held for <session> at
<tokens> tokens, idle <minutes>: <reason>`, and `GET
/api/v1/claude/compact-settings` lists the latest 50 holds as
`idleCompactionHolds` (`sessionId`, `session`, `root`, `requestAt`,
`contextTokens`, `reason`, `at`), with `root` given as the configured root it
resolves to and `session` as the session's display name. The dashboard's
compaction settings show the latest five under each root, and `agenthail claude
settings compact` prints them under each root, saying so when the daemon can't
be reached. The reasons, in the order they are checked: off for the root, not
idle (a status that never left busy shows here), waiting on a prompt, a
background task running, the root at a usage limit, no compact for the
session, and a queued or in-flight item. The holds live in the daemon's memory
and start empty when it restarts.

Each transcript changed in the last hour is read on every pass, held or not,
so the reads are incremental: `Claude.IdleCompactionState` continues from the
offset the previous pass reached and folds in only the records appended since,
so a pass reads what was written in the minute before it. The offset belongs to
the file it was read from (device and inode), and the appended bytes are read
from the same open file that was measured. It reads the last 4 MiB afresh on
the first pass, when the transcript was replaced or shrank, or when more than
that was appended.

For a `claude-bg` job, Claude assigns the background ID. Agenthail parses that ID from the native launch response, then resolves the full session ID through `claude agents --json --all`. It never assumes that a supplied `--session-id` controls background identity. The registered session and optional alias become the targets for later messages. A successful launch confirms registration, not completion of the first model turn; there is no fabricated turn receipt.

Claude Code lets one conversation stay open in several processes; resuming it elsewhere only warns. The conversation has one row whatever its processes: the process that opened it first is attached to the row, sends to the session reach it, and a later process is addressed by its PID. When a process moves to another conversation (`/branch`, `/clear`, `/resume`), registering it under the new conversation detaches it from the old row; nothing else moves. Commands run from either process still resolve to that session. Because a sender's process is unknown when several share its ID, replies to it are addressed to the session rather than to one process's socket.

Status, logs, stop and resume operate on sessions the host runs and on `claude-bg` jobs. A `claude-bg` job's logs are Claude's background log; a host session's logs are the last 4 KiB its Claude process wrote to stderr, which the host keeps while the session is open; the conversation itself is read with `agenthail last`. Interactive sessions do not acquire background lifecycle controls merely by appearing in discovery. Each session row reports which of these its runtime supports in `capabilities.lifecycle` (`status`, `logs`, `stop`, `resume`): all four for a `claude-bg` job and for a host session, and none for an interactive one, which a person opens in its terminal instead. The same object's `adopt` says the session can be opened in the host with `session-adopt`: set for a terminal session and a `claude-bg` job, not for a session the host already runs. Like the other flags it follows the runtime: a conversation adoption cannot open, such as one with no transcript or with transcripts in two config roots, is refused when it runs, with the reason. Clients show each control only where its flag is set, and offer **Open in Agenthail** only where `adopt` is. A resume registers the process it reached, from the dashboard and the CLI alike: a `claude-bg` job that has ended resumes in the host and is listed from then on as a `claude-host` session. Destructive removal is not exposed.

### Delivering to a closed Claude session

A Claude conversation is identified by its session ID, the name of its transcript `projects/<project>/<session-id>.jsonl`. A background job ID is that ID's first eight characters, a PID lasts one process, and a Remote Control session ID lasts one registration. Registry rows are keyed by the session ID; the PID, messaging socket and Remote Control ID are attachments read from the live process record whose `sessionId` matches. So an alias follows the conversation through any number of resumes, and a `/branch` gets its own row (with `forked_from`) while the alias stays on the original. Rows created before this keying were moved to their conversation ID in one schema step; `session_rekeys` records each move, `/api/state` returns it as `rekeyed`, and the catalog announced each move as `session.removed` with `rekeyedTo` so the apps move open panes and drafts to the new ID.

`Claude.reach` makes a session reachable before anything is posted into its conversation. `Claude.Send` and `Claude.SendHumanTurn` call it, so whichever route `delivery.RouteFor` picks, every message sender does: CLI `send`, dashboard and app sends including a person's composer turn, channel sends, voice delegation, and the daemon queue that carries relays and queued messages. A resumed conversation runs in the Claude host, which takes a person's turn under its delivery intent's uuid; agent messages take the resumed process's native socket. Steer and interrupt act only on a running turn, so they never resume. `/compact` and `/model` act only on a running process, so they never call `reach`: a host session takes both through the host, and otherwise both need a running process with Remote Control and report unsupported without one. Voice selects its target from the registry by alias or exact session ID and refreshes it from the surface's live session for that exact ID. A surface whose sends wake a closed session, as Claude's do, takes the registered row when the session is not live, so a closed Claude session can be selected and a delegation wakes it, showing "Waking up" while it does. The reply stream outlives the request that started the delegation. A delegation is the person's turn. Voice speaks the reply only when the send kept the session ID its stream was opened for and the person's route to the session carries turns; a resume in the host changes the session's transport but not its ID, so voice follows the resumed session's reply. Otherwise the request is delivered and voice says it cannot follow that session's reply. `reach` uses only Claude's own records:

- A live process whose session record has a validated messaging socket or a Remote Control ID: the message goes to it, and the session row is re-registered if its process changed.
- A live process for the conversation without either, or a background job that is still starting and has no record yet: the delivery is held. A send queues it, and the queue retries it until the process can take messages, or until it exits and the conversation is resumed. No second process is started.
- No process: the Claude host opens the conversation with `resume` in its working directory (the job's, else the session's, else the transcript's), without a prompt, so only the sender's message reaches the conversation. A terminal or background session resumes this way too and becomes a host session without Remote Control. The send then waits up to 10 seconds for the host's process to be listed. An agent message to it takes the native socket, so `send --reply` and `--stream` deliver it and say that its reply is read with `agenthail last`. Without a running host the delivery is held.

Resumes of one conversation are serialized across Agenthail processes by `~/.agenthail/claude-resume/<session-id>.lock`, held from the liveness check through the wait, so concurrent senders resume once and each message is delivered once. The host opens a conversation by its id, so it cannot start a copy. A process from an earlier host that still runs the conversation holds the delivery. A Claude that exits with an error before it starts has refused the resume: the delivery fails with `Claude refused to resume <id>: <Claude's output>` and nothing was sent. A Claude that cannot be spawned or does not start in time is unavailable and retried. A resume whose answer from the host is lost, or that the host opened but did not list in time, is unknown: the delivery stays `submitted`, a queued item is dead-lettered as unknown, and nothing is retried.

Observation reads a session whose process exited as idle when no other process holds its conversation, so its queue drains into a resume, and as offline while another process, including a starting job, holds it. A target resolves to a registered session that discovery no longer lists only when it names that session exactly: an alias, a session ID, or `surface:<id>`. Name and folder fragments never resolve to historical sessions. `agenthail thread resume` uses the same path without sending a message. For a host session, `thread status` reads the host's listing, `thread stop` closes the session in the host, and `thread logs` returns its Claude process's stderr while it is open; a closed session's logs are refused, so read the transcript with `agenthail last`.

If creation returns a session, that session is registered before the initial-turn result is reported. An ambiguous initial-turn outcome records a durable `delivery_intents` row targeting that session and returns a neutral, non-retryable `submitted` result with its `deliveryId`; no automatic retry occurs. A definitive initial-turn failure records a failed delivery problem and returns a typed failure while preserving the created session. If the session identity is absent, the result is `{ok:false,status:"failed",retryable:false,unknown,error}`, where `unknown` is `true` when the initial-turn outcome is ambiguous and `false` for a definite failure; inspect the native catalog before any explicit retry of an unknown outcome. If the durable intent cannot be recorded, the result is an explicit bounded failure that carries the created session.

## Codex forks

```sh
agenthail thread fork codex:<source-id> --alias experiment --json
agenthail thread fork @builder --last-turn <turn-id> --json
```

Optional `--model` and `--last-turn` pass to the owning app-server; `--last-turn` keeps history through that turn. Over the API, `beforeTurnId` instead forks from just before a turn: the daemon finds the turn the journal recorded before it and passes that as `lastTurnId`, which is how a client offers Fork from here on a sent prompt. Sending both is `400 fork_point_conflict`; a turn the journal does not hold is `409 fork_turn_unknown`, and the first turn is `409 fork_first_turn`. The fork stays in the source folder. A fork retains the source transport, uses the returned identity, and is registered for subsequent sends. Goal continuation is deferred until explicit input; forking alone does not launch an automatic goal continuation. A fork does not create a Git worktree.

## Codex native input queue

```sh
agenthail thread queue @builder list --json
agenthail thread queue @builder list --cursor <nextCursor> --json
agenthail thread queue @builder add "Run the next check" --client-id <stable-message-id> --json
agenthail thread queue @builder update "Run the focused check" --id <queuedSubmissionId> --json
agenthail thread queue @builder reorder --ids <first-id>,<second-id> --json
agenthail thread queue @builder start --id <queuedSubmissionId> --json
agenthail thread queue @builder delete --id <queuedSubmissionId> --json
```

These commands call `thread/queue/list`, `add`, `update`, `reorder`, `start` and `delete`. Responses preserve native fields, including pagination cursors. List fetches at most 100 items. Start without an ID lets Codex choose its next queued submission. Use the exact IDs returned by Codex.

Every add requires a client message ID. Reuse it when retrying the same uncertain submission; use a fresh ID for new work. The dashboard generates an ID and replaces it only after a successful add. Failed or uncertain submissions retain the original ID.

This queue belongs to Codex. Native queue actions do not also create Agenthail outbox entries. Ordinary `send` retains Agenthail's existing durable-delivery behavior. Queue acceptance is not turn completion. Mutation failures after transport dispatch are conservatively unknown; failures before dispatch remain unavailable.

## Codex turn settings and structured output

```sh
agenthail send @builder "Return the findings" --service-tier fast --output-schema /path/to/schema.json --reply --json
agenthail thread create codex "Return the findings" --output-schema /path/to/schema.json --effort high --mode plan --json
```

A session's model, effort and mode are session state. `GET /api/v1/session-metadata` lists them as `turnSettings`, and the `turn-setting` action changes one: `{"action":"turn-setting","sessionId":…,"setting":"effort","value":"high"}`. A Codex thread keeps them and applies a change from its next turn; a Claude session the host runs applies it at once. A `send` (or `coordinator-send`) that carries `effort` or `mode` is refused with 400, and `agenthail send --effort` or `--mode` fails the same way. Creation still takes them for the first turn: effort accepts `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` and `ultra`, and mode accepts `plan` or `default`; mode without a model reads the new thread's model.

When a session compacts its context is listed as `compactSettings` in the same shape, and the same `turn-setting` action changes it. `autoCompact` is `on` or `off` and applies at once to a Claude session the host runs; the host keeps it when it reopens the session. `compactWindow` is `auto` or a token count from 100000 to 1000000. For Claude it applies to new sessions: it is written to the settings of the config root named in `scope`, at the session's model entry when the root already has one and at the root's own `autoCompactWindow` otherwise. A running Claude does not take a new window.

`sessionCompactWindow` gives one Claude session the host runs its own window: `default` (the root's), `auto` or a token count. The host keeps the choice with the session's other settings, which survive a host update and a daemon restart. Once the session is idle, with no open request, background task or owed turn, the host closes it, and the next message reopens it with the window. Until then the setting's `value` is the window in effect and `pending` the one chosen. A reopen that fails leaves the session with its old window and the error names both. A Claude that reports no session states is closed only as one of its turns ends, since its idle is a guess, so there the window applies from the message after the next.

For a Codex thread `compactWindow` is the thread's own `model_auto_compact_token_limit`, with `applies: nextLoad`. Agenthail keeps it in the registry and passes it as config each time it resumes the thread. Codex reads thread config only when it loads a thread that no client has open, so a change makes Agenthail drop its own subscriptions to the thread, resume it with the limit and subscribe again. Another app attached to the thread keeps it loaded, and Codex then takes the limit only on its next load. Codex reports no effective limit, so the setting says it is set, never that it is in effect. A thread in the middle of a turn refuses the change with 400; a resume Codex refuses restores the previous limit and returns the error. A resume Codex does not answer keeps the new limit, since Codex may have taken it, and returns an error that says so.

Service tier accepts `default`, `fast` or `flex` and uses the protocol's `serviceTierForTurn` field. Output schema is an inline JSON object in the API and a file in the CLI, limited to 64 KiB. Agenthail validates that it is an object; Codex validates supported schema semantics and constrains the final response. It does not constrain every tool event or intermediate message.

Service tier and output schema survive the durable Agenthail queue, retries and daemon replay even when no model override is given; they are stored in `message_queue.turn_options`, and queue JSON and dashboard queue state expose them. They are rejected for non-Codex targets; Claude creation separately supports its native effort flag.

## API and output

`POST /api/action` uses the existing dashboard authentication and request protection:

- `session-create`: `surface`, `message`, `cwd`, `alias`, `model`, plus creation and turn fields.
- `send`: `sessionId`, `message`, `serviceTier`, `outputSchema`.
- `turn-setting`: `sessionId`, `setting` (`model`, `effort`, `mode`, `autoCompact`, `compactWindow` or `sessionCompactWindow`), `value`. An old runtime is `409 runtime_too_old` with `runtime` and `minimum`.
- `session-fork`: `sessionId`, `fork: {model, lastTurnId, beforeTurnId}`.
- `native-queue`: `sessionId`, `nativeQueue: {queueAction, message, queuedSubmissionId, clientUserMessageId, queuedSubmissionIds, cursor}`.
- `session-lifecycle-status`, `session-lifecycle-logs`, `session-lifecycle-stop`, `session-lifecycle-resume`: `sessionId`.
- `session-adopt`: `sessionId`, `stopTerminal`. A busy session returns HTTP `409` with `busy`, a terminal session without `stopTerminal` returns `409` with `needsConsent: true`.

Optional fields may be omitted. Fork, queue and lifecycle successes return `{ok: true, result: ...}`; failures return `{ok: false, unknown, error}`. An ambiguous session creation returns HTTP `202` and `{ok:true,status:"submitted",accepted:true,retryable:false,session,deliveryId,detail:"Submitted to <target>."}`; the CLI JSON shape uses the same status fields and exits successfully, while human output is exactly `Submitted to <target>.`. Definitive initial-turn failures return `{ok:false,status:"failed",code:"initial_turn_failed",retryable:false,session,deliveryId,error}` and remain non-retryable. Creation that returned no session reports `{ok:false,status:"failed",code:"create_failed",retryable:false,unknown:false,error}` when it definitely failed, and `{ok:false,status:"unknown",code:"create_unknown",retryable:false,unknown:true,error}` when Agenthail can't tell whether a session started; a client keeps that request's idempotency key and asks the person to check the session list before trying again. A create forwarded to a paired host that never answered is unknown in the same way; it returns HTTP `504` with `{error:{code:"host_outcome_unknown",message}}`. Creation without a durable intent returns `{ok:false,status:"storage_failed",code:"create_storage_failed",retryable:false,session,error}`. None of these is an accepted receipt, and `error` is always a message string. A launcher that started a session Agenthail could not fully record (no location, or a registry write failed) returns HTTP `202` `{ok:true,status:"submitted",accepted:true,retryable:false,launcher,warning}`: the session runs in its terminal and discovery registers it, and `warning` says what was not kept, such as a name that did not bind. Every client shows that warning. CLI fork, queue and lifecycle successes print JSON even without `--json`. With `--json`, operation failures also emit a JSON error object on stdout and return a nonzero exit status; the CLI writes its diagnostic to stderr.

## Verification boundary

The implementation was checked against installed Claude 2.1.263 help/extracted source and Codex 0.153.4 generated experimental app-server schemas. Automated tests cover fake Claude subprocess launch/identity/lifecycle, a local Codex socket for forks and all six queue methods, turn-option protocol mapping, schema migration and queued replay, CLI parsing/output, and dashboard API consumers. These are deterministic integration checks with no paid model calls. Live model execution, production daemon replacement and installation are separate release steps.
