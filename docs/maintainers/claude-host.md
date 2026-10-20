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

## Requests

Claude asks the host, through the SDK's `canUseTool`, before a tool its
permission mode does not allow and for every `AskUserQuestion`, in every mode
including `bypassPermissions`. Claude's safety checks also ask in
`bypassPermissions`. One example is the dangerous-removal check on an `rm`
whose target Claude cannot resolve before it runs, such as a glob after a `cd`
or a command substitution. In Claude Code 2.1.304 that check returns a
`safetyCheck` decision with no rule to always allow, and bypass mode allows
only decisions that are not safety checks. Such a request is Claude's own,
its `decisionReason` names the check, and Agenthail shows it like any other. The host holds each one as an open request until
a person answers it. There is no timeout: a request ends only with an answer,
with Claude withdrawing it (an interrupt or the turn ending, outcome
`withdrawn`), or with its session (`ended`). Agenthail never answers on a
person's behalf.

Each open request is listed with its session in `list` (`requests`, oldest
first) and announced as a `request_opened` event; `request_resolved` carries its
outcome. A request has Claude's own `requestId`, the tool, its kind
(`question` for `AskUserQuestion`, otherwise `approval`), Claude's prompt text
(`title`, `description`, `decisionReason`), the tool's input up to 16 KB or a
question's questions and options, and `alwaysAllow` when Claude offers a rule
that would allow the tool from now on.

`respond({sessionId, requestId, decision})` answers one: `{behavior: "allow"}`,
`{behavior: "allow", always: true}` (only with `alwaysAllow`), or
`{behavior: "deny", message}`. A question is allowed with `answers`, one per
question keyed by its text; an answer that is not one of the options is the
person's own text. A request answered twice, for another session, or after it
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
if a native module appears. The Mac package runs the host with its bundled
Node. Release archives and `install.sh` installs use `node` from `PATH`.
`AGENTHAIL_CLAUDE_HOST` overrides the script path.

## Tests

```bash
npm ci --prefix claude-host --omit=optional
npm test --prefix claude-host
go test ./internal/claudehost/... ./internal/surface/surfaces/ ./internal/daemon/ -run 'Host|Ensure|Subscribe|Client' -race
```

The tests run the real host against a fake Claude CLI
([`claude-host/test/fake-claude.mjs`](../../claude-host/test/fake-claude.mjs))
that speaks the stream-json protocol, writes a session record and serves a
messaging socket.
