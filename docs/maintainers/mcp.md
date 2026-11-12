# MCP server: session credentials, injection, tools, scope and limits

The daemon serves an MCP server named `agenthail` at `/mcp` on its own listener (`127.0.0.1:7412` by default). It uses the official Go SDK (`github.com/modelcontextprotocol/go-sdk`) over streamable HTTP in stateless mode with JSON responses: every request carries its own bearer, and no `Mcp-Session-Id` state is kept.

The server negotiates only protocol revisions `2026-07-28`, `2025-11-25` and `2025-06-18`, the ones with `structuredContent`, which every tool result uses. A client that asks for an older revision is answered with `2025-11-25`.

## Who may call it

- Only `ahs_` session credentials are accepted. The dashboard token and paired-device tokens are refused.
- Only callers on this machine are accepted. A request from a non-loopback address, or one carrying forwarding headers (`Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host`, `X-Real-Ip`, `Tailscale-User-Login`), is refused with `403 remote_refused`. Remote access publishes the listener through a proxy that also arrives from loopback, so the address alone is not enough.
- A cross-origin browser request is refused (`http.CrossOriginProtection`), and the SDK's localhost DNS-rebinding check stays on.
- All of this runs before any MCP method. A refused request gets an HTTP error with `{error: {code, message}, retryable}`:

| Status | Code | When |
| --- | --- | --- |
| 401 | `unauthenticated` | No credential, or one that is unknown |
| 401 | `credential_expired` | `detail.reason: "idle"`: unused for a day while the session is not running, `retryable: true`, revived when the daemon next sends the session a message. `detail.reason: "max"`: 30 days old, `retryable: false`, only a newly issued credential works |
| 401 | `credential_revoked` | Rotated, revoked, or its session was archived or moved to another host; `detail.reason` names which |
| 403 | `remote_refused` | Not a local caller |

## Session credentials

The registry table `session_credentials` holds at most one live credential per session (a partial unique index on `session_id` where `revoked_at_ms=0`).

- **Token:** `ahs_` plus 32 random bytes in base64url. Only its SHA-256 is stored; the raw token is returned once, when it is minted, and is never logged.
- **Identity only:** a credential names a session and its expiry, nothing else. It carries no capabilities, so a change in what a session may do never touches its token.
- **Issue:** `issueSessionCredential` reuses the live credential until its maximum expiry and otherwise mints a new one. A reused credential's token cannot be recovered, so the caller keeps the token it was first given.
- **Rotation:** a credential is replaced only when a runtime must be given a token this daemon process does not hold (after a restart, since the registry stores hashes) or when it reached its maximum expiry. `Rotate` returns the held live token when there is one; concurrent resumes of one session share a per-session lock and one token.
- **Idle expiry, 24 hours:** every authenticated call refreshes it. An expired credential is suspended, not revoked, and two things renew it:
  - The daemon handing the session work: a send on any route (queue drain, direct send, relay, channel and notice deliveries all go through it), a steer, or a goal control (set, clear, pause, resume, budget, and a held Claude `/goal` when it runs). Compaction does not count, since its turn calls no tools. The credential is refreshed just before the message goes out, so the turn it starts can call tools.
  - A call from a session that is running. A turn can start where the daemon never sees it, such as a Claude peer message or a Claude background task notification, so the call is accepted and renews the credential when the session's surface already knows it runs, without a new probe: for Claude, the session record of the session's recorded process (or of its holder, when another process holds the conversation) names the conversation and its `procStart` matches the running process, read from `/proc` on Linux and `sysctl` on macOS, so a reused pid does not count; for Codex, an app-server connection has the thread loaded.

  Otherwise the call is refused, and `daemon.log` gets a line naming the session and why.
- **Maximum expiry, 30 days from issue:** nothing extends it, and a turn does not revive it. Issuing again after it mints a fresh credential.
- **Revocation:** archiving a session revokes its credential in the same transaction. Authentication also revokes a credential whose session is archived or now belongs to another host. Deleting a session deletes its credentials.

## How a session gets the server

Every Claude conversation the host runs and every Codex thread Agenthail starts, resumes or forks is given the `agenthail` server with its own bearer.

Its tools run without asking for approval in both runtimes. The credential's capabilities are the gate, so a per-call prompt would only stall agents (and a voice operator mid-call) without adding a check.

The raw token exists in these places only, never in the registry or a log:

- the daemon that minted it, in memory, until the daemon exits;
- the runtime that uses it: the Claude host's session (until the conversation closes) and Codex's loaded thread config;
- for a terminal launch, the launch file described below, until the CLI takes it or it is swept.

**Pending credentials.** A new session has no row to bind a credential to when its token must already be in the start call, and a Codex thread has no id until `thread/start` returns. So the daemon mints the credential unbound, with a `pending_ref` and a short lifetime:

- an unbound credential never authenticates. Within its lifetime it passes the MCP handshake with no caller, because Codex connects during `thread/start` and keeps the tools it lists then for the thread's first turn; every tool call is refused (`unauthenticated`, retryable) until it binds. Past its lifetime it gets a 401;
- the ref is claimed once for one session id (`claimed_session_id`); a second claim, or one after the lifetime, is refused;
- the claimed credential binds when that session registers, in the registration transaction, and replaces any live credential the session had;
- an unbound credential past its lifetime is deleted when the daemon next issues one and when the MCP server starts.

The lifetime is the start timeout plus its naming margin (50 s) for starts the daemon runs itself, and the pending-launch lifetime (5 min) for terminal launches.

**Codex.** Every `thread/start`, `thread/resume` and `thread/fork` goes through `codexThreadRequest`, which adds `config.mcp_servers.agenthail` (`url`, `http_headers.Authorization`, `tool_timeout_sec: 3700`, `default_tools_approval_mode: "approve"`). `TestOnlyCodexThreadRequestSendsThreadStartResumeOrFork` fails if any other code sends those methods.

- Start and fork carry a pending credential and claim it for the returned thread id.
- Codex connects a thread's MCP servers from the config of the call that loads the thread. A resume of a thread the app-server already has loaded joins it bare, since differing config would make Codex rebuild an unsubscribed thread and abort a starting turn (see `docs/codex-protocol.md`). A resume that loads the thread carries the session's token when the daemon holds it. When the thread is loaded, or the daemon holds no token (after it restarts), the resume goes out bare and `mcpServerStatus/list` for that thread shows whether the server is connected. If not, the credential is rotated and the thread is unsubscribed and resumed with the new token, then checked again; a thread another client keeps loaded is logged and gets the server at its next load. While a turn is starting or running the thread is not reloaded; the missing server is logged and the next resume once the thread is idle attaches it.
- A held token is offered only while its credential is within its maximum expiry. When the session has no such credential, its next input reloads the thread even if Codex has it loaded and its MCP status shows connected, so a token past its limit is replaced rather than kept.

**Terminal launches.** A tmux or cmux Codex launch runs `agenthail codex` in the terminal, which starts the thread itself. The daemon mints a pending credential for the launch; the launcher writes it to `~/.agenthail/launches/<launch id>.mcp` (mode 0600) before the terminal starts, and removes it if the launch never starts. Files older than the pending-launch lifetime are swept whenever the daemon issues a launch credential or starts the MCP server. The CLI takes the file (reads it once and removes it) before any other step that can fail, starts its thread with the token, and claims the ref through `POST /api/v1/mcp/claim` (`{ref, threadId}`, dashboard token). The claimed credential binds when the daemon registers the located session.

**Claude host.** Every host `open` goes through `hostOpen`, which passes `mcp: {url, token}`; `TestOnlyHostOpenOpensAClaudeHostConversation` keeps it the only caller. The host gives Claude an http MCP server named `agenthail` whose tools run without asking (`allowedTools: ["mcp__agenthail"]`). A new conversation claims a pending credential for the id chosen before `open`; a resume sends the held token, or rotates the session's credential, or, for a conversation not yet registered here, claims a pending one. Opening a conversation the host already runs with a different token replaces the server on the live query (`setMcpServers`). When a retiring host still holds the conversation, `open` hands that host the new token through `replaceMcp`. A swap the server refuses is returned as `mcpError` on the opened session and logged; the host records a new token only after its swap succeeds, so the next open retries a failed one. A narrowing checker opens without the server.

## Permissions are computed at each call

`mcpAuth` resolves the bearer to its session, then reads what that session may do from durable state on every request:

- **Default capabilities:** `read.self`, `read.project`, `spawn`, `control.descendants`, `requests.answer`, `ledger.read`, `ledger.write`, `browser.drive` and `simulator.drive`.
- **Project lead:** a session in the registry's local lead set (`LocalLeadSessions`) also holds `control.project`.
- **Voice operator:** the session holding the `@voice` alias that voice registered (with a `session_origins` row `via='voice'`) also holds `control.all` and `read.all`. A replaced operator loses them on its next call.
- **Ceiling:** the session's row in `session_ceilings`, or unbounded (`{}`) when it has none, as for every session a person starts (see [Ceiling](#ceiling)).

A lead or operator change, made by the daemon, by another process writing the registry, or while the daemon was stopped, applies to the session's next call with the same token. No token is minted or pushed, so a runtime the daemon cannot reach (a Codex thread another client keeps loaded, a conversation no host has open) is never left holding a refused token. A coordinator whose start fails before the handover never enters the lead set, so its token never gains `control.project`.

`tools/list` returns every tool. A call the caller's capabilities do not cover is refused with `capability_missing`, so a session that gains or loses a role needs no new tool list.

Each tool's name and description come from `agenttools.Tools`, which `registerMCPTools` walks in order, binding each to its handler; a tool without a handler stops the daemon at start. The tools most sessions call, `agenthail_send`, `agenthail_create`, `agenthail_wait`, `agenthail_result`, `agenthail_end_turn`, `agenthail_ask` and `agenthail_pr`, carry `_meta["anthropic/alwaysLoad"]: true`, so Claude lists them in every turn instead of behind tool search; every other tool stays deferred. `internal/agenttools/budget.json` pins the bytes of the whole tool list, of the always-loaded tools, and of the CLI and tool forms of the session card. `TestTheToolListAgentsSeeStaysAtItsPinnedSize` and `TestASessionWithAgenthailsToolsIsTaughtTheToolsAndATerminalOneTheCLI` fail on any change in size, so a change that grows what agents read raises the numbers in the same diff.

## Read scope

A session reads:

- itself (`read.self`);
- every session it spawned, transitively through `session_origins.creator_session_id` (`control.descendants`);
- the live local sessions of its own project (`read.project`). A session's project is the registered project containing its cwd; a session with no project reads only itself and its descendants.

Archived sessions are outside every scope. `agenthail_sessions` also lists the paired hosts' sessions this host mirrors and can currently reach, under the same rules: those whose owning host puts them in the caller's project (matched by the project id that is the same on every computer) with `read.project`, every one with `read.all`. Each carries its host-qualified id and a `host` with the paired host's `id`, `name` and `state`. Every other read tool stays on this host. A session outside the caller's scope is refused with `out_of_scope`; an unknown one with `not_found`.

## Tools

| Tool | What it returns |
| --- | --- |
| `agenthail_whoami` | The calling session, alias, surface, cwd, host, project, coordinator role, capabilities, ceiling and credential expiry |
| `agenthail_capabilities` | Capabilities, ceiling, read reach, configured surfaces and limits |
| `agenthail_sessions` | Visible sessions with a `mine`, `project` or `all-visible` filter, paged with `cursor` |
| `agenthail_read` | Timeline items of one visible session: the latest page; older pages through `before`; newer journal items through `since` (pass back `latestSeq`); a clipped item's full body through `bodyRef` and `offset` |
| `agenthail_requests` | The visible sessions that are waiting on a prompt or question, or the open requests of one session, in one call without a cursor; `requestId` for one request's longer prompt |
| `agenthail_ledger_read` | The caller's project ledger, read by the same code as `GET /api/v1/ledger`: the board (capture, sync, counts, goals, newest provisional words), one list paged with `cursor` (`goals`, `asks`, `decisions`, `questions`, `work`), the top of the ranked `ready` queue, or one work item or ask by `id` (`item`) |

`agenthail_read` and `/api/v1/session` share `readSessionPage`, and `agenthail_requests` and `/api/v1/requests` share `openRequests`.

| Tool | What it does | Needs |
| --- | --- | --- |
| `agenthail_projects` | The projects on this host and its paired hosts with their coordinators; `conflict` marks a project with coordinators on two hosts | any session |
| `agenthail_history` | Delivery history (`view: history`, optionally one `sessionId`) or the delivery queue (`view: queue`), newest first and paged with `before` | `read.all` |
| `agenthail_send` | A message to a session (`target`) or a project's coordinator (`project`, starting its conversation if it has none). `mode: queue` (default) or `now` | control scope; see below |
| `agenthail_steer`, `agenthail_interrupt` | Steer or interrupt a running turn | control scope, down only |
| `agenthail_goal` | Set, edit, clear, pause or resume a Codex session's goal, or change its token budget | control scope, down only |
| `agenthail_answer` | Answer an open request by its ask id: allow or deny a permission request (`always` keeps allowing with the rule the request offers), answer an agent's ask, or pass either on with `escalate` | Only the session the request is routed to, or the voice operator (`control.all`) for the person; a permission request also needs `requests.answer`, and `always` needs `control.all` |
| `agenthail_session_settings` | Read a session's effective model, effort, permission and config root with where each came from; or change its model, permission mode or effort, or compact it | read: any session the caller can read; change: control scope, down only, and a permission mode stays within the target's ceiling unless the caller has `control.all` |
| `agenthail_alias` | Name a session | control scope, down only |
| `agenthail_archive` | Archive or restore a session | control scope, down only |
| `agenthail_create` | Start a Claude or Codex session with a first message, within the caller's ceiling, or a Notion thread | `spawn`; Notion needs `control.all` |
| `agenthail_end_turn` | The last call of a turn: a headline and up to 3 next items. Refused, without ending the turn, when invalid or when the turn has already ended. See [work-record.md](work-record.md) | any session, about its own turn |
| `agenthail_ask` | Raise an ask, routed to the caller's lead or spawner, else the person, or withdraw one of the caller's open asks. See [asks.md](asks.md) | any session, about its own work |
| `agenthail_report` | A task's child reports `done` or `failed` with its result; the first report settles the task and later ones update `latest` | the child of a task |
| `agenthail_result` | A task's state, result and `latest`, by `taskId` or child `target`, clipped to the output limit | the parent, or a reader of the child |
| `agenthail_wait` | Wait until a task settles, its child asks for something, or its goal stops | the parent, or a reader of the child |
| `agenthail_channel_send` | Post to a channel or project as the caller | any session; a project only its own, unless `control.all` |
| `agenthail_channel` | Create, delete, rename or list channels and change their members | `control.all` |
| `agenthail_relay` | Add, remove or list relays | `control.all` |
| `agenthail_queue` | Retry or cancel one queued instruction, or clear a session's pending ones | `control.all` |
| `agenthail_coordinator` | Set or clear a project's coordinator | `control.all` |
| `agenthail_ledger_write` | `attach`, `clause`, `question` or `work` in the caller's project ledger, recorded as the caller; ownership cannot be set; a request key the operation does not define is refused, and a work add or edit that sends a nested key at the top is told where it goes (`fields.type`, `change.priority`) | `ledger.write` |
| `agenthail_browser` | List this host's listening ports and the session that started each (`servers`, see [Servers](browser-host.md#servers)), and drive tabs in the host's headless browser: `open`, `navigate`, `snapshot` (whole, interactive only, or one part), `text`, `links`, `scroll`, `dismiss`, `screenshot` (optionally saved to a path inside the session's folders), `act`, `wait`, `dialog`, `resize`, `list`, `close`. A snapshot, text or link list too long for the tool result limit returns its head and the file that holds all of it. Each session has its own browser context and sees only its own tabs; see [the browser host](browser-host.md#tabs) | `browser.drive` |
| `agenthail_simulator` | Lease one of a Mac's two shared iOS simulators (`acquire`, `renew`, `release`, `list`), install a `.app` from the session's own output folder, `launch`, `terminate`, `openUrl`, read the screen (`describe`, with refs), `tap`, `type`, `swipe`, `button`, `screenshot`, `push`, `location` and `logs`. One session holds a device at a time, and a lease ends after 10 idle minutes or 60 in all unless renewed; see [the simulator host](simulator-host.md) | `simulator.drive` |

Every write runs the same shared code as the dashboard and CLI, with the session's principal, so it is validated, refused and recorded the same way: `agenthail_ledger_write` through the ledger's own write route (`ledgerWriteRoute`, the handler behind `/api/v1/ledger/<op>`), and every other write through `performAction`. Both take the same idempotency key and record the same audit entry. Every write acts as the calling session (its principal, and the source of its audit entry) and never carries a person author. The message tools (`agenthail_send`, `agenthail_steer`, `agenthail_channel_send`) also set `sourceSessionId` to the caller, so the message is attributed to that session, as the CLI's `agenthail send` from a session is, and `agenthail_create` sets it for a Claude or Codex start so the new session's spawner is the caller. Other writes leave it empty, since on those actions it means something else (a fork's source, a coordinator start's creator).

### Tasks

`agenthail_create` with `task: true` records a task for the new session, a row in `tasks`, and replies with its `taskId`. If the first turn's outcome is unknown, the reply is `{taskId, sessionId, state: "running", outcome: "unknown"}`, and the task still counts. A task belongs to one child on the same host; moving the child to another host or archiving it cancels the task.

The first of these settles a running task, and only one can:

1. The child calls `agenthail_report`.
2. A turn has finished with no active goal: the child is idle, with no background work running. The task settles with that turn's text, as `failed` with its error, or as `done` with empty text if the turn wrote none. An interrupted turn settles as `failed`.
3. The goal completes. A turn that finishes under a goal that isn't complete, or whose state can't be read, leaves the task running. A stopped goal does not settle the task.
4. A first turn that definitely failed settles as `failed`.
5. The child's session is gone for good: it is offline, off the catalog, and its surface can't resume it. The task settles with the last turn a goal held open, or as `failed` if there is none.

The observer checks this on every pass. Running tasks' children are always watched, including an exited Claude child, whose transcript can still be read. So a turn settles however long ago it ended, and a daemon restart needs no reconciling: the first pass sees the same state. The goal is read at once for a newly finished turn and otherwise every 30 seconds. A goal read as complete is followed by a fresh observation, so the turn that completed it has finished before it settles. A failing observation backs off to at most a minute between tries, the same as any watched session, so it delays a task but can't starve it.

The first terminal turn is the result, even if a follow-up was already on its way. That follow-up's turn goes to `latest`.

Codex lists a running turn as `interrupted` with no `completedAt` whenever its thread is not active: to a process that hasn't loaded the thread, while the turn is starting, and after the server that ran it crashed. Such a turn settles nothing, and relays and completion notices don't treat it as the turn's end; a turn Codex recorded an end for carries `completedAt`. The same read can't tell a running turn from one whose owner died, so such a child stays open until it is archived or its turn is seen to complete, and holds no spawn slot meanwhile. A child that exited but can still be resumed also stays open and holds no slot. A child's row is not pruned while it has a task, so a result stays readable.

After settling, a report only updates `latest`, and `agenthail_result` records a turn that finished since. Settled children are not watched. Until a task settles, its idle child counts toward the caller's running-session limit. Rows are durable, so a result survives a daemon restart.

`agenthail_wait {tasks, timeoutSec, cursor}` takes the event cursor, reads the tasks' durable state, and returns if anything is ready: a settled task, an open request on a running child, or a stopped goal. Otherwise it subscribes from that cursor, so an event between the read and the subscribe still wakes it, and reads again on a relevant event, a dropped subscription, or every 30 seconds. A daemon restart ends the call; the next wait starts from the database. The returned cursor records what has been reported, so the next wait doesn't repeat it. It holds no state on the daemon. Inside a Codex code-mode `exec`, the script yields a cell id after its `yield_time_ms` while the wait keeps running; Codex's `wait` tool resumes the cell for the result.

The default is 600 seconds and the maximum 3600; `0` only looks. Hosted Claude and Codex sessions run the agenthail server with a 3700-second tool timeout. A Claude session Agenthail doesn't host keeps Claude's own limit, measured at 60 seconds on 2.1.318, so its waits return at 45 seconds with the cursor.

A wait that times out sets `wake` on the running tasks the caller started. Each observer pass sends one queued notice, keyed `task-settled-<id>`, for a settled task with `wake` set, then clears it, so a settlement from any path or a restart in between still gets exactly one. Reading the result, by `agenthail_result` or a wait, clears `wake` and cancels a notice not yet delivered. A request a wait returned withdraws the waiter's own notice for it, including one not yet queued; the person is still notified. A notice the queue has already started delivering can't be recalled and may arrive just after the read.

### Control scope

`mcpControlTarget` resolves the target with the CLI's resolver (`internal/targets`): `@lead`, a coordinator's `@name`, `host::@alias`, `host::id`, and an unqualified target looked up on this host and every paired host, where a match on more than one is refused as `invalid`, never picked. A host or target that matches nothing is `not_found`. A session on a paired host is reached only by a queued `agenthail_send`, under the same rules as a local one, so crossing a host never widens a caller's reach:

- `control.all` reaches any of them;
- otherwise the session must be in the caller's project (`out_of_scope`, `detail.rule: different_project`) and not archived there (`detail.rule: archived`);
- `mode: now` and every other write refuse it (`detail.rule: not_queued`);
- a target the paired host matched only by a name fragment is `invalid`.

`agenthail_channel_send` to a project (`#<project>`) reaches that project's workers, so it takes the same reach: a caller posts only to its own project's channel unless it holds `control.all`, and is otherwise refused with `out_of_scope`, `detail.rule: different_project`, before anything is sent. Another project's coordinator stays reachable by a queued `agenthail_send` with `project`. A stored channel keeps its members, and the person's posts are not limited. The CLI's `agenthail channel send` takes the same rule when run from an agent's shell (see below) and is not limited in the person's terminal. A paired host applies the same rule to a project post that reaches it (see hosts.md).

The project and the archive come from the paired host's own answer to `/api/v1/resolve` (its `project` and `archived` fields), never from this host's mirror, which can lag a move or an archive. When the host cannot answer, the target is matched against the mirror only to name it, and the send is refused as retryable `unavailable` (`detail.rule: project_unconfirmed`).

A local target is then decided in this order:

1. The caller's own session is `invalid`.
2. A session above the caller (its spawner chain through `session_origins`) is reachable only by a queued `agenthail_send`; every other write refuses it with `out_of_scope`. This comes before any capability, so a coordinator never controls the session that spawned it, even in its own project.
3. `control.all` reaches every other local session.
4. `control.descendants` reaches the sessions the caller spawned, transitively, and `control.project` reaches its project's live sessions, each only when the target's ceiling is within the caller's (`exceeds_ceiling` otherwise).
5. The caller's project coordinator on this host is reachable only by a queued `agenthail_send`.
6. Any other non-archived session in the caller's project is reachable only by a queued `agenthail_send`, so sessions in a project can message each other; an archived one is refused with `detail.rule: archived`, and every other write with `detail.rule: not_spawned_or_led`. A session in another project is refused with `not_spawned_or_led` and is reached through its coordinator, with `project`.

`agenthail_send` with `project` reaches any project's coordinator as a queued message, so a project's coordinator is reachable by project from every session. `mode: now` needs `control.all`: to the caller's own coordinator it is refused as control flowing up, and to another project's with `out_of_scope`, `detail.rule: different_project`.

### The CLI from an agent's shell

The CLI run in a session's shell (`AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` or `CODEX_THREAD_ID` set) takes the same decisions before every write, from `POST /api/v1/scope` (`api_scope.go`), which builds the session's caller with `agentCaller`, the same function `mcpAuth` uses, and answers with the MCP functions:

| `action` | CLI commands | Decided by |
|---|---|---|
| `send` | `send`, `queue <target>`; `mode: now` with `--no-queue`, which delivers at once or fails | `mcpControlTarget`; when queued, the caller's spawner chain and any session in its project are allowed |
| `lead-send` | `send` or `queue` to a coordinator by name, `@lead` or a coordinator session; `mode: now` with `--no-queue` | `mcpLeadScope` |
| `control` | `steer`, `interrupt`, `compact`, `model <name>`, `control effort\|rename`, `goal <op>`, `answer` (before its requests are read), `permission-mode <mode>`, `thread stop\|resume\|fork\|archive\|pin\|snooze\|wake\|adopt\|clean`, `identify`, `pr --session`, `limit-resume resume\|cancel`, `move` | `mcpControlTarget`; the session itself is allowed |
| `ledger` | every `ledger` write (all but its reads: the board, `asks`, `ask`, `show`, `goals`, `questions`, `digest`, and `work list\|show\|ready\|next\|critical-path\|search`), for `--project` or the folder's project | `ledger.write` and the caller's own project, as `agenthail_ledger_write`; `out_of_scope`, `detail.rule: different_project` otherwise |
| `channel-send` | `channel send` | `mcpProjectPostScope` |
| `create` | `thread create` | `mcpCreateScope`, then `mcpChildrenRefusal` and the start under the spawner's lock, as `agenthail_create`: the daemon starts the child (`session-create`, sourced from the caller, with its mode or approval and ceiling) and returns that action's reply. Only the fields `agenthail_create` and `thread create` share pass through (surface, cwd, message, model, alias, name, agent, mode or approval, worktree isolation, effort, mode, service tier, output schema). A `configRoot` (`--root`) must name a configured root, and is allowed when it is the root of the caller's own Claude transcript, or when the caller is its project's coordinator or holds `control.all`; any other root is refused (`out_of_scope`, `detail.rule: config_root`). Without one, the child is placed as for any session, on its creator's root first |
| `operator` | `queue rm\|clear`, `relay add\|rm`, `channel create\|add\|rm\|rename` | `control.all`, or the project's coordinator when every session the item or relay touches (`sessions`: a queue item's target and sender, a relay's two ends) is in its project, or the channel (`channel`) is its project's |
| `operator` | `coordinator set\|new\|clear`, `limit-resume on\|off`, `relay rm host::id`, `project add\|remove\|rename\|archive\|unarchive` | `control.all` |

A refusal is the MCP shape (`error.code`, `detail.rule`), printed as `refused (<code>, rule <rule>): <message>`. An agent's `thread create --host` on another host is refused: the ceiling cannot be carried there, as `agenthail_create` has no host either. The endpoint answers only loopback callers with the dashboard token. With the daemon stopped these writes refuse. `--from` never changes the caller; the person's terminal, with none of the variables set, is not checked. Machine setup commands (`daemon`, `hosts`, `devices`, `signin`, `permissions`, `person`, `roots`, `proxies`, `claude settings`, `triggers`, `hooks`, `launch`, `registry`, `ui`, `update`, `debug`) are not checked.

This is a guardrail against an agent reaching too far by following instructions, not a security boundary: every session runs as the person's OS user and can read the dashboard token, unset its identity variables, or reach the Codex and Claude sockets directly. A boundary needs OS separation.

### Ceiling

A ceiling bounds what a session started over MCP may do: `permission` (ranked `plan` < `ask` < `edits` < `auto` < `full`) and `roots` (folders). An empty axis is unbounded.

- Claude modes rank `plan` → plan, `default`/`manual`/`dontAsk` → ask, `acceptEdits` → edits, `auto` → auto, `bypassPermissions` → full. Codex approvals rank `untrusted` → ask, `on-request` → edits, `never` → full; Codex has no plan rank.
- `agenthail_create` refuses a permission mode above the caller's rank, or a `cwd` outside its roots, with `exceeds_ceiling` and `detail.axis`. Without a mode, the child starts at the caller's rank. The child's ceiling is that rank and the caller's roots, or its own `cwd` when the caller has none.
- The ceiling is recorded in `pending_ceilings` against the pending credential minted for the start and moves into `session_ceilings` when the session registers; a person-started session has no row. It is read at each call, so the token never carries it.
- Sessions nest at most 3 deep below the one a person started (`depth_limit`), and a spawner runs at most 8 children at once (`children_limit`, retryable). The count is taken under a per-spawner lock, so racing creates cannot pass it.

### Idempotency

Every write takes a `clientRequestId`. The action receipt is keyed `<tool>:<clientRequestId>` per calling session and records a hash of the tool's arguments: a repeat with the same arguments replays the first result, and a repeat with different arguments is `idempotency_conflict`. The receipt is read before any capability, scope or ceiling check, so a retry replays even after the caller lost the role that let the first call through (a replaced voice operator, a lead that stepped down). A first request still running, or one whose outcome was not recorded, answers `busy` with `detail.outcome: "unknown"`; retrying with the same id reads its result.

### Audit

Each write the shared operation receives records a history entry with kind `mcp`: the target session, the caller as source, `"<tool> <clientRequestId>"` and the outcome (`ok` or the refusal code). A refusal made before the operation runs (scope, capability, ceiling, missing fields) is not recorded. An answer is also recorded by the shared operation as `request-<outcome>`, `answered by session <id>`.

### The voice operator

The voice operator has no command-line path. Its developer instructions name the `agenthail_*` tools, and its own call tools are only `voice_transfer` and `voice_return_to_orchestrator`. An operator created before this keeps its old instructions and tool set until the person chooses **New conversation**; the voice screen says so once.

## Output limits

- Every tool result is at most 16 KiB of JSON. A paged list (`agenthail_sessions`, the ledger lists, `agenthail_read`) stops at the byte budget and returns `truncated: true` with a `nextCursor`. A result that still exceeds the cap is refused with `invalid`.
- An item's text longer than 4 KiB is clipped at a character boundary and marked `truncated`. When the journal kept the full body, the item carries a `bodyRef`, and `agenthail_read` pages through it in whole-character chunks of up to 8 KiB.
- A list cursor continues one traversal. A traversal returns every entry that was in the list when it began, and is still in it, exactly once, and never repeats an entry. An entry that joins the list during a traversal may be missed; the next traversal from the start includes it. Cursors are keysets over an order that never changes for an entry: sessions by registry row order, ledger lists by id (each kind's ids count up in the order entries were made).
- The lists an agent acts on are not paged. `agenthail_requests` returns the waiting sessions, or one session's open requests, in one call, clipping prompts so they fit (at least 256 bytes each, `clipped: true` when cut); `requestId` returns one request with its prompt up to 4 KiB. When even the clipped rows exceed the 16 KiB cap, it returns the oldest that fit, with `total` and `truncated: true` and no cursor. `ready` is a ranked queue that reorders as work moves; it returns its first `limit` entries (at most 50) as compact rows with `total`.
- A read-back cursor names the page and the oldest item already returned, so a page the byte cap cut resumes inside that page. If the session's latest page moved on in between, the read is refused as `not_found` with `detail.stale: true`. Following a cursor past the oldest retained history returns an empty, final page.

## Tool refusals

A tool that refuses returns an MCP tool error (`isError: true`) whose structured content is `{code, message, retryable, detail}`. The tools' own checks refuse with `invalid`, `not_found`, `out_of_scope`, `capability_missing`, `exceeds_ceiling` (with `detail.axis`), `depth_limit`, `children_limit`, `unsupported_surface` and `idempotency_conflict`. Errors from the shared operations map onto these codes by explicit rules in `mcpCodeFor`:

- `ledger_owner_unreachable` → `owner_unreachable`, retryable;
- `ledger_owner_outdated` → `owner_outdated`, retryable: the host that keeps the ledger does not know a key this host sent, and the message names the host to update;
- any `400` → `invalid`; any `404` → `not_found`;
- `409 stale` → `not_found` with `detail.stale`;
- `409 moved` → `moved`, retryable, with `detail.movedToHost`: the session moved to that host, and the same call made again reaches it there;
- `409 move_busy` and `session_adopt_busy` → `busy`, retryable;
- other `409`s → `refused`;
- `502 control_failed` → `busy`, retryable;
- `502 *_unknown` → `busy` with `detail.outcome: "unknown"`, not retryable;
- other `502`s and any `500` → `refused`, retryable.

`TestEveryActionErrorMapsToAnMCPCode` fails when the parity table records an error code with no rule.
