# Session creation, lifecycle and Codex native controls

Agenthail exposes these operations through the CLI and the authenticated dashboard API. The web dashboard has creation controls, Codex turn options, and a session operations form. The native iPhone companion supports Claude background creation with name, worktree, named-agent, model, effort and permission options, plus ordinary session and Agenthail queue controls. The Mac app has Claude background lifecycle, Codex forks, native Codex queue editing, and Codex effort and plan mode; see [native apps](../native-apps.md). On iPhone, use the web dashboard, Mac app or CLI for those.

The immediate send status in CLI JSON and dashboard/mobile API results is `sent`, `queued`, or `submitted`, followed by the target. `sent` means the selected transport accepted the request, `queued` means Agenthail durably accepted delayed work, and `submitted` means Agenthail recorded the intent without asserting provider acceptance. Human/API sends without an agent sender use the durable operator identity. Channel sends count `submitted` members separately from `sent` and `queued`. Receipt copy does not ask the sender to investigate read or confirmation state. Success is silent; proven delivery problems appear as notices. Internal audit evidence retains `transport_accepted`, `held`, `delivered`, `reply_observed`, `failed`, `unknown`, and `canceled`. Socket acceptance never proves model completion.

`agenthail list --json` returns discovered sessions together with an `errors`
object. A failed optional surface is a warning when at least one surface completed
discovery; the command fails only when every configured surface failed. Codex
rows take the local transcript's latest task lifecycle when it is known and the
shared database state otherwise. Claude peer `idle` is trusted only from peers advertising idle
notifications or from a readable transcript; otherwise the state is unknown.

With the daemon running, `agenthail list` reads the daemon catalog and applies
the daemon's scopes: current by default, or `--all` or `--archived`.
The scopes count session families, so a subagent appears with its root. The
table's PROJECT column and the JSON `project` field are the daemon's project for
the session, and `--project <name|id>` keeps one project; a name shared by several projects
is an error that lists their ids. See
[projects](projects.md) for the identity and archive rules. Without the daemon,
`list` discovers sessions directly and prints the working directory in place
of the project. It shows the 15 most recently active session families and
leaves out Notion threads idle for more than seven days; `--all` lists every
saved session; `--project` and `--archived` need
the daemon.

`agenthail project list [--all]`, `project add <path>`, and
`project <archive|unarchive> <name|id>` manage projects.
`agenthail thread <archive|unarchive> <target>` archives one session. Both route
to the host that owns the project or session.

`agenthail list --cwd <path>` retains sessions whose normalized workspace is that
directory or a descendant. Existing symlinks resolve before comparison, and path
components, not string prefixes, define ancestry. `--wide` prints each full
normalized workspace. JSON always retains the complete `cwd` field. CWD narrows
discovery; it never selects a caller identity.

`agenthail last <target> [count] --timeout 30s` and
`agenthail reply <target> --timeout 30s` use one bounded session reader. The
newest page is returned first, text and JSON identify the source, and JSON
includes `nextBefore`. A page holds at most `count` exchanges and the activity
recorded alongside them, and `nextBefore` addresses the record before the oldest
exchange on the page, so `--before <nextBefore>` reads the preceding page with
no gap. Past the oldest journal entry, the cursor continues into the provider
history recorded at seed time.
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
target advertises steering) when busy. A steer-policy target without steering
capability is queued instead. An agent's message to a Claude session with a
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
on its turn observation carries the kind (`question`, `approval`, `input`), a
stable id and when it was first seen. Claude reads it from the live process's
record, `sessions/<pid>.json`: `"status":"waiting"` with `waitingFor` set to
`input needed` for `AskUserQuestion` (kind `question`) or `permission prompt`
for a plan approval or a tool permission (kind `approval`), and
`statusUpdatedAt` as when it started. The id is
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
for an approval, or `o0` to `o2` for a single question's options when there
are at most three, and `reply`. At most four buttons are offered; Apple
documents only that a banner shows the first two. Several questions, or one
that takes several answers, get no buttons, since a notification answer
carries one choice or one line of text; opening the notification opens the
session with the request above its message box. The push stays generic and
adds only the request ID; the phone fetches the prompt and buttons from
`GET /api/v1/requests`, which returns each open request with `prompt` and
`actions`. A button answers with `respond` and `decision.choice`, plus
`decision.message` for `reply`, which answers a question with that text or
denies an approval with it as the reason. A choice is resolved against the
request as it is then: one it no longer offers is refused, and an answered
request is `stale`. While a target waits, nothing is
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

Creation opens a new conversation in the [Claude host](claude-host.md) and sends the message as its first turn, a person's turn under the delivery intent's uuid. The host session has no Remote Control. Its tool approvals and questions wait for a person: `agenthail requests <target>` lists them, `agenthail answer <target> allow|always|deny|"answer"` answers the oldest (or `--request <id>`), and the dashboard shows them above the composer. Answers go through the `respond` action, and a request already answered or expired is a conflict (`stale`). `agenthail permission-mode <target> <mode>` and `agenthail model <target> <name>` change a running host session; both take the same `control` scope as creating a session, and each change is recorded in the session's history with who made it. The daemon must be running, since it runs the host; without the host nothing is created. The `claude-bg` launcher still starts a `claude --bg` background job when a client selects it. Optional flags are `--model`, `--name`, `--worktree <name>`, `--agent <name>`, `--permission-mode` and `--effort`. The working directory defaults to the caller's directory. Supported permission modes are `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk` and `plan`; Claude effort values are `low`, `medium`, `high`, `xhigh` and `max`. Model availability and native permission behavior remain Claude's responsibility.

Model discovery and background lifecycle commands share executable selection and environment: explicit `AGENTHAIL_CLAUDE_BIN`, then `claude` on PATH, then the native installation at `~/.local/bin/claude`. An invalid explicit choice fails without choosing another installation. `agenthail daemon install` saves the explicit choice in its launchd configuration. Native installation discovery works when macOS launches Agenthail without the interactive shell's PATH. These commands run without the caller's `CMUX_` variables. Inside a cmux terminal, cmux's `claude` wrapper otherwise injects a temporary `--settings` file and an `--mcp-config` that Claude saves as the job's respawn flags; after a reboot the file is gone and every resume fails before init.

Use `@alias` or `claude:<session-id>` as a target. The `claude/name` transcript heading is a display label, not a routable address. Read the session's `id` from `agenthail list --json`; do not construct an ID from a truncated table label. Duration arguments require units, such as `--timeout 8s`.

For native messaging, Agenthail resolves the session first, then reads its PID registration and selects `messagingSocketPath`. It verifies session identity, a live PID, the expected `/tmp/cc-socks/<pid>.sock` path, socket ownership, and the process-start identity. Agenthail's verifier and peer registrations use `LC_ALL=C TZ=UTC`; native Claude records from 2.1.267 onward use UTC, while older records use local time. A stale process identity is rejected. Relays store canonical session IDs and use the shared queue and adapter delivery path; they do not retain a socket path across process changes.

Socket messaging and Remote Control have different capabilities. Native socket-only sessions support messages and transcript inspection, but not compact, model switching, interrupt or steering. Sessions with a Remote Control attachment can use those typed controls even when ordinary messages use the native socket. The `control` commands (context, usage, status, effort and rename) also need Remote Control, and a session's `commands` capability says whether it has them, apart from `model`: a host session changes its model through the host but has no `control` commands. An install without Remote Control advertises none of these controls for sessions outside the host. Controls are never encoded as queued slash-text messages. Status, stop and resume of a host session go through the host; a `claude-bg` job uses the Claude CLI lifecycle interface, which also has logs. Socket delivery is not evidence that a Remote Control operation works, and queue acceptance is not proof that Claude consumed the message. Composer messages from the web dashboard and native apps declare `"author": "person"` and use the Remote Control attachment when the session has one, so they arrive as the user's own turn with `delivered` rather than `transport_accepted` evidence.

Claude compact requests are stored as typed queue operations. The daemon waits
for the target to become idle, invokes `/compact` through Remote Control, and
marks the queue row delivered only after the local transcript records a new
`compact_boundary`. If the daemon loses confirmation after submitting the
command, the row becomes `unknown` for operator review instead of being retried
as either a control or a message.

For a `claude-bg` job, Claude assigns the background ID. Agenthail parses that ID from the native launch response, then resolves the full session ID through `claude agents --json --all`. It never assumes that a supplied `--session-id` controls background identity. The registered session and optional alias become the targets for later messages. A successful launch confirms registration, not completion of the first model turn; there is no fabricated turn receipt.

Claude Code lets one conversation stay open in several processes; resuming it elsewhere only warns. The conversation has one row whatever its processes: the process that opened it first is attached to the row, sends to the session reach it, and a later process is addressed by its PID. When a process moves to another conversation (`/branch`, `/clear`, `/resume`), registering it under the new conversation detaches it from the old row; nothing else moves. Commands run from either process still resolve to that session. Because a sender's process is unknown when several share its ID, replies to it are addressed to the session rather than to one process's socket.

Status, stop and resume operate on sessions the host runs and on `claude-bg` jobs; logs exist only for `claude-bg` jobs, and a host session points to `agenthail last` instead. Interactive sessions do not acquire background lifecycle controls merely by appearing in discovery. Destructive removal is not exposed.

### Delivering to a closed Claude session

A Claude conversation is identified by its session ID, the name of its transcript `projects/<project>/<session-id>.jsonl`. A background job ID is that ID's first eight characters, a PID lasts one process, and a Remote Control session ID lasts one registration. Registry rows are keyed by the session ID; the PID, messaging socket and Remote Control ID are attachments read from the live process record whose `sessionId` matches. So an alias follows the conversation through any number of resumes, and a `/branch` gets its own row (with `forked_from`) while the alias stays on the original. Rows created before this keying were moved to their conversation ID in one schema step; `session_rekeys` records each move, `/api/state` returns it as `rekeyed`, and the catalog announced each move as `session.removed` with `rekeyedTo` so the apps move open panes and drafts to the new ID.

`Claude.reach` makes a session reachable before anything is posted into its conversation. `Claude.Send` and `Claude.SendHumanTurn` call it, so whichever route `delivery.RouteFor` picks, every message sender does: CLI `send`, dashboard and app sends including a person's composer turn, channel sends, voice delegation, and the daemon queue that carries relays and queued messages. A resumed conversation runs in the Claude host, which takes a person's turn under its delivery intent's uuid; agent messages take the resumed process's native socket. Steer and interrupt act only on a running turn, so they never resume. `/compact` and `/model` act only on a running process, so they never call `reach`: a host session takes `/model` through the host, and otherwise both need a running process with Remote Control and report unsupported without one. Voice selects its target from the registry by alias or exact session ID and refreshes it from the surface's live session for that exact ID. A surface whose sends wake a closed session, as Claude's do, takes the registered row when the session is not live, so a closed Claude session can be selected and a delegation wakes it, showing "Waking up" while it does. The reply stream outlives the request that started the delegation. A delegation is the person's turn. Voice speaks the reply only when the send kept the session ID its stream was opened for and the person's route to the session carries turns; a resume in the host changes the session's transport but not its ID, so voice follows the resumed session's reply. Otherwise the request is delivered and voice says it cannot follow that session's reply. `reach` uses only Claude's own records:

- A live process whose session record has a validated messaging socket or a Remote Control ID: the message goes to it, and the session row is re-registered if its process changed.
- A live process for the conversation without either, or a background job that is still starting and has no record yet: the delivery is held. A send queues it, and the queue retries it until the process can take messages, or until it exits and the conversation is resumed. No second process is started.
- No process: the Claude host opens the conversation with `resume` in its working directory (the job's, else the session's, else the transcript's), without a prompt, so only the sender's message reaches the conversation. A terminal or background session resumes this way too and becomes a host session without Remote Control. The send then waits up to 10 seconds for the host's process to be listed. An agent message to it takes the native socket, so `send --reply` and `--stream` deliver it and say that its reply is read with `agenthail last`. Without a running host the delivery is held.

Resumes of one conversation are serialized across Agenthail processes by `~/.agenthail/claude-resume/<session-id>.lock`, held from the liveness check through the wait, so concurrent senders resume once and each message is delivered once. The host opens a conversation by its id, so it cannot start a copy. A process from an earlier host that still runs the conversation holds the delivery. A Claude that exits with an error before it starts has refused the resume: the delivery fails with `Claude refused to resume <id>: <Claude's output>` and nothing was sent. A Claude that cannot be spawned or does not start in time is unavailable and retried. A resume whose answer from the host is lost, or that the host opened but did not list in time, is unknown: the delivery stays `submitted`, a queued item is dead-lettered as unknown, and nothing is retried.

Observation reads a session whose process exited as idle when no other process holds its conversation, so its queue drains into a resume, and as offline while another process, including a starting job, holds it. A target resolves to a registered session that discovery no longer lists only when it names that session exactly: an alias, a session ID, or `surface:<id>`. Name and folder fragments never resolve to historical sessions. `agenthail thread resume` uses the same path without sending a message. For a host session, `thread status` reads the host's listing, `thread stop` closes the session in the host, and `thread logs` is refused: the host keeps no background log, so read the transcript with `agenthail last`.

If creation returns a session, that session is registered before the initial-turn result is reported. An ambiguous initial-turn outcome records a durable `delivery_intents` row targeting that session and returns a neutral, non-retryable `submitted` result with its `deliveryId`; no automatic retry occurs. A definitive initial-turn failure records a failed delivery problem and returns a typed failure while preserving the created session. If the session identity is absent, the result is `{ok:false,status:"failed",retryable:false,unknown,error}`, where `unknown` is `true` when the initial-turn outcome is ambiguous and `false` for a definite failure; inspect the native catalog before any explicit retry of an unknown outcome. If the durable intent cannot be recorded, the result is an explicit bounded failure that carries the created session.

## Codex forks

```sh
agenthail thread fork codex:<source-id> --alias experiment --json
agenthail thread fork @builder --last-turn <turn-id> --json
```

Optional `--model` and `--last-turn` pass to the owning app-server; `--last-turn` keeps history through that turn. The fork stays in the source folder. A fork retains the source transport, uses the returned identity, and is registered for subsequent sends. Goal continuation is deferred until explicit input; forking alone does not launch an automatic goal continuation. A fork does not create a Git worktree.

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
agenthail send @builder "Return the findings" --effort high --mode plan --service-tier fast --output-schema /path/to/schema.json --reply --json
agenthail thread create codex "Return the findings" --output-schema /path/to/schema.json --effort high --json
```

Effort accepts `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` and `ultra`; the selected model must support the requested value. Mode accepts `plan` or `default`. If mode is supplied without a model override, Agenthail reads the current session model. Codex's collaboration settings take precedence over separate model/effort fields. These native settings can affect subsequent turns; an omitted field does not promise a reset.

Service tier accepts `default`, `fast` or `flex` and uses the protocol's `serviceTierForTurn` field. Output schema is an inline JSON object in the API and a file in the CLI, limited to 64 KiB. Agenthail validates that it is an object; Codex validates supported schema semantics and constrains the final response. It does not constrain every tool event or intermediate message.

Turn settings survive the durable Agenthail queue, retries and daemon replay even when no model override is given. Registry schema version 3 adds `message_queue.turn_options`; existing queued entries retain empty/default settings. Queue JSON and dashboard queue state expose the stored fields. Advanced send settings are rejected for non-Codex targets; Claude creation separately supports its native effort flag.

## API and output

`POST /api/action` uses the existing dashboard authentication and request protection:

- `session-create`: `surface`, `message`, `cwd`, `alias`, `model`, plus creation and turn fields.
- `send`: `sessionId`, `message`, `effort`, `mode`, `serviceTier`, `outputSchema`.
- `session-fork`: `sessionId`, `fork: {model, lastTurnId}`.
- `native-queue`: `sessionId`, `nativeQueue: {queueAction, message, queuedSubmissionId, clientUserMessageId, queuedSubmissionIds, cursor}`.
- `session-lifecycle-status`, `session-lifecycle-logs`, `session-lifecycle-stop`, `session-lifecycle-resume`: `sessionId`.

Optional fields may be omitted. Fork, queue and lifecycle successes return `{ok: true, result: ...}`; failures return `{ok: false, unknown, error}`. An ambiguous session creation returns HTTP `202` and `{ok:true,status:"submitted",accepted:true,retryable:false,session,deliveryId,detail:"Submitted to <target>."}`; the CLI JSON shape uses the same status fields and exits successfully, while human output is exactly `Submitted to <target>.`. Definitive initial-turn failures return `{ok:false,status:"failed",retryable:false,session,deliveryId,error}` and remain non-retryable. Creation without a known session returns `{ok:false,status:"failed",retryable:false,unknown,error}`, and creation without a durable intent returns `{ok:false,status:"storage_failed",retryable:false,session,error}`; neither is represented as an accepted receipt. CLI fork, queue and lifecycle successes print JSON even without `--json`. With `--json`, operation failures also emit a JSON error object on stdout and return a nonzero exit status; the CLI writes its diagnostic to stderr.

## Verification boundary

The implementation was checked against installed Claude 2.1.263 help/extracted source and Codex 0.153.4 generated experimental app-server schemas. Automated tests cover fake Claude subprocess launch/identity/lifecycle, a local Codex socket for forks and all six queue methods, turn-option protocol mapping, schema migration and queued replay, CLI parsing/output, and dashboard API consumers. These are deterministic integration checks with no paid model calls. Live model execution, production daemon replacement and installation are separate release steps.

See [the capability audit](agent-capability-audit.md) for the historical inventory and remaining adapters and controls.
