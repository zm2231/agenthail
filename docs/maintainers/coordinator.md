# Coordinator

Each project can have one coordinator, the agent its sessions report to and a person sends the project's work to. It is a role on an ordinary session, not a new kind of session.

## Projects

A coordinator belongs to a project: the Git repository that contains its folder or, outside any repository, the nearest registered folder project (see [projects](projects.md)). `internal/project` resolves a folder's project, and the daemon's catalog and the CLI both use it, so they place a folder in the same project. A folder outside every project has no coordinator: `coordinator new` and `@lead` refuse there and say to use a Git repository or `agenthail project add`.

## State

The owning host keeps one row per project in the `coordinators` table, keyed by project id: the project root, the name, the agent (`claude` or `codex`), working directory, model, permission mode and standing instructions. A Git project has no `projects` row, so the key is the id the catalog derives, not a foreign key. The name defaults to `<project>-lead` and is unique across projects. The coordinator is whichever session the alias `@<name>` names. Aliases follow session rekeys, so the address survives them without extra bookkeeping.

`coordinator set <target>` makes an existing session its project's coordinator and saves its agent, working directory and configured model. `coordinator new <claude|codex>` saves a spec with no session for the project of `--cwd` (default: the current folder), starting in that folder or the project root, so the first message starts it. `coordinator list` shows every project's coordinator.

## Addresses

`@<name>` reaches a coordinator from anywhere. `@lead` reaches the coordinator of the sender's project: the project of the calling session's folder when the registry knows that session, otherwise the current folder. A worker reports with `agenthail send @lead "..."`. `lead` is reserved and cannot name a session.

## Delivery

`internal/coordinator` owns every delivery to a coordinator. The CLI `send`, the `send` action of `/api/v1/actions` (dashboard, Mac and iPhone composers, notification replies), channel sends from the CLI and the dashboard, voice delegation and the delivery queue all use it. A channel message sent from a conversation a restart replaced counts as sent by the coordinator, so the coordinator does not receive it back.

| State of the coordinator | What a send does |
| --- | --- |
| Running and idle | Delivers. |
| Running and busy | Steers the current turn when the session can be steered, otherwise queues. A sender that chooses a busy policy keeps it. |
| Ended, conversation on disk | Delivers through the surface's own resume. For Claude the Claude host resumes it. |
| Resume failed for now, or Agenthail cannot tell whether the conversation still exists | Queues the message. Agenthail never replaces a conversation because of a transient failure. |
| Conversation gone, or the name points at no session | Starts a new conversation from the spec and points the alias at it. Pending queue items, channel memberships and relay routes of the old session move to the new one. The first turn is the instructions, a line saying why it started, and the message, with any attached files named by their stored paths. The receipt says it started. |

A Claude conversation is gone when it has no transcript and no process holds it. A process that holds it counts because a conversation that has just started has not written its transcript yet. Starting again holds a lock file per project next to the registry, and the alias moves with a compare-and-swap, so concurrent senders in any Agenthail process start at most one conversation for a project. A queued message to a coordinator whose conversation is gone becomes the first turn of the new conversation instead of resuming an empty one.

The registry remembers each conversation a restart replaced, and for which project. A send addressed to one of them, such as a reply to an old notification or a composer still showing the old conversation, and a queued message claimed for one, reach the project's current coordinator instead.

`send --no-queue` refuses to wait for a busy or unavailable coordinator, but still starts one whose conversation is gone.

A Codex coordinator starts without a name or permission mode, which Codex threads do not take; `--permission-mode` is refused for it. Agenthail cannot yet tell a gone Codex thread from an unreachable one, so a Codex coordinator starts again only when its name points at no session.

Context growth is left to the agent's own compaction.

## Limits

A coordinator is local to the host that owns it. Other hosts reach it as `host::@<name>`, like any other session; `@lead` resolves on the sender's own host. A message that starts a new conversation loses its sender label, because the conversation is created with that message as its prompt.
