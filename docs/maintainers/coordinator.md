# Coordinator

Each project can have one coordinator, the agent its sessions report to and a person sends the project's work to. It is a role on an ordinary session, not a new kind of session.

## Projects

A coordinator belongs to a project (see [projects](projects.md)). The CLI and the daemon resolve a folder's project with `daemon.ResolvePathProject`, the resolution the catalog uses, so a folder lands in the same project everywhere. A folder outside every project has no coordinator: `coordinator new`, `coordinator set` and `@lead` refuse there and say to use a Git repository or `agenthail project add`.

A coordinator is keyed by the project's logical id. For a project in a repository with a remote that is its `repo:` key, the same on every paired host, so a project has one coordinator across hosts. A project without a remote is keyed by the host's own project id, which paired hosts see as `host::<id>`.

## State

Each host keeps its coordinators in the `coordinators` table, keyed by project id and host. A row with an empty host is this host's own: the project root, the name, the agent (`claude` or `codex`), working directory, model, permission mode, standing instructions, who changed it last and when. The coordinator's session is whichever session the alias `@<name>` names, so the address follows rekeys and restarts. A row with a host is a read-only mirror of a paired host's coordinator, with its name, agent and current session.

The name defaults to `<project>-lead`. A name that any host's other coordinator uses, or that names another session on the owning host, is refused. Because the name is what binds the coordinator to its session, it moves only through the coordinator commands: `identify`, a new session's name and a removed alias cannot rename the coordinator's session, give its name to another session, or take the name of a coordinator that has not started.

Every set, replace and clear appends a `coordinator_history` row naming who made the change: the calling session as `host::session`, or the person or device acting (for example `the CLI`).

## Commands

- `coordinator set <target>` makes an existing session its project's coordinator and saves its agent, working directory and configured model. The target can be on any paired host (`server::@builder`): the CLI sends the routed action `coordinator-set` to the host that runs the session, which resolves the project there and records the coordinator. If that host is unreachable, the set is refused and nothing is written anywhere else.
- `coordinator new <claude|codex>` saves a coordinator with no session yet; its first message starts it. It uses the project of `--cwd` (default: the current folder) and starts in that folder or the project root. `--host <host> --cwd <folder>` defines it on a paired host, in a folder on that host.
- `coordinator` shows the current folder's project's coordinator, on any host. `coordinator list` shows every coordinator this host knows, with its host. A local coordinator whose folder now resolves to another project, for example after the repository's origin changed, is marked; `set --replace` moves it. Agenthail does not re-key it.
- `coordinator clear` removes the coordinator of the current folder's project, on whichever host owns it. Its session and name stay.

## Takeover

A project that already has a coordinator on any host refuses `set` and `new`, naming it as `host::@name`. `--replace` takes over:

- A coordinator on this host is replaced, and its earlier conversations stop routing to the project.
- A coordinator on a paired host is replaced in two steps. This host first records the new coordinator and its name, in one transaction with every local check, so no other session can take the name while the replace runs. It then asks the owner to clear its coordinator with the routed action `coordinator-clear`. The request carries the time that coordinator last changed, and the owner refuses it if the coordinator changed since. If the owner refuses, is unreachable or needs an update, this host withdraws the new coordinator in one transaction: it removes the new coordinator and its name and puts back the previous coordinator, the names the replace moved, and the conversations it had stopped routing, then reports that nothing changed. If any of those changed in the meantime, for example another session took the old name, it withdraws nothing and says the new coordinator stays alongside the owner's. A project therefore never loses its coordinator to a replace: the worst case is the two-coordinator conflict below.
- A daemon changes one coordinator at a time. A paired host's clear that arrives while it is changing one is refused, so two hosts replacing each other's coordinator fail rather than wait on each other.

Errors name a coordinator from the caller's side: `@name` for one on the caller's own host, `host::@name` for any other.

Two hosts can still each hold a coordinator for one project, after a race or after pairing two hosts that each had one. Then `@lead` refuses and names both, `coordinator` and `coordinator list` show both, and `set --replace` on either host resolves it.

## Federation

A host serves its own coordinators in `coordinators[]` of `/api/v1/snapshot` and appends `coordinator.changed` (on a set, replace or new conversation) and `coordinator.removed` (on a clear) to its catalog stream. A paired host replaces its mirror of that host's rows from each snapshot and applies the events. A host never re-exports mirrored rows, so hosts paired both ways do not loop. `hosts remove` drops the removed host's coordinators with its sessions. These are part of federation version 3; a host on an older version shows as needing an update and is sent none of these actions.

## Where it shows

Each project in the daemon snapshot lists `coordinators`: for each host that has one, its host, name, agent, who changed it last and when, and, once started, `sessionId`. A project normally has one; more than one is the conflict above. A project with this host's coordinator and no sessions yet is still a project: it appears in the snapshot and in the new-session picker (`/api/v1/session-options`), and can be archived. A paired host's coordinator is listed on a project this host shows. A coordinator whose folder this host now shows under another project, after its repository's origin changed, is listed on no project, and a project action by its old id is refused with a message that the project may be listed under another id now; the dashboard, Mac and iPhone then refresh their project list; `coordinator list` marks it and `set --replace` moves it.

The dashboard shows the coordinator in the project header as `@name`, or `host::@name` for a paired host's, and opens its conversation from it; until it starts it says it starts on its first message. A conflict shows the number of coordinators and names them. `project list` prints the coordinator in its LEAD column, `conflict (N)` when hosts disagree.

## Addresses

- `@<name>` reaches this host's coordinator by name. An `@<name>` that no session or coordinator here holds reaches the paired host's coordinator of that name; a name several hosts' coordinators share is refused with each `host::@name`.
- `@lead` reaches the coordinator of the sender's project: the project of the calling session's folder when the registry knows that session, otherwise the current folder.
- A paired host's coordinator is reached through its owner with the routed action `coordinator-send`, which delivers or starts it there. `--stream` and `--reply` need its conversation, so they follow its current session once it has one.
- `lead` is reserved and cannot name a session.
- A coordinator cannot take a name held by a conversation still starting, and a conversation cannot start under a coordinator's name. See [launchers](launchers.md#names).

A worker reports with `agenthail send @lead "..."`. Its host needs to have paired the coordinator's host to see it. A finished report ends with the worker's completion report, the `agenthail-report` block the [operations skill](../../skills/agenthail-operations/SKILL.md#completion-report) defines, and a coordinator dispatching work asks for one. Agenthail does not parse it.

## Delivery

`internal/coordinator` owns every delivery to a coordinator on its own host. The CLI `send`, the `send` and `coordinator-send` actions of `/api/v1/actions` (dashboard, Mac and iPhone composers, notification replies, paired hosts), channel sends from the CLI and the dashboard, voice delegation and the delivery queue all use it. A channel message sent from a conversation a restart replaced counts as sent by the coordinator, so the coordinator does not receive it back.

| State of the coordinator | What a send does |
| --- | --- |
| Running and idle | Delivers. |
| Running and busy | Steers the current turn when the session can be steered, otherwise queues. A sender that chooses a busy policy keeps it. |
| Ended, conversation on disk | Delivers through the surface's own resume. For Claude the Claude host resumes it. |
| Resume failed for now, or Agenthail cannot tell whether the conversation still exists | Queues the message. Agenthail never replaces a conversation because of a transient failure. |
| Conversation gone, or the name points at no session | Starts a new conversation from the spec and points the alias at it. Pending queue items, channel memberships and relay routes of the old session move to the new one. The first turn is the instructions, a line saying why it started, and the message, with any attached files named by their stored paths. The receipt says it started. |

A Claude conversation is gone when it has no transcript and no process holds it. A process that holds it counts because a conversation that has just started has not written its transcript yet. Starting again holds a lock file per project next to the registry, named by a hash of the project id, and the alias moves with a compare-and-swap, so concurrent senders in any Agenthail process start at most one conversation for a project. A queued message to a coordinator whose conversation is gone becomes the first turn of the new conversation instead of resuming an empty one.

The registry remembers each conversation a restart replaced, and for which project. A send addressed to one of them, such as a reply to an old notification or a composer still showing the old conversation, and a queued message claimed for one, reach the project's current coordinator instead.

`send --no-queue` refuses to wait for a busy or unavailable coordinator, but still starts one whose conversation is gone. It is not available for a paired host's coordinator.

A Codex coordinator starts without a name or permission mode, which Codex threads do not take; `--permission-mode` is refused for it. Agenthail cannot yet tell a gone Codex thread from an unreachable one, so a Codex coordinator starts again only when its name points at no session.

Context growth is left to the agent's own compaction.

## Limits

A message that starts a new conversation loses its sender label, because the conversation is created with that message as its prompt.
