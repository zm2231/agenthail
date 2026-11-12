# Agent card: what a session knows about itself

An agent Agenthail manages has no other way to learn its alias, who started it, who it reports to or how its questions reach the person. The agent card is that answer, computed when asked from the registry of the host that runs the session.

## Data

`internal/agentcard` builds a `Card` from a registered session (`Sources.For`):

| Field | Source |
|---|---|
| `alias` | `aliases` |
| `host` | this host's name, only when paired hosts exist |
| `project` | `daemon.ResolvePathProject` on the session's folder: the registered project, not the folder's name |
| `lead` | `ProjectCoordinators` for that project; `self` when the coordinator's alias points at this session; `leadProblem` when two hosts both have one |
| `startedBy` | `session_origins` |
| `reportTo` | the creator when an agent started the session, else `@lead` unless the session is the coordinator |
| `decides` | true for the coordinator itself, and for a session that started one Agenthail would tell when it is blocked (`coordinator.Service.PromptHearer`, the same rule the daemon's notices follow) |
| `ask` | the runtime's question tool, whether Agenthail can answer it, and whether a paired phone has a push target |
| `mcp` | the session holds a live credential for Agenthail's MCP server (`LiveCredentialHash`): every Claude host session and every Codex thread Agenthail starts (which gets a card at all only with the Codex hooks installed, below) |
| `commands` | the few commands that act on the fields above, with exact syntax: MCP tool calls when `mcp` is true, CLI commands otherwise |

`ask.answerable` is true for a Claude host session, and for a terminal Claude whose config root has the `PermissionRequest` hook (`agenthail hooks install`). A `claude --bg` job and Codex sessions are notified but answered where they run.

The brief sends questions where reports go. A session that reports to an agent (its creator or `@lead`) is told to ask that agent with `agenthail send`. One that reports to the person (they started it with no coordinator, or it is the coordinator) is told to use its runtime's question tool, which Agenthail shows as Needs you and on the phone, unless its own instructions forbid it; then it asks in its reply.

A session with `decides` is told that the person has it answer those sessions' permission prompts and questions itself, every time, and that this authority comes from the person through Agenthail rather than from the peer whose notice reports the prompt. It reads the request with `agenthail requests <target> --json`, allows what fits the session's task, denies what is unsafe with the safe form in the reason, and goes to the person only when the effect is hard to undo or reaches outside the task, or it can't judge it. The card names Claude's rm guard, which can fire even in bypass mode, as the common case. The blocked notice comes from Agenthail, never from the blocked session: a lead the Claude host runs takes it as Agenthail's own turn starting with `[agenthail]`, and a lead in a terminal as a peer message from Agenthail's sender. The notice only reports the prompt, so the authority has to come from the card.

A session with `mcp` is taught the tools instead of the CLI: the guide, the ask line, the decide rules and every command line name `agenthail_send`, `agenthail_create`, `agenthail_wait`, `agenthail_requests` and the rest, with their JSON arguments, under "Your Agenthail tools". Both forms of each line come from one table, `agenttools.CardLines`, so they cannot drift. A credential that appears after the session's card was given changes only its one-line summary, so such a session keeps the CLI card, which still works.

A session Agenthail starts is recorded before its first turn runs: the starter calls `surface.SessionReady` once the session has its id (a Codex thread after `thread/start`, with its credential already claimed; a Claude host conversation after `open`), and every caller that starts one registers it, binds its alias and records who started it there (`RecordStartingSession`): the dashboard's `session-create`, with or without a starter launcher (`claude-sdk`, `codex-app-server`), `thread create`, and a coordinator's start. The first turn's hooks therefore find the session, its origin and its credential, so its first card is the tool card and names who it reports to. A session that cannot be recorded there is not sent its first turn, and the caller reports a storage failure, not a failed delivery. A `claude-bg` job takes its first message on the command line, so it has no such point and is recorded after it starts, as before; it runs without Agenthail's MCP server.

`agenthail whoami --json` prints the card; `agenthail help agent` renders it (`agentcard.Render`). A caller Agenthail cannot name gets `resolved: false` and the reason, never an error.

## Telling a Claude session its card

A session Agenthail's Claude host runs gets this with no hook installed: the host registers the same command as SDK callbacks ([Claude host](claude-host.md)), passing `--claude-pid` so its claim matches an installed hook's. For a terminal Claude:

`agenthail hooks install` adds `exec '<agenthail>' hook session-context` (timeout 10s, no matcher) as a `SessionStart` and a `UserPromptSubmit` hook in every Claude config root, beside the `PermissionRequest` hook and a `Stop` hook running `hook stop --surface claude --terminal` ([work record](work-record.md)). The hook reads Claude's input on stdin, finds the session by its `session_id` (registry, then the local Claude adapter, which registers a live one; never a paired host), and prints `hookSpecificOutput.additionalContext`:

- `SessionStart` (startup, resume, clear, compact): the rendered card.
- `UserPromptSubmit`: the card if the session was never given one, `agentcard.RenderChange` (one line) if the card's fingerprint differs from the one it was given, otherwise nothing.

Claude runs the hook from every root that has it at once, so each print is claimed in `session_context(session_id, fingerprint, start_key, delivered_at_ms)` first and only the claimant prints. A start is keyed by its `source` and the Claude pid; copies of one start arrive within `sessionStartWindow` (30s) and a later start of the same kind does not. A change is a conditional update on the fingerprint (`Card.Fingerprint`, a hash of the rendered card, so a field it never shows, such as the folder, changes nothing). The hook opens the registry only when it already has this build's schema (`registry.OpenCurrent`: no file, migration or journal-mode change), because Claude or Codex may run a different agenthail build than the daemon's; a registry of any other schema prints nothing. Any failure, including an unknown session, prints nothing, and the hook gives up after 5s, registry locks included, so it never holds a prompt for long. The row follows a Claude rekey. `hooks status` and `doctor` report a root as installed only with all four hooks current, each under a matcher that runs it for everything (none or `*`); install moves one out of a narrower entry and leaves that entry's other hooks where they are.

## Telling a Codex session its card

Codex sessions, the ones Agenthail starts included, get the card only after `agenthail hooks install codex` (`surfaces.InstallCodexContext`), which puts a `SessionStart` and a `UserPromptSubmit` command hook running `exec '<agenthail>' hook session-context --surface codex` (timeout 10s) in the person's own Codex config, and has Codex trust them. Codex sends the hook Claude's input shape (`session_id` is the thread id). The hook runs inside the app-server that holds the thread, with that process's environment, so it opens the registry of the app-server's user, and Codex runs it for every thread, `codex exec` and review threads included. It therefore answers only for a thread the registry already knows and prints nothing for any other: no lookup through Codex and no registration. On a Codex whose app-server reports 0.163.0 or later in its `initialize` `userAgent`, install also adds a `Stop` command hook running `exec '<agenthail>' hook stop --surface codex` (timeout 10s), which holds a turn that ends without `agenthail_end_turn`, or a lead's reply that asks without an ask, once ([work record](work-record.md)).

Install runs its own `codex app-server` over stdio with the person's environment (`codexStdioClient`), never the shared daemon, and ends it with the caller's deadline. It:

- Adds the hooks to the hooks file the person already uses: `CODEX_HOME/hooks.json` when it exists, where Agenthail's handlers are deleted and its group appended by path with sjson, so every other byte stays, and the file keeps its mode; otherwise `config.toml` through `config/read` (user layer) and `config/batchWrite`, which keeps the file's comments and other keys. Every `config/batchWrite` carries the user layer's `expectedVersion`, which Codex reports even before `config.toml` exists, is refused without one, and is rebuilt from a fresh read, up to three times, when a running Codex wrote the config in between. Writing to only one of the two avoids Codex's warning about hooks in both. Agenthail's handlers are recognised by command (`ownsCodexContextHook`, any absolute `agenthail` path), so install replaces an older binary's hooks and keeps every other handler, of any type, in place.
- Has Codex trust them: `hooks/list` over `CODEX_HOME` gives each hook's key and `currentHash`, and any of Agenthail's that is not trusted gets `hooks.state."<key>".trusted_hash` set to that hash. Agenthail never computes Codex's hash, so a Codex release that changes it needs only another install. Codex keys a hook by the resolved path of the file that declares it, `<path>:<event>:<group>:<handler>`; on macOS the temp directory sits under `/var`, a symlink to `/private/var`, so a key is only ever taken from `hooks/list`, never built from a path.
- Writes `agentcard.Guide` between `<!-- agenthail:begin -->` and `<!-- agenthail:end -->` at the end of `CODEX_HOME/AGENTS.md`, after one added newline that uninstall removes with it so the person's text is left byte for byte, which Codex loads as instructions for every session. A project's `AGENTS.md` is never touched. A begin marker without an end marker is an error, not a guess.

Install starts Codex before it writes anything, so a machine without Codex is left untouched, and runs only from a binary named `agenthail`, the name its hooks are recognised by. Uninstall removes the block (deleting `AGENTS.md` if nothing else is left), Agenthail's handlers and the groups they leave empty, and their `hooks.state` entries. `hooks status codex` and `doctor` (`codexHooks`) report `installed` when every hook this Codex takes runs this binary with timeout 10s, no other Agenthail hook is left, Codex trusts them and the block is current, and "installed; the end-of-turn nudge needs Codex 0.163.0 or later" for the same on an older Codex (a note, not a failure); `untrusted` when only the trust is missing ("Codex hook not trusted yet: run agenthail hooks install codex or trust it in Codex"); `missing` when none of it is there; `outdated` otherwise. With no Codex home they report nothing.

## Who started a session

`session_origins(session_id, via, creator_session_id)` holds one row per session Agenthail created; discovered sessions have none. `via` is `agent` (with the creator), `person` or `coordinator`.

- `thread create` resolves the caller as `send` does (`--from`, then `AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`) before it starts anything. A caller variable naming a session this registry does not know is refused with the same remedies as `send`. No caller is the person. From an agent's shell the start takes the caller's ceiling, as `agenthail_create` does, and only on this host (see mcp.md, "The CLI from an agent's shell"). Every start, a person's terminal included, is the daemon's `session-create`, so the session gets its agenthail MCP server and the daemon's placement, worktree and name handling; with the daemon stopped, `thread create` refuses and names `agenthail daemon start`. `--timeout` bounds the wait for the daemon's reply; the daemon stops the start when the command gives up.
- The dashboard's `session-create` takes `sourceSessionId`; without it the person started the session. `thread create --host` sends the caller as `sourceSessionId`, which host forwarding qualifies like any sender (`routeSource`): the receiving host stores the mirrored row or the no-reply stand-in.
- A coordinator conversation Agenthail starts is recorded as `coordinator`.
- `thread fork` records the caller the same way, in the CLI, the API's `session-fork` and on a paired host.
- A move to another host carries the origin in its manifest. The target names the creator as its own session, the source's mirrored one, or a stand-in with no reply address (`incomingOrigin`).
- A session the person starts outside Agenthail, including `agenthail codex`, is discovered and has no origin.

The row survives a Claude rekey (`rekeySessionTx`), as both the session and the creator.

## Tests

`go test ./internal/cli -run 'TestASession|TestThreadCreate|TestThreadFork|Whoami|TestAClaudeSession|TestTheCodexContextHook|Hooks'`, `go test ./internal/registry -run Schema29`, `go test ./internal/surface/surfaces -run TestHooksInstallCodex` and `go test ./internal/daemon -run 'TestDashboardRecordsWhichSession|TestAnIdleClaudeSessionMovesToAPairedHost'`.
