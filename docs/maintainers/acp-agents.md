# ACP agents

Agenthail will host agents that speak the Agent Client Protocol (ACP, JSON-RPC
2.0 over stdio, protocol version 1) through one runtime written in Go. An
agent is a config entry: an id, a label and the command that starts its ACP
server. There is no per-agent code. The first agent is Devin (`devin acp`); a
second, such as `gemini --acp`, needs only another entry on the machine that
runs it. Claude and Codex
keep their own integrations.

This page names the pieces, what each owns, and how ACP sessions,
permissions, transcripts and restarts map onto what Agenthail already has for
Claude and Codex.

## What the protocol gives us

### Observed on the wire

Against `devin acp` 3000.14 over stdio:

- `initialize` returns `protocolVersion: 1`, `loadSession: true`,
  `sessionCapabilities` `list`, `delete`, `close` and `additionalDirectories`,
  no `resume` and no `fork`, `mcpCapabilities` `http` and `sse`, and a
  `devin-browser` auth method. The agent uses its own sign-in.
- `session/new {cwd, mcpServers}` returns the `sessionId` (a short slug such as
  `three-period`, unique only within the agent), `modes` and `configOptions`.
  Each config option has a `category` of `mode` or `model` and lists its
  values.
- `session/prompt` is one turn. While it runs, the agent sends
  `session/update` notifications. Seen: `user_message_chunk` (on replay),
  `agent_message_chunk`, `agent_thought_chunk`, `tool_call`,
  `tool_call_update`, `usage_update`, `session_info_update` (title),
  `current_mode_update`, `config_option_update` and
  `available_commands_update`. The prompt's response carries `stopReason` and
  `usage`; `end_turn` and `cancelled` were seen.
- `session/cancel` (a notification) ends the running turn: its prompt answers
  `stopReason: cancelled`, and the running tool's update reports `failed`.
- `session/request_permission {sessionId, toolCall, options}` carries only the
  `toolCallId` and vendor `_meta` in `toolCall`; the title, kind and
  `rawInput` came earlier in that call's `tool_call` update. Options have an
  agent-chosen `optionId`, a `name` and a `kind`. Devin offers up to six: one
  `allow_once`, one `reject_once`, and several `allow_always` with different
  reach, two of which switch the session's mode (`switch_accept_edits`,
  `switch_bypass`). A `reject_once` is honoured.
- Devin's `accept-edits` mode ran a shell command that writes inside the
  workspace without asking, so a mode is not a sandbox.
- `session/load {sessionId, cwd, mcpServers}` from a new process replays the
  whole conversation as `session/update` notifications and only then
  answers. The agent remembered the conversation afterwards.
- `session/set_config_option {sessionId, configId, value}` changes the mode
  and answers the new `configOptions`.
- `session/list {cwd}` lists the agent's sessions with title and `updatedAt`.
- With `fs.readTextFile` and `fs.writeTextFile` advertised, Devin routes its
  file tool through `fs/read_text_file` and `fs/write_text_file`, and treats a
  `null` write result as a parse error. With them off, its shell commands
  wrote files themselves; its file tool with them off is checked in the live
  run.
- Closing stdin ends `devin acp` with status 0.
- Devin sends many vendor notifications (`_cognition.ai/output`,
  `_cognition.ai/turn_stats`, …). Methods starting with `_` are extensions and
  are ignored.
- Devin runs the person's own agent hooks inside its sessions, the Agenthail
  `hook session-context` among them; each shows as a completed `tool_call`.
- Devin's `process_model` setting can be `daemon`, in which its own
  background daemon keeps a turn running after the `devin acp` client exits.
  A new client's `session/load` then replays the turn so far, re-sends its
  open permission request and streams the rest, but no response for the old
  prompt ever arrives. The built-in entry pins `--process-model in-process`,
  so the process Agenthail holds is the one running the turn.

The wire log of these runs is kept with the change's work record, not in the
repository, because it carries the machine's paths and hook output.

### From the protocol schema, not seen

`plan` updates, the stop reasons `max_tokens`, `max_turn_requests` and
`refusal`, the option kind `reject_always`, `session/set_mode`, and
`session/set_model`. The runtime handles them as the schema defines; the fake
agent in the tests sends each.

## Pieces

```
daemon ──┐                   ┌── agent process (devin acp)  session A
CLI ─────┼── acp-host.sock ──┤
         │   (agenthail      └── agent process (devin acp)  session B
         │    acp-host)
         └── reads ~/.agenthail/acp/<agent>/<session>.jsonl  (the host writes)
```

- `internal/acp`: the protocol. A JSON-RPC 2.0 connection over a reader and a
  writer, newline-delimited, one reader goroutine, requests matched by id in
  both directions, notifications fanned out, server-to-client requests
  dispatched to a handler. Typed params and results for the methods above.
  Unknown fields are kept as raw JSON so `_meta` survives. No agent names
  appear in it.
- `internal/acphost`: the host. `agenthail acp-host` is a process of the same
  binary that owns every agent process and its stdio, writes transcripts,
  holds open permission requests, and serves a newline-JSON protocol on
  `~/.agenthail/acp-host.sock` (directory 0700, socket 0600). Its client
  (`acphost.Client`) is what the daemon and the CLI use.
- `internal/surface/acpsurface`: the adapter. One `surface.Surface` per
  configured agent, all sharing one `acphost.Client`. `Name()` is the agent's
  id (`devin`); `RuntimeKinds()` is that agent's own runtime kind,
  `acp:devin`. `Adapters.For` routes a session by runtime kind and takes the
  first adapter claiming it (`internal/surface/runtime.go:107`), so a kind
  shared by every ACP agent would send a second agent's sessions to the
  first adapter.

### Why a host process and not the daemon itself

Neither Claude nor Codex runs its agent inside the daemon: Claude runs in the
Claude host (`internal/claudehost/manager.go`, started with `Setsid`), Codex
in its own `codex app-server daemon` (`internal/surface/codexsurface/runtime.go`).
Both keep running turns and open permission requests across a daemon restart
or update, and the daemon reattaches. ACP agents speak over stdio, so the
process holding the pipes holds the turn. If that were the daemon, every
daemon restart would cut every running ACP turn, drop its open permission
request, and lose the prompt's `stopReason`. The host is that process,
written in Go, supervised the way the Claude host is. The CLI calls adapters
in its own process (`internal/delivery/delivery.go:133`), and reaches the
host's socket as it reaches the Claude host's, without the daemon.

## Config

An agent is an entry under `acpAgents` in `~/.agenthail/config.json`, the
free-form file each package reads its own key from
(`surface.ReadAgenthailConfig`, as `proxies` and `claudeConfigDirs` are read):

```json
{
  "acpAgents": [
    {"id": "devin", "label": "Devin", "command": ["devin", "acp", "--process-model", "in-process"],
     "modes": {"ask": "plan", "plan": "plan", "accept-edits": "edits", "smart": "auto", "bypass": "full"}},
    {"id": "gemini", "label": "Gemini", "command": ["gemini", "--acp"]}
  ]
}
```

- `id`: the surface name everywhere (`agenthail thread create devin`,
  `agenthail_create {"surface": "devin"}`, the session row's `surface`).
  Lowercase letters, digits and `-`, at most 32 characters, and not `claude`,
  `codex`, `notion`, `agent` or `agenthail`. An invalid entry is skipped with a
  daemon log line and an `agenthail doctor` entry.
- `label`: the name the apps show.
- `command`: argv. `command[0]` is resolved on `PATH` as the daemon's
  environment has it, so an agent that is not installed is listed as not
  connected rather than failing at start.
- `env`: added to the agent's environment.
- `modes`: the rank of each of the agent's mode ids on the ceiling scale
  (`plan`, `ask`, `edits`, `auto`, `full`; `internal/daemon/mcp_ceiling.go:20`).
  See [Ceilings](#ceilings).

Devin is built in: with no `acpAgents` key, the list is the Devin entry above.
An entry with the same id replaces a built-in one, and
`{"id": "devin", "disabled": true}` removes it. The daemon and the CLI read
the config when they start, so a changed entry takes effect at the next
daemon restart. No agent is special-cased anywhere else.

## The host

### One host per user

The daemon supervises the host as it does the Claude host
(`internal/daemon/claude_host.go`): at start and every 30 seconds it dials
`acp-host.sock` and exchanges `hello {protocol, version}`. With nothing
answering it takes an exclusive `flock` on `~/.agenthail/acp-host/lock` and
starts `agenthail acp-host` with `Setsid`, the lock file as fd 3. The host
holds the lock for its lifetime; the kernel releases it when the host dies,
however it dies. Its log is `~/.agenthail/acp-host/host.log`.

A host of another version is replaced without cutting a turn. The daemon asks
it to `drain`: it moves to `~/.agenthail/acp-host.<pid>.sock`, releases the
lock and leaves `acp-host.sock`, so the daemon starts its own host at once.
The draining host refuses new sessions with `draining`, keeps serving its own
(send, cancel, respond, list, subscribe), closes each once it is idle with no
open request, and exits when its last session has closed. It exits then
whether or not anything is still connected to it, so drained hosts never
accumulate. A host that dies without exiting (killed, crashed) leaves its
`acp-host.<pid>.sock`; the client removes a private socket whose process is
gone the next time it looks for hosts. Saved sessions and held sessions are different
lists. The catalog's `List` reads the session records on disk, whichever host
wrote them. A host's `held` answer names only the sessions it has open, that
is, whose lock it holds. The client routes every operation on a session to
the host whose `held` names it, so respond and cancel for a busy session of a
draining host reach that host; a session no host holds goes to the current
host, which opens it. A session held by a draining host stays there until it
closes, after which its next send starts it in the current host. A session is never open in two hosts: a host opens a session only
while holding the session's lock file (`~/.agenthail/acp/<agent>/<id>.lock`,
an `flock` held for as long as the session is open in that host), so a second
host's open waits or is refused. Every file of a session is written only by
the host holding its lock, so two hosts never write the same file.

Every request the daemon makes of a host has a deadline sized from
measurement: each request is logged with its duration, and the deadline is set
with headroom over the slowest observed reply of its kind. A slow reply never
turns a delivered answer into an error:

- A request that changes something (send, respond, cancel, set a mode)
  carries a key the daemon chooses. The host keeps each key's state:
  `started` when it takes the request, `done` (with the result) once the
  effect is out, which for a send or respond is when its line is written to
  the agent's stdin, and `failed` when it gave up before writing. A request
  sent again with a known key gets that state instead of acting twice.
- When the daemon's wait ends first, it asks the host for the key's state,
  waiting as long as the state is `started`. `done` is success. `failed` is
  proven non-delivery, and the caller may retry. A lookup of a key the host
  has not seen records it `failed` in the same step, so the original request,
  if it arrives later, finds `failed` and does nothing; that makes "not seen"
  proven non-delivery too. A host that cannot be reached is an unknown
  outcome: the adapter returns it wrapped as `surface.DeliveryOutcomeUnknown`.
- Releasing a permission answer after an unknown outcome is safe for ACP,
  so `applyPermission` (`internal/daemon/permission_asks.go:304`), which
  releases the ask on any error, is unchanged. The host answers each agent
  request at most once: a second answer to a request it already answered, or
  no longer holds, is refused as stale and never reaches the agent. So when
  an answer's outcome is unknown and the ask is released, the person's next
  answer either reaches a request still open or is told the request was
  already answered (`surface.ErrRequestStale`, which the daemon shows as
  stale). Claude and Codex answers behave as before.
- A `list` that times out keeps the sessions last listed for that host
  rather than reporting them gone.

### Agent processes

One agent process per session, started by the host on first use:

- argv from the entry, working directory the session's `cwd`, environment the
  daemon's agent environment (`surface.AgentEnv`) plus the entry's `env` plus
  `AGENTHAIL_SESSION_ID=<session id>`, so `agenthail` run from the agent's
  shell or hooks knows its session as it does in a Claude or Codex session.
- a process group of its own (`surface.ProcessGroupCommand`), killed as a
  group when the session closes or the host exits. The session's record
  holds its running process's pid and start time; a host that starts after a
  crash kills any recorded group whose process is still the one recorded.
- `initialize` with `clientCapabilities {fs: {readTextFile: false,
  writeTextFile: false}, terminal: false}` and `clientInfo {name:
  "agenthail"}`. Agenthail serves no file system or terminal: the agent works
  in its own process on the same machine, and nothing Agenthail shows needs to
  sit in the middle of a file write. An agent that calls a client method
  anyway is answered `-32601`.

One process per session, not one per agent, because the session's identity
is in the environment (`AGENTHAIL_SESSION_ID`, the MCP credential), a crash
or a stuck agent costs one conversation, and cancel and close stay per
process. A process stops when its session has been idle 15 minutes with no
open request; the next send starts it again and loads the session. Measured
on Devin, a new process answers `initialize` in about 0.7 s and `session/load`
of a six-turn session in about 2.5 s more.

Each session has one state lock in the host, held only while its state
changes, never for a turn's length. Start and load hold it until the load
answers, so a prompt that arrives meanwhile waits and then runs. A prompt
takes it to mark the turn running and releases it; respond and cancel take it
briefly during the turn; a second prompt while a turn runs is refused
(below).

### Sessions

The Agenthail session id is a UUID the host mints. The agent's own
`sessionId` is not unique across agents. Each session has a record,
`~/.agenthail/acp/<agent>/<id>.json`, written atomically
(`internal/atomicfile`) by the host holding the session's lock: Agenthail
id, agent session id, cwd, created at, the running process's pid and start
time, and the session's latest title, mode, model, listed modes and models,
available commands and context usage. These are the updates that are state
rather than history, so the record is where they survive. There is no shared
index: listing reads the records in the agent's directory.

- Create: mint the id, start the process, `session/new {cwd, mcpServers}`,
  apply the mode (see [Ceilings](#ceilings)) and model with
  `session/set_config_option`, write the session's record and create its
  transcript file with its identity record, call
  `surface.SessionReady` (the daemon records the session row, as for Codex
  and Claude: `internal/daemon/dashboard_launcher.go:105`), then send the
  first prompt. A mode or model the agent did not list closes the new session
  and fails the create before `SessionReady`, so no row is left behind.
- Send: start the process and `session/load` if it is not running, then
  `session/prompt`. A session with a running turn refuses a second prompt,
  so the Agenthail queue holds the message as it does for a busy Codex
  thread. ACP has no steer; `Capabilities.Steer` is false.
- Interrupt: `session/cancel`. Every open permission request of the session
  is answered `cancelled` first, as the protocol requires, even with no turn
  running. A cancel and a prompt are written under one lock (not the one
  that covers reopening a session), so a cancel always reaches the agent
  ahead of any later prompt, and an agent ignores a cancel with nothing
  running; a cancel from a stop answer or a ceiling breach names its turn and
  does nothing once that turn has ended. An interrupt also covers a message
  still starting (the session reopening for it): that message is never sent
  to the agent and is refused with `interrupted`, which the adapter reports
  as unavailable. A direct send is returned to its sender as not sent, with
  no delivery problem; a queued message stays queued and is sent later, as
  after any interrupt.
- Model and mode: `session/set_config_option` with the option whose
  `category` is `model` or `mode`; an agent that lists `modes` but no config
  options gets `session/set_mode`, one with `models` gets
  `session/set_model`. A mode change is ranked against the session's ceiling
  first.
- Close and archive: `session/close` when the agent lists it, then stop the
  process. Archive does not delete the agent's own session.
- Listing: `List` is the records, so the catalog shows every ACP session
  Agenthail started. A session whose process is stopped is `idle`, not
  ended: a send starts it, so it stays a delivery target. Sessions the person
  started in the agent's own app are not adopted in this change.

### MCP

Each `session/new` and `session/load` carries the `agenthail` MCP server, so
an ACP session gets Agenthail's tools as a Claude or Codex one does
([mcp.md](mcp.md#how-a-session-gets-the-server)):

- When the agent lists `mcpCapabilities.http`: `{type: "http", name:
  "agenthail", url: <the daemon's /mcp>, headers: [{name: "Authorization",
  value: "Bearer ahs_…"}]}`. The credential is minted pending and claimed for
  the Agenthail id, as a Codex `thread/start` does; the id is known before
  `session/new`, so the claim never races the row.
- Otherwise the stdio relay, which every ACP agent must support: `{name:
  "agenthail", command: <agenthail>, args: ["mcp", "stdio"], env:
  [{name: "AGENTHAIL_SESSION_ID", value: <id>}]}`.

After a daemon restart the token is no longer held in memory, so the next
`session/load` carries a rotated one, as for a resumed Codex thread.

## Transcripts

ACP has no transcript file Agenthail can read, and `session/load` is the only
way to ask the agent for history: it needs a running process, takes the
session's lock, and costs seconds. So the host writes the transcript. Every
`session/update` that is history, every prompt Agenthail sends and every
prompt response is normalized and appended to
`~/.agenthail/acp/<agent>/<id>.jsonl` (0600) as it arrives, before it is
passed on. That file is to an ACP session what the rollout is to a Codex
thread: the record the adapter reads, which outlives the agent process, the
host and the daemon.

Records are whole lines written with one `write` to a file opened
`O_APPEND`. A host that starts finds a last line without its newline (a crash
mid-write) and truncates it; the update it held is lost, and the turn it
belonged to is closed as below.

Normalization, one record per timeline item, upserted by id:

| ACP update | Timeline item |
|---|---|
| prompt sent by Agenthail | `message`, role `user`, id `<turn>:user`, the text as sent; a sending agent's envelope is read when the item is presented, as for Codex |
| `user_message_chunk` (only on a rebuilding replay) | `message`, role `user` |
| `agent_message_chunk` | `message`, role `assistant`; consecutive chunks in a turn are one item, id `<turn>:a<n>`, upserted as text grows |
| `agent_thought_chunk` | `reasoning`, id `<turn>:r<n>`, same coalescing |
| `tool_call` | `toolCall`, id the `toolCallId`, title the call's `title`, text its `rawInput`, `status` |
| `tool_call_update` | upserts the `toolCall`'s status and title; its `content` and `rawOutput`, once the call is `completed` or `failed`, become a `toolResult` with that `callId` |
| `plan` | `event` titled Plan, text the entries with their status |
| prompt response | `done` for the turn, status from `stopReason` (`end_turn` completed, `cancelled` interrupted, the others failed with a `notice` naming the reason) |
| `usage_update`, `session_info_update`, mode, config and command updates | not items; the session's state in its record |

The adapter rides the path local Codex transcripts already take through the
session source ([session-stream-catalog.md](session-stream-catalog.md)):

- `LocalTranscriptProvider.RequiresLocalTranscript` is true, so the source
  treats the session as transcript-backed and catches up from its checkpoint
  on every seed (`internal/daemon/session_source_seed.go:141-170`), including
  after a daemon restart or while no one was following.
- `SessionReader` reads pages from the file and sets `TranscriptOffset`,
  `TranscriptOffsetSet` and `TranscriptIdentity`, a UUID the file's first
  record carries. A file's identity never changes, so the adapter needs no
  `TranscriptContinuer`.
- `TranscriptItemReader` derives every item at or after an offset.
- `Stream` follows the file from `session.TranscriptOffset`, the way the
  Codex Desktop rollout is followed, so the seed ends and the tail starts at
  the same byte and no update falls between them. The host's subscription
  only wakes it.

The source keys three things to Codex or Claude today, and they become keyed
to the adapter:

- `journalServable` asks `session.Surface == surface.KindCodex` before
  trusting `RequiresLocalTranscript` (`internal/daemon/session_source_seed.go:42`),
  and the missing-checkpoint error names Codex (`:150`).
- The derivation version. Today `journalServable` compares a journal against
  `max(transcriptJournalDerivation, codexJournalDerivation)`, the stale-journal
  rebuild runs only for Codex (`session_source_codex_derivation.go:14`), and
  `rederiveJournal` compares and marks the shared transcript version
  (`session_source_rebuild.go:113`). An adapter that writes its journal from a
  local transcript reports its own version (`JournalDerivation() int`, a new
  optional interface): Codex returns its current version, ACP starts at 1 and
  raises it when the normalization above changes. The stale-journal rebuild
  runs for any such adapter whose journal is older than its version, and
  `journalServable` compares against it. `rederiveJournal`, which re-reads
  Claude's prompt ids, stays Claude's: it runs only for an adapter that does
  not report a version. Every site that stamps a journal's version (the
  first seed, `internal/daemon/session_source_seed.go:222`, the stale rebuild
  and the rederive) takes it from one function of the adapter: its
  `JournalDerivation()` when it reports one, else today's constant. A fresh
  ACP journal is stamped 1, not the shared transcript version.

A `session/load` only ever runs on a process the host just started, whose
previous process has exited, so no turn of the session is running and the
replay is history the file already holds. The host does not append
`session/update` during a load, so the file never doubles. A request the
agent sends during a load is live and is handled as any other.

The file is created, with its identity record, before the session's row
exists (see Create), so every session Agenthail lists has one. Replay never
writes it: replay lacks what only Agenthail recorded (the message id of each
prompt, turn ids and outcomes, notices), so it cannot stand in for the file.
A listed session whose file is gone (someone deleted it) is not reopened: a
send fails with an error naming the file, raised as the existing "Message not
delivered" problem, and the session view keeps the rows its journal holds,
with the error shown above them. The session source reports a failed seed as
a `source-error` row (`internal/daemon/session_source_seed.go`). A failure
identical to the session's newest row takes that row's place under a new
sequence (`AppendSessionJournalSourceError` in
`internal/registry/session_journal.go`), so a persistent failure holds one row
and never pushes the conversation out of the journal, for every surface: a
Claude or Codex session whose transcript is gone gets the same. The
conversation is still in the agent, and a new session can carry on from it.

## Turns and status

The host knows the state of each session exactly, because it sent the prompt
and holds its response: idle, a running turn, or a running turn with an open
permission request, plus the open turn's id and the last turn's
`stopReason`. The adapter's `Observe` maps this to the status vocabulary the
daemon keeps (`internal/surface/surface.go:20`): a running turn is
`StatusBusy`, with `WaitingOn` the open requests when there are any, since
the daemon keeps permission asks only for a busy observation
(`internal/daemon/waiting.go:137`, `:152`); otherwise `StatusIdle`.
`CompletedTurnID` is the last turn with a `done`. `List` and `LocalStatus`
give the same, and the host's subscription drives the adapter's
`CatalogNotifier`, as Codex's watch does.

A turn whose process ended without a response (the agent crashed, the host
was killed) gets a `done` record with status failed and the reason "the agent
stopped during the turn", written when the host notices the process exit, or
at the host's next start for a file whose last turn has no `done`.

## Permissions

A `session/request_permission` is held open by the host with its JSON-RPC id
and becomes a tool approval through the adapter's `Responder`, so the daemon
raises it as an ask of origin `permission` ([asks.md](asks.md#permission-requests))
with no new path.

The request names only the tool call, so the host joins it to the call's
latest `tool_call` and `tool_call_update` state. The `surface.Request`:

- `ID`: a host-minted id, stable for the request's life. `Kind`:
  `approval`. `Title`: the tool call's `title`.
- `Tool` and `Input` in the shapes the approval card and `Detail` already
  render (`internal/surface/request_detail.go:52`): a call of kind `execute`
  with a `command` in its input is `Bash {command, cwd}`; a call whose content
  is a `diff` is `Write {file_path, content}` for a new file or
  `Edit {file_path, old_string, new_string}` otherwise; anything else keeps
  the call's `kind` as `Tool` and its `rawInput` as `Input`, which `Detail`
  shows as JSON. The person always sees the command or the change before
  approving it.
- `NoReason` is set (ACP carries no reason with a denial), `Stoppable` is
  set, and `AlwaysAllow` and `AlwaysDeny` are not.

Its actions are then the approval buttons the apps already show
(`internal/surface/request_actions.go:48`):

| Choice | Answer to the agent |
|---|---|
| `allow` | `selected` with the request's first `allow_once` option |
| `deny` | `selected` with its first `reject_once` option, else `cancelled` |
| `stop` | `cancelled`, then `session/cancel` |

A request with no `allow_once` option sets `NoAllow`, a new request flag:
`Actions()` then lists no `allow`, and `Resolve` refuses an `allow` choice,
so a button from an older notification cannot answer it with an outcome the
agent did not offer.

Options of kind `allow_always` and `reject_always` are never offered. Their
reach is the agent's to define and cannot be read generically: Devin's
include "always allow in all projects" and two that switch the session into
another mode, one of them `bypass`. Answering `allow` affects one tool call
only, so no answer from Agenthail can widen what the session may do. The
person can still widen it by changing the session's mode, which is ranked
against the session's ceiling.

The host withdraws the request when the agent's turn ends or the process
exits, which closes the ask. An open request lives in the host, so it
survives a daemon restart: the restarted daemon finds it on its first
`Requests` read and keeps the ask open.

## Ceilings

ACP's own permission options are not a sandbox: a mode such as Devin's
`accept-edits` or `bypass` runs tools without asking. A session's mode is
therefore ranked on the ceiling scale wherever it is set, through the entry's
`modes`:

- A mode the entry does not rank is `full`. An agent whose entry ranks no
  modes has every mode at `full`.
- `childCeiling` (`internal/daemon/mcp_ceiling.go:80`) takes the ranks from
  the entry for an ACP surface. A requested mode the agent's entry does not
  name is refused as unknown, as for Claude. With no mode requested under a
  ceiling, the session starts in the highest-ranked mode at or below the
  ceiling, preferring the agent's default when it qualifies; with none, the
  create is refused as `exceeds_ceiling`.
- Remote creation: the calling daemon cannot read the target machine's
  entry, and the two may differ. For an ACP surface it does not pick a mode
  (`remoteCreateScope`, `internal/daemon/mcp_create.go:196`); it forwards the
  requested mode, possibly empty, with the caller's ceiling, and the target
  host applies the rule above with its own entry in `session-create`, which
  already receives the ceiling. A target with no such agent refuses the
  create.
- A mode change from Agenthail (`session-settings`, the apps' mode picker) is
  refused above the session's ceiling.
- Agenthail never sends a permission option that changes the mode, so a
  bounded session's mode rises only if the agent changes it on its own. The
  host learns of that from `current_mode_update` or a mode config option it
  reports, after the fact. It then fails closed: it sends `session/cancel` to
  end the running turn, answers every open request `cancelled`, and sets the
  mode back with `session/set_config_option`. If setting it back fails, or the
  agent lists no setter, it stops the agent's process, and the session refuses
  prompts until the person changes its mode within the ceiling. Each step is
  recorded as a `notice` naming both modes. Tool calls the agent ran between
  its own change and the cancel cannot be undone; the ceiling bounds what
  Agenthail asks for and allows, not what an agent does against its own mode
  rules.

A session the person starts has no ceiling, as a Claude or Codex one has
none.

## Restarts

| What restarts | Running turn | Open permission request | Conversation |
|---|---|---|---|
| Daemon | keeps running in the host | stays open; the ask stays open | unaffected; the daemon reattaches and catches up from the transcript |
| Host (update) | the old host keeps it until it ends | as the turn | unaffected; the next send after it closes starts it in the new host |
| Host (crash or kill) | ends with its process; recorded failed | withdrawn | next send starts the agent and loads the session |
| Agent process (crash, idle stop) | recorded failed if one was running | withdrawn | next send loads the session |
| The machine | as a host crash | withdrawn | next send loads the session |

## Daemon, CLI and MCP

- `cmd/agenthail/main.go` adds one `acpsurface` adapter per configured agent
  to `app.Surfaces`, after Codex, sharing one host client.
- `surface.SurfaceKind` stays a string. The closed lists that refuse other
  surfaces accept a configured ACP agent:
  - `parseThreadCreateRequest` (`internal/cli/thread.go:152`),
  - `spawnRefusal` and the `agenthail_create` schema text
    (`internal/daemon/mcp_create.go:20`, `:40`, `:196`),
  - `NewLaunchers` and `defaultLauncherForSurface`: one launcher, `acp`,
    whose `Agents()` lists every configured ACP agent and whose launch starts
    the session through the adapter of the requested agent. The resolver
    keys launchers by id (`internal/daemon/launchers.go:15`), so one launcher
    per agent under the same id would leave only the last one reachable,
  - the queue scan's resume rule (`internal/daemon/observer.go:105`), so a
    queued message to an ACP session is delivered as it is to a Claude or
    Codex one,
  - the agent card's ask metadata (`internal/agentcard/card.go:216`): an ACP
    session asks with `agenthail_ask`.
- A coordinator stays a Claude or Codex conversation
  (`internal/daemon/coordinator.go:136`). Idle compaction and usage-limit
  resume stay Claude's.
- The registry needs no migration. A row's `surface` is the agent id,
  `runtime_kind` `acp:<id>`, `transport` `acp`, `source` `agenthail`.
- `GET /api/v1/session-options` lists each ACP agent with `label`,
  `workspace: true`, `connected` (the command resolves), and the `modes` and
  `models` its newest session listed, from its record. The adapter is a model
  lister, so `/api/models?surface=<id>` serves the same models with their
  names; an agent reports none until one of its sessions has opened.
- The dashboard state's surfaces carry the adapter's `label`.

## Native apps and dashboard

ACP sessions use the item kinds, requests and actions the apps already
render, so the session view and the queue need no change. What does:

- The dashboard's approval card (`internal/daemon/dashboard/js/requests.js`)
  and decision card (`decision-card.js`) draw their own Allow and Deny
  controls. They keep their layout and handlers and leave out Allow and
  Always allow when the request has `noAllow`, and the project's Now row
  opens such a request instead of offering Allow in place. The native card already renders `actions`
  (`native/RequestCard.swift`), as do notifications and the MCP requests view.
- The new-session form takes its agents from `session-options.surfaces` with
  their `label`, instead of a fixed list: `native/macOS/NewSessionDraft.swift`
  (`["claude", "codex", "notion"]`), `native/iOS/SessionCreation.swift` and
  the dashboard's `start-form.js`. An ACP agent's mode picker lists the
  `modes` from `session-options`; its model picker is the existing one, fed
  by `/api/models`. The project sheets' lead picker stays Claude and Codex,
  since a coordinator does.
- `native/AgenthailAPI.swift` `creationBody` sends `session-create` for any
  surface but Notion; for an ACP agent it sends the generic fields and the
  chosen mode as `permissionMode`.
- The new-session pickers name an agent by its `label` from
  `session-options`. Elsewhere the apps fall back to the capitalized surface
  (`SessionStyle.agentName`), which for an id like `devin` is its name. The
  dashboard's `format.js` learns labels from the surfaces it is sent, and
  draws an ACP agent's glyph as its label's first letter.
- Tool calls render by their title and status. ACP titles are already
  sentences ("Ran sleep", "Wrote created.txt"), so no tool names are mapped.

## Tests

- `internal/acp`: framing, id matching in both directions, a request the
  client does not serve, a notification during a response, a line past the
  size limit.
- A fake ACP agent (`internal/acp/acptest`, a Go program the tests build)
  that replies, streams chunks, calls a tool, asks permission with every
  option kind including a mode switch and honours the answer, runs a slow turn
  that cancel ends, replays on `session/load`, sends a `plan` and each stop
  reason, changes its own mode, and exits mid-turn on request. The surface,
  host and daemon tests run it through config entries of their own. Two
  entries (`fake-a`, `fake-b`) run side by side, and create, send, observe,
  read and respond for each reach its own adapter, which shows a second agent
  needs only an entry.
- Host: a daemon restart with a turn running and a request open; a drain with
  a turn running, ending in the old host's exit; a crash mid-turn and mid-line;
  concurrent send during a load; replay without doubling; a session whose
  transcript was deleted refuses to open and its view keeps its rows; a
  session lock that keeps one session out
  of two hosts.
- Source: seed, stop following, append, reconnect while another update is
  written; every item appears once in the page and the stream. A fresh ACP journal is stamped 1 and is rebuilt when the
  version is 2; Claude and Codex journals keep their versions.
- Deadlines: a respond whose reply is delayed past the deadline after the
  host delivered it reports success from the key's state; one the host never
  started is released; a lookup that overtakes its request fences the key, and
  the late request does nothing; after a release, a second answer to a
  request the host already answered is stale and never reaches the agent.
- Missing transcript: opening the session many times past the journal's
  retention leaves its conversation rows readable and one error row. An
  error, a recovery with enough items to span pages, and a second failure
  show the second failure on a fresh page; recovering clears it. A follower held
  behind the first error gets one gap per replacement, and its page reload
  shows the current error with every retained row.
- Routing during a drain: respond and cancel for a busy session of the old
  host reach the old host; an idle saved session opens in the new host.
- Every approval view (dashboard approval and decision cards, native card,
  notifications, MCP requests) shows no Allow for a `NoAllow` request.
- Two hosts during a drain write different sessions of one agent at once and
  both sessions' records survive.
- Ceilings: bounded creates with and without a mode, locally and on a paired
  host whose entry ranks modes differently; a request without `allow_once`
  takes no `allow`.
- Permissions: the approval shows the command joined from the earlier tool
  call; `allow`, `deny` and `stop` send the right outcome; no `allow_always`
  option is ever selected; an agent's own mode change above the ceiling is set
  back.
- The live check runs the branch build with a scratch home against the
  installed, signed-in Devin on a Mac: create from the app, prompt, tool
  calls in the session view, a permission answered from Agenthail, cancel, a
  daemon restart mid-conversation, and the conversation continued.

## Not in this change

- Adopting sessions started in an agent's own app (`session/list` makes it
  possible later).
- `session/fork`, which Devin does not list.
- Serving `fs/*` or `terminal/*`.
- Elicitation (`elicitation/create`), which Agenthail does not advertise.
- Attachments and images in prompts. Devin lists the `image` prompt
  capability; sending images is a later change.
- A coordinator that is an ACP agent.
