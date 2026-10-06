# Session creation, lifecycle and Codex native controls

Agenthail exposes these operations through the CLI and the authenticated dashboard API. The web dashboard has creation controls, Codex turn options, and a session operations form. The native iPhone companion supports Claude background creation with name, worktree, named-agent, model, effort and permission options, plus ordinary session and Agenthail queue controls. Use the web dashboard or CLI for lifecycle operations, Codex forks, native Codex queue editing and advanced Codex turn settings.

The immediate send status in CLI JSON and dashboard/mobile API results is `sent`, `queued`, or `submitted`, followed by the target. `sent` means the selected transport accepted the request, `queued` means Agenthail durably accepted delayed work, and `submitted` means Agenthail recorded the intent without asserting provider acceptance. Human/API sends without an agent sender use the durable operator identity. Channel sends count `submitted` members separately from `sent` and `queued`. Receipt copy does not ask the sender to investigate read or confirmation state. Success is silent; proven delivery problems appear as notices. Internal audit evidence retains `transport_accepted`, `held`, `delivered`, `reply_observed`, `failed`, `unknown`, `expired`, and `canceled`. Socket acceptance never proves model completion.

`agenthail list --json` returns discovered sessions together with an `errors`
object. A failed optional surface is a warning when at least one surface completed
discovery; the command fails only when every configured surface failed. Codex
rows take the local transcript's latest task lifecycle when it is known and the
shared database state otherwise. Claude peer `idle` is trusted only from peers advertising idle
notifications or from a readable transcript; otherwise the state is unknown.

`agenthail list --cwd <path>` retains sessions whose normalized workspace is that
directory or a descendant. Existing symlinks resolve before comparison, and path
components—not string prefixes—define ancestry. `--wide` prints each full normalized
workspace; ordinary table output also uses the full path when multiple sessions share
a workspace basename. JSON always retains the complete `cwd` field. CWD narrows
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
capability is queued instead. `send --no-queue` refuses delayed delivery, while
`queue` always creates the durable pending item and `steer` is an explicit active-
turn control. The setting is exposed by the dashboard/API settings endpoint and
the existing dashboard settings write path. Read-only targets fail before
dispatch. An unknown delivery outcome must be inspected before an explicit retry;
it is never resent automatically.

## Claude background sessions

```sh
agenthail thread create claude "Investigate the failing build" --cwd /path/to/repo --alias investigator --name investigator --effort high --permission-mode plan --json
agenthail thread status @investigator --json
agenthail thread logs @investigator --json
agenthail thread stop @investigator --json
agenthail thread resume @investigator --json
```

Creation runs the installed Claude CLI with `--bg`. Optional flags are `--model`, `--name`, `--worktree <name>`, `--agent <name>`, `--permission-mode` and `--effort`. The working directory defaults to the caller's directory. Supported permission modes are `acceptEdits`, `auto`, `manual`, `dontAsk` and `plan`; Claude effort values are `low`, `medium`, `high`, `xhigh` and `max`. Model availability and native permission behavior remain Claude's responsibility.

Model discovery and background lifecycle commands share executable selection and environment: explicit `AGENTHAIL_CLAUDE_BIN`, then `claude` on PATH, then the native installation at `~/.local/bin/claude`. An invalid explicit choice fails without choosing another installation. `agenthail daemon install` saves the explicit choice in its launchd configuration. Native installation discovery works when macOS launches Agenthail without the interactive shell's PATH. These commands run without the caller's `CMUX_` variables. Inside a cmux terminal, cmux's `claude` wrapper otherwise injects a temporary `--settings` file and an `--mcp-config` that Claude saves as the job's respawn flags; after a reboot the file is gone and every resume fails before init.

Use `@alias` or `claude:<session-id>` as a target. The `claude/name` transcript heading is a display label, not a routable address. Read the session's `id` from `agenthail list --json`; do not construct an ID from a truncated table label. Duration arguments require units, such as `--timeout 8s`.

For native messaging, Agenthail resolves the session first, then reads its PID registration and selects `messagingSocketPath`. It verifies session identity, a live PID, the expected `/tmp/cc-socks/<pid>.sock` path, socket ownership, and the process-start identity. Agenthail's verifier and peer registrations use `LC_ALL=C TZ=UTC`; native Claude records from 2.1.267 onward use UTC, while older records use local time. A stale process identity is rejected. Relays store canonical session IDs and use the shared queue and adapter delivery path; they do not retain a socket path across process changes.

Socket messaging and Remote Control have different capabilities. Native socket-only sessions support messages and transcript inspection, but not compact, model switching, interrupt or steering. Sessions with a Remote Control identity can use those typed controls even when ordinary messages use the native socket. Controls are never encoded as queued slash-text messages. Background status, logs, stop and resume use the Claude CLI lifecycle interface. Socket delivery is not evidence that a Remote Control operation works, and queue acceptance is not proof that Claude consumed the message.

Claude compact requests are stored as typed queue operations. The daemon waits
for the target to become idle, invokes `/compact` through Remote Control, and
marks the queue row delivered only after the local transcript records a new
`compact_boundary`. If the daemon loses confirmation after submitting the
command, the row becomes `unknown` for operator review instead of being retried
as either a control or a message.

Claude assigns the background ID. Agenthail parses that ID from the native launch response, then resolves the full session ID through `claude agents --json --all`. It never assumes that a supplied `--session-id` controls background identity. The registered session and optional alias become the targets for later messages. A successful launch confirms registration, not completion of the first model turn; there is no fabricated turn receipt.

Claude Code lets one conversation stay open in several processes; resuming it elsewhere only warns. With Remote Control each process has its own session ID, and each live one keeps its own row, alias and queue. Registration absorbs a row that shares the conversation's transcript only once that row's process has exited or when it is the registering process under an earlier ID, and a launch record only while it has no process or transcript. Without Remote Control the processes share the conversation's session ID and one row: the process that opened it first owns the row, sends to the session reach it, and a later process is addressed by its PID. Commands run from either process still resolve to that session. Because a sender's process is unknown when several share its ID, replies to it are addressed to the session rather than to one process's socket.

Status, logs, stop and resume operate only on native background records. A registered alias or `claude:<full-session-id>` can address a stopped session. Sending to a registered background session that has no live process fails with the job's state and the `agenthail thread resume` command to run, instead of a target match error. Resume is a no-op when the native catalog reports working, running, starting or blocked. Otherwise it invokes `--bg --resume`, checks the returned identity, and waits up to 10 seconds for the catalog to report the job working, running, blocked, idle, waiting or busy. Claude prints its `backgrounded` line before the session initializes, so that line is not proof the session started. A job that settles failed, crashed, stopped or done returns an error carrying the `detail` Claude recorded in `~/.claude/jobs/<id>/state.json`; a job that never settles returns an unknown outcome. Interactive sessions do not acquire background lifecycle controls merely by appearing in discovery. Destructive removal is not exposed.

If creation returns a session, that session is registered before the initial-turn result is reported. An ambiguous initial-turn outcome records a durable `delivery_intents` row targeting that session and returns a neutral, non-retryable `submitted` result with its `deliveryId`; no automatic retry occurs. A definitive initial-turn failure records a failed delivery problem and returns a typed failure while preserving the created session. If the session identity is absent, or the durable intent cannot be recorded, the result is an explicit bounded failure rather than an `unknown` receipt; inspect the native catalog before any explicit retry.

## Codex forks

```sh
agenthail thread fork codex:<source-id> --alias experiment --json
agenthail thread fork @builder --before-turn <turn-id> --cwd /path/to/repo --json
```

Optional `--model`, `--cwd`, `--before-turn` and `--last-turn` pass to the owning app-server. Before-turn and last-turn are mutually exclusive. A fork retains the source transport, uses the returned identity, and is registered for subsequent sends. Goal continuation is deferred until explicit input; forking alone does not launch an automatic goal continuation. A fork does not create a Git worktree.

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
- `session-fork`: `sessionId`, `fork: {cwd, model, beforeTurnId, lastTurnId}`.
- `native-queue`: `sessionId`, `nativeQueue: {queueAction, message, queuedSubmissionId, clientUserMessageId, queuedSubmissionIds, cursor}`.
- `session-lifecycle-status`, `session-lifecycle-logs`, `session-lifecycle-stop`, `session-lifecycle-resume`: `sessionId`.

Optional fields may be omitted. Fork, queue and lifecycle successes return `{ok: true, result: ...}`; failures return `{ok: false, unknown, error}`. An ambiguous session creation returns HTTP `202` and `{ok:true,status:"submitted",accepted:true,retryable:false,session,deliveryId,detail:"Submitted to <target>."}`; the CLI JSON shape uses the same status fields and exits successfully, while human output is exactly `Submitted to <target>.`. Definitive initial-turn failures return `{ok:false,status:"failed",retryable:false,session,deliveryId,error}` and remain non-retryable. Creation without a known session or without a durable intent is an explicit failure and is never represented as an accepted receipt. CLI fork, queue and lifecycle successes print JSON even without `--json`. With `--json`, operation failures also emit a JSON error object on stdout and return a nonzero exit status; the CLI writes its diagnostic to stderr.

## Verification boundary

The implementation was checked against installed Claude 2.1.263 help/extracted source and Codex 0.153.4 generated experimental app-server schemas. Automated tests cover fake Claude subprocess launch/identity/lifecycle, a local Codex socket for forks and all six queue methods, turn-option protocol mapping, schema migration and queued replay, CLI parsing/output, and dashboard API consumers. These are deterministic integration checks with no paid model calls. Live model execution, production daemon replacement and installation are separate release steps.

See [the capability audit](agent-capability-audit.md) for the historical inventory and remaining adapters and controls.
