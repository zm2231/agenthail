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
| `ask` | the runtime's question tool, whether Agenthail can answer it, and whether a paired phone has a push target |
| `commands` | the few commands that act on the fields above, with exact syntax |

`ask.answerable` is true for a Claude host session, and for a terminal Claude whose config root has the `PermissionRequest` hook (`agenthail hooks install`). A `claude --bg` job and Codex sessions are notified but answered where they run.

`agenthail whoami --json` prints the card; `agenthail help agent` renders it (`agentcard.Render`). A caller Agenthail cannot name gets `resolved: false` and the reason, never an error.

## Who started a session

`session_origins(session_id, via, creator_session_id)` holds one row per session Agenthail created; discovered sessions have none. `via` is `agent` (with the creator), `person` or `coordinator`.

- `thread create` resolves the caller as `send` does (`--from`, then `AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`) before it starts anything. A caller variable naming a session this registry does not know is refused with the same remedies as `send`. No caller is the person.
- The dashboard's `session-create` takes `sourceSessionId`; without it the person started the session. `thread create --host` sends the caller as `sourceSessionId`, which host forwarding qualifies like any sender (`routeSource`): the receiving host stores the mirrored row or the no-reply stand-in.
- A coordinator conversation Agenthail starts is recorded as `coordinator`.
- `thread fork` records the caller the same way, in the CLI, the API's `session-fork` and on a paired host.
- A move to another host carries the origin in its manifest. The target names the creator as its own session, the source's mirrored one, or a stand-in with no reply address (`incomingOrigin`).
- A session the person starts outside Agenthail, including `agenthail codex`, is discovered and has no origin.

The row survives a Claude rekey (`rekeySessionTx`), as both the session and the creator.

## Tests

`go test ./internal/cli -run 'TestASession|TestThreadCreate|TestThreadFork|Whoami'` and `go test ./internal/daemon -run 'TestDashboardRecordsWhichSession|TestAnIdleClaudeSessionMovesToAPairedHost'`.
