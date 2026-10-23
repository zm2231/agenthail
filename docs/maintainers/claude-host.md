# Claude session host

The Claude host is a Node process that runs Claude sessions through the Claude
Agent SDK, so a person's message can reach a session as their own turn without
Remote Control. Its source is [`claude-host/host.mjs`](../../claude-host/host.mjs);
the Go side is [`internal/claudehost`](../../internal/claudehost).

## One host per user

The daemon owns the host. Every 30 seconds, and once at startup, it calls
`Manager.Ensure` ([`claudehost/manager.go`](../../internal/claudehost/manager.go)):

1. Dial `~/.agenthail/claude-host.sock` and exchange `hello`
   (`{protocol, hostVersion}`). A host of the daemon's version is used as is.
2. A host of another version is replaced in the same call. One of protocol 2
   or later is asked to drain, which retires it (below); one of protocol 1
   cannot retire and is stopped with `SIGTERM`. Its Claude processes run on,
   held from the new host until they exit, and the next message after that
   resumes the conversation in the new host. Either way the daemon then starts
   its own host.

   Until that happens, a CLI or daemon of the new build may meet the old host
   on the socket. `Client.Open` exchanges `hello` first and opens a session
   only in a host of its own protocol; any other protocol is `ErrOtherProtocol`,
   and the creation or resume is held and retried like an unreachable host.
   Protocol 3 added a `name` on a resumed `open`. Only `open` checks the
   protocol: a retiring host of protocol 2 keeps serving `list`, `send`,
   `respond`, `interrupt`, `setModel`, `setPermissionMode`, `close` and
   `subscribe` for its sessions to a client of a later protocol, and a resume
   of a conversation it still runs returns its process. `logs` came later
   without a protocol change; a host shipped before it answers `invalid`,
   which the daemon reports as a host that predates session logs.
3. If nothing answers, take an exclusive `flock` on
   `~/.agenthail/claude-host/lock`. If another process holds it, a host is
   starting: wait for its `hello`.
4. Otherwise start the host in its own session (`setsid`), passing the locked
   file as fd 3. The host checks that fd 3 is that lock file and holds the lock
   for its lifetime, so two hosts can never serve the same home.

## Replacement

An update never moves a running Claude to the new host: the SDK query that
holds Claude's input, output and open prompts lives in the old host's
process. The old host keeps serving its sessions instead.

`drain` retires the host. Before it answers, the host listens on
`~/.agenthail/claude-host.<pid>.sock`, leaves `claude-host.sock`, releases the
lock and stops writing `state.json`, so the daemon starts the new host at
once. The retiring host refuses `open` with `draining` and serves everything
else for its own sessions: `send`, `respond`, `interrupt`, `setModel`,
`setPermissionMode`, `logs`, `close`, `list` and `subscribe`. It closes a session once
Claude reports it idle with no open request, no background task and no turn
owed, and exits when its last session has closed. Claude owes a turn for work
it was handed while idle: a background task that ended, unless the live set
marked it `ambient` (not activity, so no turn follows), which Claude reports
before it starts the turn telling the model, and a message the host passed it
that no turn has started on or answered (`user_message_uuids`). Claude reports
idle only with its queue empty, so the next `running` covers everything it was
handed; work that arrives while it runs is covered by that turn's idle.
If Claude never starts an owed turn, the session stays on the retiring host
until a later message runs one. The next message resumes that conversation
in the new host. It never closes the input of a session that is still working,
because Claude then refuses every prompt it waits on.

The new host loaded the retiring host's sessions from `state.json` as running
processes, so it answers `held` for them. `claudehost.Client` routes by
session: a session call goes to the current host and, on `not_open`, `held`
or no host, to each retiring host; `open` with `resume` returns a retiring
host's session; `list` merges every host and lists each process once. The
daemon follows the retiring hosts' events until they exit. A socket nothing
listens on is removed.

Retiring hosts are bounded by their sessions. A retiring host opens nothing,
so it holds only sessions it had when it retired, and it exits with the last
of them. A host retired with no session exits at once. Repeated updates
therefore leave at most one retiring host per session that has kept working,
or kept a background task running, since the update that retired its host.

The host and its Claude processes outlive the daemon. The launchd service sets
`AbandonProcessGroup`, and a restarted daemon attaches to the running host
instead of starting another. Each Claude process is started detached: when
the host dies, a running turn finishes, and the next host reopens the session
with `resume`. A session whose previous process still runs is reported as held
until that process exits.

The host runs the user's own `claude` (`surface.ClaudeBinary`) with the same
environment as the daemon's other Claude launches (`claudeCommandEnv`), so
`CLAUDE_CONFIG_DIR` and the account apply unchanged. Its log is
`~/.agenthail/claude-host/host.log`.

## Protocol

Newline-delimited JSON over the socket, one request per connection:
`hello`, `open`, `send`, `interrupt`, `respond`, `setModel`,
`setPermissionMode`, `logs` (the last 4 KiB an open session's Claude wrote to
stderr), `close`, `list`, `drain`, and `subscribe`, which streams
events after a sequence number and reports a gap when the ring buffer has moved
past it. A failed request returns `{code, message}`:

| Code | Meaning | Delivery outcome |
|---|---|---|
| `not_open` | the host does not run that session | unavailable |
| `held` | the session's previous process still runs | held, retried |
| `draining` | `open` on a retiring host | held, retried |
| `unknown` | the SDK had not taken the message within ten seconds | unknown, never retried |
| `invalid` | malformed request | terminal |
| `refused` | `open`: Claude exited with an error before it started, such as a workspace it does not trust | refused, held until a person fixes the cause |
| `open_failed` | `open`: Claude could not be spawned, was killed, or did not start in time | unavailable, retried |
| `stale` | `respond`: the request was already answered or withdrawn | a conflict; nothing is retried |

A `send` is answered only after the SDK has taken the message for Claude's
input, so a success means the message has left the host's queue.

## Context window

When a turn result arrives the host asks Claude for its context report
(`getContextUsage`, at most five seconds) to learn the main model, then records
that model's `modelUsage[model].contextWindow` from the result. The reports run
one at a time beside the message loop, so a slow one holds up no turn, and a
retiring session closes only after its pending report. A result counts every model
the turn called, side queries included, so the report decides which entry is
the main one. The host lists the window with its session (`contextWindow`,
`contextModel`; `0` and empty before the first result) and emits a `context`
event when either changes. Claude writes this figure nowhere on disk, so a
session the host does not run has no such window. The report's own `maxTokens`
is the auto-compact window, not the model's, and is not used for it.

The same report says when Claude compacts the session: `isAutoCompactEnabled`,
`maxTokens` (the auto-compact window), `autoCompactThreshold` (the size that
triggers it, the window less Claude's buffer) and `autocompactSource` (which
input set the window: `settings`, `env`, `auto` and others). The host lists it
as `autoCompact` (`{enabled, window, threshold, decidedBy}`; `enabled:false`
alone while autocompact is off, when `maxTokens` is the model's window instead)
and Agenthail reports it as the session's `contextUsage.autoCompact` with
`source: "runtime"`, under the same rule as the window: only for a hosted
session whose latest reply came from the reported model. It is the effective
value; the settings files are what Claude reads at start.

## Requests

Claude asks the host, through the SDK's `canUseTool`, before a tool its
permission mode does not allow and for every `AskUserQuestion`, in every mode
including `bypassPermissions`. Claude's safety checks also ask in
`bypassPermissions`. One example is the dangerous-removal check on an `rm`
whose target Claude cannot resolve before it runs, such as a glob after a `cd`
or a command substitution. In Claude Code 2.1.304 that check returns a
`safetyCheck` decision with no rule to always allow, and bypass mode allows
only decisions that are not safety checks. Such a request is Claude's own,
its `decisionReason` names the check, and Agenthail shows it like any other.
An MCP server asking for input (MCP elicitation) reaches the host through the
SDK's `onElicitation`; without that callback the SDK declines every one
unasked. The host holds each one as an open request until
a person answers it. There is no timeout: a request ends only with an answer,
with Claude withdrawing it (an interrupt or the turn ending, outcome
`withdrawn`), or with its session (`ended`). Agenthail never answers on a
person's behalf. A withdrawn or ended MCP request is `cancel`led, which MCP
gives a request nobody answered.

Each open request is listed with its session in `list` (`requests`, oldest
first) and announced as a `request_opened` event; `request_resolved` carries its
outcome. A request has Claude's own `requestId`, the tool, its kind
(`question` for `AskUserQuestion`, `input` for an MCP server's request,
otherwise `approval`), Claude's prompt text
(`title`, `description`, `decisionReason`), the tool's input up to 16 KB or a
question's questions and options, and `alwaysAllow` when Claude offers a rule
that would allow the tool from now on. An `input` request has no tool; its
`elicitation` has the server, its message, its `mode` and either the `url` to
open or the form's `fields` in the server's order. A field has its `name`,
`title`, `description`, `type` (`string`, `number`, `integer` or `boolean`),
`required`, the `options` a string takes (from `enum` with `enumNames`, or
`oneOf`/`anyOf` of `const` and `title`), `multiSelect` for a list of them,
and its `default` when it is one the field takes. MCP limits a form to such
flat fields; a property of any other type is taken as text.

`respond({sessionId, requestId, decision})` answers one: `{behavior: "allow"}`,
`{behavior: "allow", always: true}` (only with `alwaysAllow`), or
`{behavior: "deny", message}`. A question is allowed with `answers`, one per
question keyed by its text; an answer that is not one of the options is the
person's own text. An `input` form is allowed with `content`, an object of
field values: every required field, only the form's own fields, and each a
value its field takes (an option's value, a list of them, a number, an
integer or a boolean). Its outcome is `accepted`, and the server receives
`{action: "accept", content}`. A link is allowed with no content once the
person chose to open it; a deny is `declined`. A request answered twice, for another session, or after it
was withdrawn is `stale`.

A request lives in the host process that holds Claude's prompt. Clients and the
daemon can disconnect and come back; the request is still open. It cannot
outlive that process or Claude's input: once the input closes, Claude refuses
every prompt it still waits on ("Tool permission stream closed before response
received") and carries on. The host therefore ends a session's open requests
as `ended` when it closes the session's input, and refuses any request asked
after that. A request no host holds cannot be answered. That covers a session
whose process exited, one not open in any host, and one whose Claude runs on
without a host. Answering it is `stale`.

The daemon derives what a session waits on from the hosts' listings. A
`stale` answer makes it observe the turn again, which clears
`session_runtime.waiting_id` and the catalog row's `waiting_on`. The Mac, iOS
and web clients drop the card on a `409` before reading the requests again.

The host passes `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`, so Claude reports
its own `idle`, `running` and `requires_action` states. The SDK sets
`CLAUDE_CODE_SDK_READS_SESSION_STATE`, under which Claude marks those events
`sdk_host_only` and the SDK drops them, so without the variable the host
would see neither a turn Claude starts itself (another agent's message) nor a
prompt it waits on. Claude also sends `background_tasks_changed`, the whole
set of live background tasks, on every change. A Claude that sends a turn
result without state events is logged once. For it, idle is read from turn
results, and its sessions retire only as a turn ends.

The daemon follows the host's events and asks for a catalog pass when a request
opens or resolves. For a session the host runs, the oldest open request is what
the session waits on: its `Waiting` id is the `requestId`, so each request is
announced once.

A wait carries `answerable`, kept in `session_runtime.waiting_answerable`. It is
true only for a request a host holds or a prompt the permission hook holds,
which `agenthail answer` and the clients' request cards reach. A wait read only
from Claude's process record (`status: waiting` with its `waitingFor`) is not
answerable. A terminal session answers it in its terminal. A hosted or
background session has nothing that shows it, so the clients say Agenthail
can't reach it and offer to stop and resume the session, which clears it.

## Usage limits

When a usage limit refuses a turn, Claude writes an API error record with
`apiError: usage_limit_reached` (or `error: rate_limit`) and the reset time in
`quotaLimits.resetsAt`. A session whose latest turn is such a refusal carries
`usageLimit{resetsAt}` on the session, its catalog row
(`catalog_sessions.usage_limit`) and the dashboard projection, until the reset
time passes or a later turn starts. The daemon's status pass rereads a session
whose limit has reset even when none of its files changed, so the row drops
the limit within a second of `resetsAt`. `resetsAt` is absent when Claude did not
say. The limit outranks a wait read from the process record, which is stale
once the turn has ended. The clients list the session under Needs you with
its reset time, and the transcript notice names it too.

## Activity of a working turn

A working session also carries `activity{busySince, lastProgressAt,
tool{name, startedAt}, noProcess}` on the session and the dashboard projection,
which federates it to paired hosts. It never changes the session's status and
nothing acts on it; it lets a client say how long a working turn has been
quiet.

`busySince` is when the open turn started. `lastProgressAt` is its newest
transcript record, or the session record's `statusUpdatedAt` when that is
later. `tool` is the turn's newest tool call without a result yet, so a long
build reads as running Bash since it started rather than as stuck. A session
record that says idle clears `activity`, as does a wait on the person.
`noProcess` marks a turn the transcript leaves open when there is neither a
pid nor a live session record for the conversation, so nothing is running it.

The status pass republishes a row for its activity only when the turn, the
tool or `noProcess` changed, or `lastProgressAt` moved by a minute or more
(`surface.ActivityProgressStep`). After ten minutes without progress the Mac,
iOS and web clients add `no activity for 12 min · running Bash since 10:02`
(or `· no Claude process`) to Working, as plain secondary text.

A Codex session carries the same `activity`, read from its rollout by the
discovery and status passes alike. `busySince` is the open turn's
`task_started` record, `lastProgressAt` its newest record (or the rollout's
last write when records carry no timestamp), and `tool` its newest
`function_call` or `custom_tool_call` without a matching output. The rollout
alone counts a turn as busy only within 15 minutes of its last write; past
that a session the runtime still reports busy keeps the turn's activity, so a
hung Codex turn reads as quiet the same way. `noProcess` is never set for
Codex: a turn the runtime does not report running is not working.

## Task progress

A session whose agent keeps a task list carries `plan{done, total, current}` on
the session and the dashboard projection. `current` is the step in progress,
as its active form when the agent gave one. Older clients ignore the field and
clients treat its absence as no plan.

Claude keeps the list itself in `<config root>/tasks/<list id>/<n>.json`, one
file per task. The list id is `CLAUDE_CODE_TASK_LIST_ID`, else the agent
team's name, else the session id, with anything outside `[a-zA-Z0-9_-]`
replaced by `-`. The transcript only shows the calls: a `TaskCreate` result
names the task's id and subject, a `TaskUpdate` result its id. The daemon
reads the directory named for the session id, and only on an observation that
follows a new task result, never on a scan that brings none. It counts the
list as the session's own only when the newest task result agrees with it: the
created task is there with the same subject, or the deleted one is gone. A
list kept under another id, as with `CLAUDE_CODE_TASK_LIST_ID` or a team,
fails that check and the session shows no plan rather than someone else's.
A `TodoWrite` call carries the whole list and sets the plan directly.

Codex sends its plan with `update_plan`, as a function call or, in code mode,
as `tools.update_plan({plan: [...]})` inside an `exec` call. The daemon reads
the newest one from the rollout's last 4 MB on first sight and then only what
was appended. A plan built from a variable cannot be read, and shows none
rather than the previous one.

The status pass republishes a row for its plan only when `done`, `total` or
`current` changed. The plan stays on the row once the session closes; clients
show a `3/5` badge only while the session is open, not offline, and has steps
left, so a session that stopped part way does not keep a badge.

## Background tasks

A turn can end with work still running: a Bash call run in the background, an
Agent call run asynchronously, a Monitor. Claude starts a turn of its own when
one reports, so the session's status is idle but its work is not done. The
daemon keeps this as a separate fact beside the status,
`backgroundTasks{running, tasks[{id, kind, description, parentId, ambient,
since}], since, source}`, on the session, its catalog row
(`catalog_sessions.background_tasks`) and the dashboard projection, which
federates it to paired hosts. `kind` is `shell`, `agent` or `monitor`, or
Claude's own task type for any other.

For a session the host runs, the host lists the tasks from the last
`background_tasks_changed` and sends a `tasks` event on each change, which asks
the daemon for a catalog pass. `since` is when the host first saw the task.
Claude marks some tasks `ambient`: tasks it keeps out of the transcript and
live-update watchers, which it says are not activity. They are listed with
`ambient: true` and do not set `running`. A Monitor started with the Monitor
tool is not ambient. `source` is `host`.

For a terminal session, the daemon reads the tasks from the transcript, counting
only those started since the live process's `startedAt`. The call's result
says it went to the background, not its input: a Bash result with
`toolUseResult.backgroundTaskId` (asked for with `run_in_background` or moved
there while it ran), an Agent result with `isAsync` and `agentId` (asked for or
asynchronous by the agent's definition), and a Monitor result with `taskId`. A `<task-id>` in a later record (the task
notification Claude queues) or a TaskStop on the id ends it. A terminal Claude
writes `shell` as its status while a background shell runs, so a shell the
transcript leaves open counts only while it does; that covers shells ended
without a notification, such as when a process exits. `source` is
`transcript`. A `shell` status with no shell in the transcript still counts,
with no tasks and `source` `status`.

The completion alert waits for the work. A turn that completes with
`running` set does not send "finished" and keeps the alert armed, so the turn
that completes with nothing left running sends it, including the turn Claude
starts when the last task reports. Relays follow completed turns as before.

## Model and permission mode

`setModel` and `setPermissionMode` change a running session at once, between
turns or in the middle of one. Every session is opened with Claude's consent to
`bypassPermissions` (`allowDangerouslySkipPermissions`), so a session can be
switched into it later; the mode it runs in is still the one asked for. Claude
starts a resumed conversation in its settings' default mode and model, so the
host keeps the mode and model each conversation last ran with in
`~/.agenthail/claude-host/settings.json` and reopens a resume without them in
those.

## Delivery

A Claude process that the host runs for the conversation it lists is
discovered with runtime kind `claude-host`; a Claude Code background job
(`claude --bg`, whose session record has kind `bg`) is `claude-bg`, and any
other Claude process is `claude-cli`. The Claude adapter serves all three and
decides per session from that kind. The
process also serves Claude's native messaging socket, so its transport is
`uds`. It is open while the host lists it; a CLI session is open while its pid
is a running `claude` process. When the host stops listing a process that still
runs, discovery records it as `claude-cli` again.

- A person's message ([`RouteHumanTurn`](../../internal/delivery/route.go))
  goes through the host's `send` with origin `human`. Its uuid is the delivery
  intent's uuid, which Claude stores as the transcript record's uuid: the turn
  is followed exactly, and a resend of the same intent runs once, also across a
  host restart. Steer sends with priority `now`; interrupt goes to the session's
  query.
- Agent, relay and channel messages take `RoutePeer` over the native socket,
  where Claude records the verified sender and the reply address. Claude drops a
  peer origin given on SDK input, so the host is not used for them.
- While the host is down and the session's process still runs, a person's
  message is held and retried.

Every delivery intent has a uuid (`delivery_intents.uuid`). The dispatcher
passes it to the adapter for direct sends, steers and busy handling, and the
outbox for queued messages (`surface.WithMessageUUID`).

## Creation and resume

`Claude.StartSession` opens a new conversation in the host under a uuid it
chooses, with the model, permission mode, effort, agent, name and worktree
asked for, then sends the message as the conversation's first turn: a person's
turn under the delivery intent's uuid. It is the `claude-sdk` launcher, the
default for the CLI, the dashboard and the apps. An open whose answer is lost
still names the session, so creation reports it as unknown rather than failed.
Without a running host nothing is created.

Every session runs with Claude Code's own system prompt (`systemPrompt: {type:
'preset', preset: 'claude_code'}`) and reads the same settings a terminal
session does (`settingSources: user, project, local`): CLAUDE.md files,
settings, hooks and skills. The SDK sends an empty system prompt when none is
given, and Claude then runs with a one-line prompt instead of Claude Code's.
When the daemon starts the host from an `agenthail` binary it passes it as
`--agenthail`, and every session gets SDK `SessionStart` and
`UserPromptSubmit` callbacks that run `agenthail hook session-context
--claude-pid <pid>` with the hook input on stdin, so it is told its agent card
with no hook installed ([agent card](agent-card.md#telling-a-claude-session-its-card)).
The callback returns only the command's `additionalContext`, and nothing on any
failure or after 8s. The claim is keyed by the Claude pid, the same key the
installed hook uses as Claude's child, so a session that has both is told once.
Where Claude records a conversation's system prompt (the SDK's `snapshot`,
which the SDK notes is still rolling out and does not apply to every
provider), it resends the prompt it recorded on every later request and resume
until the conversation is compacted, so an existing conversation keeps its
prompt. Measured with Claude Code 2.1.305, Claude Code's prompt added about
2,200 input tokens to each request, read from the prompt cache after the first.

`Claude.reach` resumes a conversation no process holds with `open({resume})`
and waits until the host lists the process. When the conversation lives in a
Claude config root other than the primary one, the open carries
`configDir`, and the host runs that Claude with it as `CLAUDE_CONFIG_DIR`.
Claude restores a resumed conversation's title from its transcript (the last
`custom-title` record) but names the resumed process anew, with
`nameSource: "derived"`, so the open carries that title as `name` and the
session keeps the name it was started or renamed with. The host refuses only
a `worktree` on a resume. The host's `held` answer holds the delivery. `refused` means Claude refused the
conversation: a queued message waits as `refused` with Claude's output as its
last error, and a direct send fails with it. `open_failed` is a Claude that
never got to decide, and the delivery is retried like any other unavailable
target.

`thread status` reads the host's listing, `thread stop` closes the session in
the host, and `thread resume` reopens it. The host keeps no background log.

## Remote Control

A Claude process started through the SDK does not register a Remote Control
bridge, with or without `--remote-control`, while `claude --bg` under the same
account does. Host sessions therefore have no `bridgeSessionId` and are not
controllable from claude.ai or the Claude apps, and `/compact` is unsupported
for them. The model and permission mode change through the host instead.

## Packaging

`scripts/stage-claude-host.sh` stages `host.mjs` with its locked dependencies
beside the `agenthail` binary as `claude-host/`. The SDK's bundled Claude
binaries are optional dependencies and are omitted; the remaining dependencies
are plain JavaScript, so one tree serves every platform, and the script fails
if a native module appears. Every install ships Node 22 as
`runtime/node/bin/node` beside `claude-host/`, and the daemon runs the host and
the cookie bridge with that file only, never a `node` from `PATH`.
`scripts/fetch-node.sh` pins the version and checksums and stages it for the
Mac package, the release archives and `install.sh`; run it with no arguments
in a checkout for source builds and tests. `AGENTHAIL_CLAUDE_HOST` overrides
the script path.

## Tests

```bash
scripts/fetch-node.sh
npm ci --prefix claude-host --omit=optional
npm test --prefix claude-host
go test ./internal/claudehost/... ./internal/surface/surfaces/ ./internal/daemon/ -run 'Host|Ensure|Subscribe|Client' -race
```

The tests run the real host against a fake Claude CLI
([`claude-host/test/fake-claude.mjs`](../../claude-host/test/fake-claude.mjs))
that speaks the stream-json protocol, writes a session record and serves a
messaging socket. Hosts and fake CLIs run in their own sessions, out of reach of a
test's cleanup once its process is killed or times out, so each one loads
[`claude-host/test/owner-watchdog.cjs`](../../claude-host/test/owner-watchdog.cjs)
(hosts through `NODE_OPTIONS`; the fake CLI imports it, because the Agent SDK
drops `NODE_OPTIONS` when it starts Claude): it exits the process when any pid in
`AGENTHAIL_TEST_OWNER_PIDS` (the test process, and for the Node tests their
runner) is gone.
