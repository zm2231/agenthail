---
name: agenthail-operations
description: "Operate AgentHail across Claude, Codex, and Notion: discover sessions, create Claude, Codex and Notion threads, send and observe work, manage queues, aliases, channels, relay subscriptions, daemon notifications, and the dashboard."
---

# AgentHail Operations

AgentHail connects to agents that already exist on the local Mac or Linux machine. Its CLI owns
target resolution, surface capability checks, durable delivery, and ambiguity
errors. Do not build a second target picker or choose between ambiguous matches.

## Install And Verify

```bash
agenthail version --json
agenthail update --check
agenthail doctor --json
agenthail list --json
```

The primary install is the signed `Agenthail-*-arm64.pkg` from the GitHub
release. It includes its own Python and Node runtimes, installs the launchd
daemon and native Mac app, enables the authenticated loopback dashboard, and
does not require Homebrew or developer tools.
Homebrew remains an alternative with `brew install zm2231/tap/agenthail`
followed by `brew services start agenthail`.

Update a package or Homebrew installation with `agenthail update`. Use
`agenthail update --check` to check a package installation without changing it.
`agenthail upgrade` is an alias. Source checkouts update with `git pull` and
`./install.sh`.

On Linux (amd64 or arm64) the release ships `agenthail-<tag>-linux-<arch>.tar.gz`
with a `.sha256`. Unpack it to `~/.local/share/agenthail`, link
`~/.local/bin/agenthail` to the binary, then run `agenthail daemon install`.
`agenthail update` replaces that directory in place, restarts the daemon and
rolls back when the restarted daemon does not answer on its API.

For a source checkout:

```bash
./install.sh
agenthail daemon install
agenthail doctor --json
```

AgentHail targets macOS and runs headless on Linux. Native Claude messages use local sockets and require
the daemon. Claude Remote Control and Notion read cookies from the system default browser's
last used profile unless pinned with `agenthail signin use <notion|claude> <profile>`;
`auto` returns to the default and `none` turns a consumer's sign-in off. `agenthail signin list`
shows what each consumer resolves to and any problem with its fix. Codex
Desktop uses AgentHail's loopback Desktop bridge (`agenthail launch codex`).
Without Desktop, AgentHail uses Codex's app-server daemon and starts it when a
conversation needs it. Codex 0.151 and older can only start that daemon from the
standalone runtime; `doctor --json` reports a typed runtime `problem` plus
non-failing `notes` with the exact install step
(`curl -fsSL https://chatgpt.com/codex/install.sh | sh`). Never run that
installer on the user's behalf. Relevant overrides are
`AGENTHAIL_PYTHON`, `AGENTHAIL_CLAUDE_BIN`,
`AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT`, `AGENTHAIL_NOTION_SPACE`, and
`AGENTHAIL_NOTION_USER`. Never print browser cookies, dashboard tokens, or
credentials.

On macOS, agents AgentHail runs use the privacy grants of Agenthail Daemon.
When desktop automation (agent-hands) fails with a permission or
`bootstrapTimedOut` error, run `agenthail permissions` (or read `permissions` in
`doctor --json`): it lists Accessibility, Screen Recording and Automation per
target as granted or missing, with the System Settings pane for each. Granting
is the person's decision: tell them, and only run `agenthail permissions
request` when they ask, since it puts macOS prompts on their screen.

AgentHail reads these overrides from the environment of each CLI or daemon
process and stores shared session, alias, queue, relay, and delivery state in
`~/.agenthail/registry.db`. A launchd or systemd service does not inherit
variables that exist only in an interactive shell. `daemon install` records the
installing shell's `PATH`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR` and `AGENTHAIL_*`
overrides in the service; rerun it after changing them. Claude Code state is
read from `CLAUDE_CONFIG_DIR` when it is set, otherwise `~/.claude`, plus every
root listed as `claudeConfigDirs` in `~/.agenthail/config.json`. `doctor` shows
each root. A conversation present in two roots is refused rather than guessed.
`agenthail accounts [--json]` shows the account signed into each root, its
cached usage, and which roots share one account. `thread create claude
--account <name|email|dir>` starts the session under that root; `list --wide`
shows each Claude session's account.

`agenthail claude settings compact [--json]` shows each root's compaction
settings as its `settings.json` holds them: `autoCompactWindow`, each
`modelSettings.<model>.autoCompactWindow`, `autoCompactEnabled` and
`idleCompaction`, plus `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `DISABLE_AUTO_COMPACT`
or `DISABLE_COMPACT` when set in this environment, since they take precedence.
`agenthail claude settings compact [--root <root>] [--model <model>]
<tokens|auto|unset> [--enabled on|off] [--idle on|off]` edits only those keys
in that root's file (the primary root by default). Tokens run from 100000 to
1000000; `auto` is Claude's window for the model; `unset` removes the key. A
running Claude does not reread the file: a change applies to sessions started
after it. A hosted session's `contextUsage.autoCompact` is what the running
Claude reports (`source: "runtime"`), the value actually in effect.

One unhealthy surface does not block the others. Preserve the `errors` object
from JSON discovery output when reporting partial availability.

## Surface Contract

| Operation | Claude | Codex | Notion |
|---|---:|---:|---:|
| Find existing sessions | yes | yes | yes |
| Send and read replies | yes | yes | yes |
| Start a new session or thread | CLI or dashboard background | CLI, TTY, or dashboard | CLI |
| Stream, interrupt, steer, compact | yes | yes | no |
| Persistent session model | yes | yes | no |
| One-message model override | no | yes | yes |
| Goal status | yes | yes | no |
| Goal set, edit, clear | host or Remote Control | yes | no |
| Goal pause, resume, token budget | no | yes | no |

Claude's control capabilities depend on its transport. Native UDS messages
cannot execute slash commands, steer a running turn, or correlate message IDs
with transcript turns for `send --reply` / `--stream`. Use `last` to inspect
the transcript or native `SendMessage` for an explicit reply. Compact, model,
interrupt, steer and the `control` commands (context, usage, status, effort,
rename) require a Remote Control attachment. A session the Claude host runs has
no Remote Control: it takes interrupt, steer, model and permission-mode changes
through the host, and has no compact or `control` commands.
A receipt with `evidence: "transport_accepted"` only proves socket acceptance;
receiver policy can still hold or deny the message. Do not claim model delivery
from it. Report the immediate send status and target as returned: Sent, Queued,
or Submitted. Submitted is a neutral durable intent, not model completion.
Continue the task without adding receipt/read-state hedges or investigation
chores. Agenthail reports proven delivery problems separately; never resend
automatically.

The daemon registers the busiest and most recently active Codex/Notion agents
(up to 112) as individual Claude `ListAgents` peers, refreshing every 30
seconds. Other senders register on first send to Claude. A send never fails
because the peer pool is full: it displaces the least recently used peer
outside that set, or else the lowest ranked registered peer. `--from` must resolve to a session
or alias; otherwise identity is resolved in this order: `AGENTHAIL_SESSION_ID`,
`CLAUDE_CODE_SESSION_ID`, then `CODEX_THREAD_ID`, read when the command runs.
Agents Agenthail starts never inherit these from their launcher. Without identity, an
operator peer receives replies into history. Inbound messages to read-only/offline agents
are denied rather than silently queued. Peer histories include received
messages, rejection reasons, and asynchronous Claude receipts.

Claude model changes use the session's `/model` flow and require confirmation.
Notion supports one-message model overrides. Do not attempt streaming, steering,
interruption, compaction, persistent model changes, or goals on Notion.

Codex sessions have separate read and write boundaries:

| Codex conversation | Read | Send |
|---|:---:|:---:|
| Created by AgentHail, `agenthail codex` or another non-Desktop client | yes | yes, through Codex's daemon |
| Created in Codex Desktop, Desktop bridge reachable | yes | yes, through Desktop |
| Created in Codex Desktop, Desktop closed | yes | no |
| Loaded by a plain `codex` terminal that is still running | yes | no (writer conflict) |

Let AgentHail report read-only sessions. Do not claim a send succeeded when the
target has no writable transport.

## Find And Resolve Targets

With the daemon running, `list` shows the daemon's current scope: open Claude
sessions, busy or loaded Codex sessions active within the configured recent
window (five hours by default), and busy or recently active Notion threads.
`--all` lists every session that is not archived, and `--archived`
lists the archived ones. Use `--all` only when the user asks for history or a
target is older or missing from the current view. `--project <name|id>` keeps
one project's sessions; `agenthail project list` names the projects.

```bash
agenthail list --json
agenthail list --all --json
agenthail list --all --project agenthail --json
```

`doctor` is a health probe. Its per-surface session counts are full discovery
inventory counts, not current, active, or recent totals. Never use those counts
in a session summary. Session lists and counts must come from `list` output
after applying the current-view policy above. A surface discovery error means
that surface is unavailable for this call; report it and continue with sessions
from healthy surfaces. Do not describe an unavailable Notion surface as active
or configured.

Targets accept `@alias`, PID, session-ID prefix, cwd/name fragment, a
qualified `surface:target` value, or `host::target` for a paired host. A cwd/name fragment resolves to a family's
root session, never to one of its subagents. Address a Codex subagent by its
ID or as `@<parent>/<nickname>` (for example `@lead/Ada/Euclid` for a nested
one). `@lead/<nickname>` starts from your project's coordinator, on whichever
host runs it. Claude subagents are read-only and cannot be targeted. If AgentHail
reports ambiguity, show the candidates and ask for one qualification. Never
select heuristically.

Commands that deliver, `send`, `steer`, `queue`, the destination of
`relay add` and `channel add`, never take a cwd/name fragment. They take an
alias, a session ID or unique ID prefix, a Claude conversation ID or PID,
`surface:` with one of those, or `@<parent>/<nickname>`. A fragment is
refused and the error lists the sessions it matched with their IDs and
aliases. To deliver to a session someone named in words, find it with
`agenthail list --json`, then address it by its `@alias` or ID.

```bash
agenthail identify claude:<session-id> researcher
agenthail identify codex:<session-id> builder
agenthail identify notion:<thread-uuid> notes
agenthail identify list
agenthail identify rm notes
```

Each session has one current alias. Naming the same session again replaces its previous alias.

## Start New Sessions And Threads

Create a writable Codex thread non-interactively:

```bash
agenthail thread create codex "Implement the verified fix" --alias builder --json
agenthail thread create codex --message - --cwd /path/to/project --model gpt-5.6-sol --approval on-request --json < task.md
```

The working directory defaults to the caller's current directory. The command
creates and registers the thread, starts its first turn, and returns the session
and delivery result. Keep the returned thread ID or alias. Valid approval modes
are `untrusted`, `on-request`, and `never`. Use `--timeout` to bound the complete
operation.

Create a Notion thread non-interactively:

```bash
agenthail send notion:new "Start a research thread" --reply --json
agenthail send notion:new:<name> "Draft the launch notes" --reply --json
```

Creation must happen immediately and cannot be queued. The receipt contains the
real persisted thread UUID. `notion:new:<name>` also stores `<name>` as a durable
AgentHail alias. Keep the UUID or alias for replies and future sends. Notion
chooses the visible title from the first message.

Create a Codex thread with `agenthail thread create codex`; it runs on Codex
Desktop when its bridge is reachable and on Codex's app-server daemon
otherwise, so it works on machines without Desktop. Start an interactive
terminal thread with `agenthail codex` in a human TTY. It uses the caller's
current directory unless `--cd` is provided. `agenthail launch codex` starts
Desktop with a writable bridge but does not itself create a thread.

Create Claude sessions with `agenthail thread create claude "task" --alias worker --json`.
The session runs in Agenthail's Claude host, which needs the daemon running. It
has no Remote Control. A tool its permission mode does not allow, and every
question it asks, waits for a person: `agenthail requests <target>` lists them
and `agenthail answer <target> allow|always|deny|"answer"` answers the oldest.
Do not answer them yourself unless your user asked you to.
A Claude CLI in a terminal shows its prompts the same way once
`agenthail hooks install` has added the PermissionRequest hook; the daemon
holds the prompt, so `requests` and `answer` need it running.
`agenthail permission-mode <target> <mode>` changes the mode while it runs.
A Claude session ID is the conversation ID (the transcript name). The process,
messaging socket and Remote Control session are attachments; Remote Control is
never the identity. `/branch` and `/clear` create a new session without an alias
(a branch shows `forkedFrom`); the alias, queue and routes stay on the original.
After `/branch`, `agenthail whoami` names the branch. Move an alias only with
`agenthail identify <target> <name>`.

Use `thread status`, `stop` or `resume` with the alias; a host session has no
`logs`, so read it with `agenthail last`. Preserve unknown launch outcomes;
inspect `agenthail list` before retrying.
Send to a Claude session whose process has ended (a stopped or finished
background job, or a closed terminal session) like any other session: the send
resumes it in the Claude host by its session ID and delivers the message once.
This holds for every sender, voice delegation included. Do not run `thread resume` first. The alias and session ID keep working
afterwards. If another process still holds the conversation but cannot take
messages yet, the send is queued and goes out once it can, or once that process
exits and the session is resumed. An `unknown` or `submitted` outcome means the
resume or the delivery may have happened; inspect the session before sending
again. `thread resume` remains for waking a session without a message; it
returns once the session can take messages, and a session Claude refuses to
start returns Claude's output.

Codex supports `thread fork <target>` and `thread queue <target> <list|add|update|delete|reorder|start>`.
Native queue adds require `--client-id <stable-id>`; reuse it only for retries
of the same submission. Native queue commands do not enter the Agenthail outbox.
`send` and `thread create codex` accept `--effort`, `--mode plan|default`,
`--service-tier` and `--output-schema <file>`. Preserve these fields on retries.
See [session operations](../../docs/maintainers/session-operations.md) for the
complete flags, API shapes, lifecycle limits and verification boundary.

## Send, Read, And Control

```bash
agenthail send @builder "Implement the confirmed fix." --reply --json --timeout 5m
agenthail send @builder "Implement the confirmed fix." --json
agenthail send @builder "Walk through the issue." --stream --timeout 2m
agenthail send @builder - --reply --json < prompt.txt

agenthail reply @builder --json
agenthail reply @builder --json --max-bytes 4000
agenthail last @builder 5 --full --json
agenthail stream @builder --timeout 10s
agenthail steer @builder "Prioritize the failing test."
agenthail interrupt @builder
agenthail compact @builder
agenthail model @builder
agenthail model @builder <model-name> --json
agenthail permission-mode @builder acceptEdits
agenthail requests @builder --json
agenthail answer @builder deny --reason "Not this file."
agenthail control @builder context --json
agenthail control @builder effort medium
agenthail goal @builder --json
agenthail goal @builder set "Ship the verified fix."
agenthail goal @builder pause
agenthail goal @builder resume
agenthail goal @builder budget 50000
agenthail goal @builder budget clear
agenthail goal @builder clear
```

Goals: Codex takes every goal subcommand. A Claude session takes `set`, `edit`
and `clear`, which run Claude's `/goal` once the session is idle and are
confirmed by Claude's answer; it has no `pause`, `resume` or `budget`, and a
session reached only through its peer socket cannot set a goal.

Claude controls over Remote Control report request-linked evidence. `model` and
`interrupt` wait for Claude's `control_response` to their own request id;
`interrupt` also waits for the active turn to end. A steer is only
`transport_accepted`, with the posted message id: Claude has not confirmed it
reached the turn. `control <target> context|usage|status|effort|rename` runs one
allowlisted Claude command and returns its output joined to the request. An
accepted control whose effect was not observed reports `unknown` with its request
id; check the session before retrying. Arbitrary slash commands are not exposed.

`compact` is a typed control, never a queued message. Claude sessions with a Remote Control attachment compact through the authenticated Claude bridge immediately; native socket-only Claude peers report compact as unsupported. Codex uses its native compact operation.

Messages a person types in the dashboard or the Mac/iPhone app carry `"author": "person"` and reach a Claude session with a Remote Control attachment as that person's own user turn, as do composer steers. A `send` action without `author`, and every CLI, relay, channel, voice or agent send to Claude, stays a native peer message whenever the session has a native socket; bridge-only sessions receive every send through Remote Control. Do not set `author: person` on a message an agent wrote.

`send` queues a busy target by default. `--no-queue` requires immediate
delivery. `--reply` waits for one new completed reply only when delivery is
immediate. A queued `--reply` returns the durable queue receipt. `--stream`
watches live deltas and cannot be combined with `--reply` or `--json`.

`--max-bytes N` caps `reply` text, and each `last` prompt and reply, at N bytes
without splitting a character. A cut field says so: JSON adds `truncated` and
`textBytes` to `reply`, and `userTruncated`/`userBytes` or
`assistantTruncated`/`assistantBytes` to an exchange, with the original size;
text output ends it with `[truncated: shown of total bytes]`. It cannot be
combined with `--report`.

Use `--model` only for a one-message Codex or Notion override. Use `--from` when
the receiving agent needs a human-readable sender label. Long content should go
through stdin.

## Coordinator

Each project may have a coordinator, the agent the person sends the project's
work to, on this machine or a paired host. Report to your project's coordinator
with `agenthail send @lead "..."`; `agenthail coordinator --json` shows its host,
name and current session. A send to the coordinator steers it
when busy (or queues when steering is unavailable), resumes it when ended, and
starts a new conversation from its saved instructions when the conversation is
gone. Do not set or clear the coordinator unless the person asks.

When you dispatch work to another agent, ask it to end its final message with a
completion report (below). When you finish work someone dispatched to you, end
your final message, and your report to `@lead`, with one.

## Completion Report

End the final message of a finished piece of work with exactly one completion
report: a fenced block whose info string is `agenthail-report`, holding one line
of JSON, with nothing after the closing fence.

````markdown
Fixed the export retry and pushed the branch.

```agenthail-report
{"v":1,"status":"done","stop":"complete","commits":["0d825e4d"],"paths":["internal/export/retry.go"],"next":"Deploy the export service"}
```
````

| Key | Required | Value |
| --- | --- | --- |
| `v` | yes | `1` |
| `status` | yes | `done`, `partial`, `blocked` or `failed` |
| `stop` | yes | Why you stopped: `complete`, `needsInput`, `needsApproval` or `gaveUp` |
| `reason` | unless `status` is `done` | What is missing or went wrong, at most 280 characters |
| `commits` | no | Commit ids you made, oldest first, at most 20 |
| `paths` | no | Files you changed or produced, relative to the repository, at most 50 |
| `next` | no | The one action the reader should take, at most 280 characters |

`stop` is `complete` exactly when `status` is `done`. A `blocked` report stops
with `needsInput` or `needsApproval`, a `failed` one with `gaveUp`, and a
`partial` one with any of those three. `blocked` means you need something named
in `reason` before you can go on.
The report is your own account and stays separate from what Agenthail observes
about the session: its goal (`agenthail goal <target> --json`, where `blocked`
means the goal was judged impossible), usage limits and turn errors. Do not
restate those in the report; a report that disagrees with them, such as `done`
while the goal is still active, is worth a reader's attention.

The JSON line is at most 2048 bytes; when the paths do not fit, list the ones that matter most. Leave out a key you have nothing for rather
than writing an empty value. Readers ignore keys they do not know; a change that
breaks this shape uses a new `v`. A report anywhere but the end of the final
message is quoted text, not a report.

The report is your claim, not evidence. A reader checks `commits` and `paths`
before acting on them. Read it with `--report`, which checks every rule above:

```bash
agenthail reply @builder --report
agenthail last @builder 5 --report
```

`reply --report` prints the report as JSON and exits non-zero, naming the broken
rule, when the latest reply has no valid report. `last --report` lists each
exchange's `report` or `reportError` and fails only when the newest exchange
has none, because earlier ones are often questions without a report. Neither
prints the reply text.

An Agenthail without `--report` can find the same report with jq, which does
not check the rules; keep the two in step if either changes:

```bash
agenthail last @builder 1 --json \
  | jq -e '.exchanges[-1].assistant | capture("(?:\\A|\n)`{3}agenthail-report\n(?<r>[^\n]+)\n`{3}\\s*\\z").r | fromjson'
```

## Follow, Subscribe, And Notify

AgentHail has four distinct completion behaviors:

1. `send --reply` waits for one completed turn.
2. `stream` or `send --stream` watches one live Claude or Codex turn.
3. A relay durably subscribes to successful completions. Choose `--when turn_finished` (default) for each matching turn, or `--when goal_completed` for the latest pending result after an explicit completed goal and finished final turn. Goal handoffs require source goal support. A handoff to a closed Claude Code target resumes it. Active routes remain until removed; queued messages wait until the target can take them.
4. Daemon notifications alert the human through the AgentHail Mac app and,
   when paired, the optional iPhone app.

Persistent agent-to-agent subscription:

```bash
agenthail relay add @researcher @builder
agenthail relay add @researcher @builder 'FAIL|NO-SHIP|root cause'
agenthail relay add @researcher @builder 'READY' --once
agenthail relay add @researcher @builder --when goal_completed
agenthail relay list [--json]
agenthail relay rm <id>
```

The first target is the completion source. The second receives each matching
completed reply. The optional filter is a regular expression. Relays reject
self-routes and cycles, remember queued completion IDs across restarts, and
require the daemon. `relay list` derives per-route firing evidence from that
enqueue ledger, reporting each rule's fire count and last-fired timestamp in
both text and `--json` output. `--once` deactivates the rule after its first
matching completion while retaining that firing evidence. A relay that cannot be
delivered is reported as a delivery problem to the human; the source
agent is not sent a failure notice, because its reply to that notice would be
relayed again.

Human completion notifications:

```bash
agenthail daemon notify on
agenthail daemon notify status
agenthail daemon notify test
agenthail daemon notify settings
agenthail daemon notify off
```

Mac notifications require System Settings authorization. iPhone notifications
require pairing the phone from Operations and allowing notifications on iOS.
They are not agent-to-agent relays. When a host session waits on an approval or
a single question, its notification offers buttons that answer it, and a
**Reply** that answers in the person's words or denies with a reason; the human
presses them, never an agent.

There is no generic CLI callback that subscribes an arbitrary Photon, Slack, or
webhook conversation to future queued completions. Do not promise automatic
delivery back to the current messaging conversation unless the host application
implements that callback itself.

When a user says "subscribe," determine whether they mean a one-turn reply, a
live stream, a persistent relay to another agent, or a human notification.

## Durable Queue And History

```bash
agenthail queue @builder "Then add focused tests."
agenthail queue list --json
agenthail queue list --all --json
agenthail queue list --target @builder --json
agenthail queue list --mine --json
agenthail queue list --cwd /workspace/agenthail --json
agenthail queue retry <id>
agenthail queue rm <id>
agenthail queue clear @builder
agenthail history --json
agenthail history @builder 25 --json
```

The daemon delivers queued work when a target becomes idle. Queue delivery is
ordered per session. Known pre-dispatch failures retry with bounded backoff.
Repeated failures become dead letters. Uncertain external effects remain in
audit history and are never automatically resent. Proven problems are reported
by the daemon. Pending messages never expire; they leave the queue only when
delivered, canceled, or failed. Each pending row reports `heldBy` (`waiting`,
`busy`, `offline`, `unknown`, `refused`, `retrying`, `notLoaded`,
`hostUnreachable` (a send a paired host has not received), `behind` an earlier
message) and its age in text output. Text output shows each row's status, with
its delivery evidence beside it when they differ: `delivered (remote queued)`
is a send a paired host accepted into its own queue. `refused` means the target
refused to start, for example Claude failing to resume the conversation; the
row's last error says why, and the sender receives one
`[agenthail delivery held ...]` notice. It needs a person, not a resend. A
target whose turn is waiting on its user (a Claude question, plan approval or
permission prompt, a Codex approval or
input request) carries `waitingOn` and lists as `wait`; messages to it,
including steers, are queued until the user answers. Do not interrupt it to get
a message through. A Claude session between turns whose background tasks
(a background shell, an async agent, a Monitor) still run carries
`backgroundTasks` with `running: true` and lists as `bg`: its work is not done,
and Claude starts a turn of its own when a task reports. Ambient tasks, such as
file watchers, are listed with `ambient: true` but do not count. Finished items appear in history and remain available through
`queue list --all`. `--target` filters by destination, `--mine` includes
messages sent by or addressed to the caller session, `--project` keeps targets
in one project, and `--cwd` keeps targets in the project that holds that
folder, including sessions in its worktrees.

## Channels

```bash
agenthail channel create launch
agenthail channel add launch @researcher
agenthail channel add launch @builder
agenthail channel list
agenthail channel send launch "The release is ready for review." --from @builder
agenthail channel rm launch @researcher
agenthail channel rm launch --all
```

Channels broadcast to every member. Busy members queue. Partial failure exits
nonzero so the caller can report which deliveries failed.

## Paired Hosts

Another Agenthail daemon on the tailnet can be paired so its sessions appear
here. Pairing needs remote access on the other host and both daemons running.

```bash
agenthail hosts                     # this host, paired hosts, state, session counts
agenthail hosts invite              # on the other host: prints a pairing link
agenthail hosts add '<link>' --address <tailnet-ip>
agenthail hosts remove forge
agenthail hosts name mac
```

Address a paired host's session as `forge::@builder` or `forge::<id>`.
An unqualified target that matches on two hosts is an error listing each
`host::target`; show the candidates and ask which one. `send`, `steer`,
`interrupt`, `reply`, `last`, `stream` and `thread create --host forge` reach
paired hosts, and `--cwd` with `--host` is a path on that host. A session on an
unreachable host stays listed as `down` with the host named. A send to it is
held by the local daemon (`queued`, held by `hostUnreachable`) and delivered
once when the host returns; it never expires. `delivery submitted` means the outcome is unknown, so do not resend. Other
commands refuse remote sessions.

## Daemon

```bash
agenthail daemon status
agenthail daemon start
agenthail daemon stop
agenthail daemon restart
agenthail daemon install
agenthail daemon uninstall
```

Homebrew manages its service with `brew services start|stop|restart agenthail`.
On macOS, `daemon install` creates a supervised launchd service; remove it with
`daemon uninstall`, not `daemon stop`. On Linux, `daemon install` creates the
systemd user service `agenthail.service` with `KillMode=process`, so stop,
restart and update leave the agents the daemon started running, and enables
lingering so the daemon survives logout. When logind refuses lingering without
root, install prints `linger: off` and the exact `sudo loginctl enable-linger
<user>` command; do not run sudo on the user's behalf. `daemon start|stop|restart`
drive the unit through `systemctl --user`. `doctor --json` reports Linux daemon
problems under `daemon.problems`, each with a `fix`: unreachable systemd user
manager, linger off, service not installed or inactive, Claude missing from the
daemon `PATH`, a `CODEX_HOME` or state directory that cannot run programs or
hold SQLite, and a service whose `CODEX_HOME` or `CLAUDE_CONFIG_DIR` differs
from the calling shell. Check daemon status before relying on queues, relays,
or background observation.

## Dashboard

```bash
agenthail dashboard enable
agenthail dashboard enable --no-open
agenthail dashboard disable
agenthail dashboard status
agenthail dashboard
agenthail dashboard config --codex-recent-hours 12
agenthail dashboard remote
agenthail dashboard remote status
agenthail dashboard remote off
```

Package installs enable the local dashboard on loopback behind a per-install
token. Manual and source installs use `dashboard enable`. Remote access uses a
private Tailscale Serve route. Pair the native iPhone app from Operations; the
browser-access QR is separate and contains the dashboard token. Share browser
access only when the user explicitly requests it.

## Starting And Launching Surfaces

```bash
agenthail thread create codex "Start this task" --alias builder --json
agenthail launch codex
agenthail codex
```

`thread create codex` starts a thread without a TTY, on Desktop when its bridge
is reachable and on Codex's app-server daemon otherwise. `launch codex`
opens Codex if needed and verifies its local connection. `agenthail codex` starts
an interactive writable terminal session and requires a human TTY.
Claude sessions start in the Claude host with `thread create claude`, which
needs the daemon running; Claude authentication must already be configured. Notion requires a signed-in browser.

## Verification Boundary

Non-mutating checks:

```bash
agenthail version --json
agenthail update --check
agenthail doctor --json
agenthail list --json
agenthail daemon status
agenthail queue list --json
agenthail history 10 --json
agenthail identify list
agenthail channel list
agenthail relay list --json
agenthail dashboard status
```

Status commands may exit nonzero for stopped, disabled, unavailable, or partly
unhealthy states. Preserve and interpret their output instead of hiding it.
Sending, queueing, steering, interruption, compaction, model or goal changes,
aliases, channels, relays, notification changes, dashboard changes, and daemon
lifecycle operations mutate local state or active sessions. Run them only when
the user's request calls for them, then verify the resulting status or receipt.

Relay completion capture and per-route consumption use durable SQLite cursors.
Check `pendingCount`, `eventCursor`, and `lastError` in `relay list --json` when a handoff waits.
Capture/enqueue failures retain retry state. Delivery failure records a human-visible
problem without sending a failure notice into the relay source. Do not equate a queued
receipt with destination consumption. See `docs/maintainers/durable-relays.md` for recovery.
