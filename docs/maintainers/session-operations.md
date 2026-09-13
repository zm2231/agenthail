# Session creation, lifecycle and Codex native controls

Agenthail exposes these operations through the CLI and the authenticated dashboard API. The web dashboard has creation controls, Codex turn options, and a session operations form. The native iPhone companion supports Claude background creation with name, worktree, named-agent, model, effort and permission options, plus ordinary session and Agenthail queue controls. Use the web dashboard or CLI for lifecycle operations, Codex forks, native Codex queue editing and advanced Codex turn settings.

`agenthail list --json` returns discovered sessions together with an `errors`
object. A failed optional surface is a warning when at least one surface completed
discovery; the command fails only when every configured surface failed. Codex
rows reconcile shared database state with the local transcript's latest task
lifecycle. Claude peer `idle` is trusted only from peers advertising idle
notifications or from a readable transcript; otherwise the state is unknown.

`agenthail last <target> [count] --timeout 30s` bounds target resolution and
transcript retrieval. The default is the application's command timeout. A blocked
transport or filesystem read returns a timeout error instead of waiting forever.

## Claude background sessions

```sh
agenthail thread create claude "Investigate the failing build" --cwd /path/to/repo --alias investigator --name investigator --effort high --permission-mode plan --json
agenthail thread status @investigator --json
agenthail thread logs @investigator --json
agenthail thread stop @investigator --json
agenthail thread resume @investigator --json
```

Creation runs the installed Claude CLI with `--bg`. Optional flags are `--model`, `--name`, `--worktree <name>`, `--agent <name>`, `--permission-mode` and `--effort`. The working directory defaults to the caller's directory. Supported permission modes are `acceptEdits`, `auto`, `manual`, `dontAsk` and `plan`; Claude effort values are `low`, `medium`, `high`, `xhigh` and `max`. Model availability and native permission behavior remain Claude's responsibility. `AGENTHAIL_CLAUDE_BIN` selects the executable when necessary.

Claude assigns the background ID. Agenthail parses that ID from the native launch response, then resolves the full session ID through `claude agents --json --all`. It never assumes that a supplied `--session-id` controls background identity. The registered session and optional alias become the targets for later messages. A successful launch confirms registration, not completion of the first model turn; there is no fabricated turn receipt.

Status, logs, stop and resume operate only on native background records. A registered alias or `claude:<full-session-id>` can address a stopped session. Resume is a no-op when the native catalog reports working, running, starting or blocked. Otherwise it invokes `--bg --resume` and checks the returned identity. Interactive sessions do not acquire background lifecycle controls merely by appearing in discovery. Destructive removal is not exposed.

Creation and mutations with an uncertain response are reported as unknown. Inspect native `claude agents --json --all` before retrying an uncertain launch. Agenthail does not automatically repeat it.

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

Optional fields may be omitted. Fork, queue and lifecycle successes return `{ok: true, result: ...}`; failures return `{ok: false, unknown, error}`. Creation retains its existing session/result envelope, including unknown launch outcomes without a confirmed session. CLI fork, queue and lifecycle successes print JSON even without `--json`. With `--json`, operation failures also emit a JSON error object on stdout and return a nonzero exit status; the CLI writes its diagnostic to stderr.

## Verification boundary

The implementation was checked against installed Claude 2.1.263 help/extracted source and Codex 0.153.4 generated experimental app-server schemas. Automated tests cover fake Claude subprocess launch/identity/lifecycle, a local Codex socket for forks and all six queue methods, turn-option protocol mapping, schema migration and queued replay, CLI parsing/output, and dashboard API consumers. These are deterministic integration checks with no paid model calls. Live model execution, production daemon replacement and installation are separate release steps.

See [the capability audit](agent-capability-audit.md) for the historical inventory and remaining adapters and controls.
