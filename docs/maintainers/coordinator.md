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
- `coordinator` shows the current folder's project's coordinator, on any host. `coordinator list` shows every coordinator this host knows, with its host. When a repository's origin changes, its coordinator moves to the new key with the rest of the project. A local coordinator whose folder still resolves to another project, for example because the new key already had a coordinator of its own and the move was refused, is marked; `set --replace` moves it.
- `coordinator clear` removes the coordinator of the current folder's project, on whichever host owns it. Its session and name stay. A clear of a project with no coordinator changes nothing and answers `cleared: null`.
- Setting the coordinator a project already has, the same conversation and name (or, for `new`, the same name, agent, folder, model, permission mode and instructions), changes nothing and answers with it.
- Refusals carry a code (`coordinator_exists`, `coordinator_conflict`, `name_taken`, `no_coordinator`) and a message without CLI flags; the CLI adds the flag that resolves each one.

## Takeover

A project that already has a coordinator on any host refuses `set` and `new`, naming it as `host::@name`. `--replace` takes over:

- A coordinator on this host is replaced, and its earlier conversations stop routing to the project.
- A coordinator on a paired host is replaced in two steps. This host first records the new coordinator and its name, in one transaction with every local check, so no other session can take the name while the replace runs. It then asks the owner to clear its coordinator with the routed action `coordinator-clear`. The request carries the time that coordinator last changed, and the owner refuses it if the coordinator changed since. If the owner refuses, is unreachable or needs an update, this host withdraws the new coordinator in one transaction: it removes the new coordinator and its name and puts back the previous coordinator, the names the replace moved, and the conversations it had stopped routing, then reports that nothing changed. If any of those changed in the meantime, for example another session took the old name, it withdraws nothing and says the new coordinator stays alongside the owner's. A project therefore never loses its coordinator to a replace: the worst case is the two-coordinator conflict below.
- Once a replace has taken effect, including every paired host's clear, the relays of the previous lead move to the new conversation, the way a lead's relays follow it to a new conversation of its own. On this host, routes to and from the previous lead's conversation move, whether it ran here or on the paired host. Then this host asks each paired host whose lead it cleared, over the host-only `POST /api/v1/coordinator-relays` (`{projectId, from, to}`), to move its own routes for that lead to the new conversation. The paired host does so only for the lead it last cleared for the project, with no coordinator set there since, and only to a conversation it mirrors from the asking host. A replace that fails moves nothing anywhere. The replace already stands when that request goes out, so a paired host that cannot move them, for example one that does not mirror the new conversation yet, keeps them on its former lead and this host logs why. A move that turns a route into a session relaying to itself drops it, and one that duplicates another route merges them. A relay read just before its route moved is not delivered to the previous lead: the next pass delivers it to the new one. A replace by a coordinator with no conversation yet moves nothing.
- A daemon changes one coordinator at a time. A paired host's clear that arrives while it is changing one is refused, so two hosts replacing each other's coordinator fail rather than wait on each other.

Errors name a coordinator from the caller's side: `@name` for one on the caller's own host, `host::@name` for any other.

Two hosts can still each hold a coordinator for one project, after a race or after pairing two hosts that each had one. Then `@lead` refuses and names both, `coordinator` and `coordinator list` show both, and `set --replace` on either host resolves it.

## Federation

A host serves its own coordinators in `coordinators[]` of `/api/v1/snapshot` and appends `coordinator.changed` (on a set, replace or new conversation) and `coordinator.removed` (on a clear) to its catalog stream. A paired host replaces its mirror of that host's rows from each snapshot and applies the events. A host never re-exports mirrored rows, so hosts paired both ways do not loop. `hosts remove` drops the removed host's coordinators with its sessions. These are part of federation version 3; a host on an older version shows as needing an update and is sent none of these actions.

## Where it shows

Each project in the daemon snapshot lists `coordinators`: for each host that has one, its host, name, agent, who changed it last and when, and, once started, `sessionId`. A project normally has one; more than one is the conflict above. A project with this host's coordinator and no sessions yet is still a project: it appears in the snapshot and in the new-session picker (`/api/v1/session-options`), and can be archived. A paired host's coordinator is listed on a project this host shows. A coordinator whose folder this host now shows under another project, because a move to the repository's new key was refused, is listed on no project, and a project action by its old id is refused with a message that the project may be listed under another id now; the dashboard, Mac and iPhone then refresh their project list; `coordinator list` marks it and `set --replace` moves it.

The dashboard shows the coordinator in the project header as `@name`, or `host::@name` for a paired host's, and opens its conversation from it; until it starts it says it starts on its first message. A conflict shows the number of coordinators and names them. `project list` prints the coordinator in its LEAD column, `conflict (N)` when hosts disagree.

## Addresses

- `@<name>` reaches this host's coordinator by name. An `@<name>` that no session or coordinator here holds reaches the paired host's coordinator of that name; a name several hosts' coordinators share is refused with each `host::@name`.
- `@lead` reaches the coordinator of the sender's project: the project of the calling session's folder when the registry knows that session, otherwise the current folder.
- A paired host's coordinator is reached through its owner with the routed action `coordinator-send`, which delivers or starts it there. `--stream` and `--reply` need its conversation, so they follow its current session once it has one.
- `coordinator-send` takes either `name`, a coordinator on the host that runs the action, or `project`, a project id or name resolved by the same rules as `@<project>-lead`. A project's lead on a paired host is reached through that host, with the sender, model and author carried the way a routed action carries them. Its reply names the coordinator as `ref`. A request a paired host sent is not passed on to a lead on a third host; it is refused with `host_transit`. The MCP tool `agenthail_send` with `project` is this action.
- `lead` is reserved and cannot name a session.
- A coordinator cannot take a name held by a conversation still starting, and a conversation cannot start under a coordinator's name. See [launchers](launchers.md#names).

A worker reports with `agenthail send @lead "..."`. Its host needs to have paired the coordinator's host to see it. A finished report ends with the worker's completion report, the `agenthail-report` block the [operations skill](../../skills/agenthail-operations/SKILL.md#completion-report) defines, and a coordinator dispatching work asks for one. Agenthail does not parse it.

## Delivery

`internal/coordinator` owns every delivery to a coordinator on its own host, and only the daemon runs it, so one process decides whether a lead is gone and starts its replacement. The `send` and `coordinator-send` actions of `/api/v1/actions` (dashboard, Mac and iPhone composers, notification replies, paired hosts), channel sends (`channel-send`, and `channel-deliver` from a paired host), voice delegation and the delivery queue use it inside the daemon. The CLI posts channel sends to the daemon's `channel-send` action and hands every other message for a coordinator, whether named (`@name`, `@lead`) or addressed by one of its conversations, to the daemon's `coordinator-send` action with its model, turn options and `--no-queue`. Without a running daemon such a send fails and says so; messages to other sessions are unaffected. A receipt marks a conversation started for the message with `started`. When a `--reply` or `--stream` send, to this host or a paired one, makes a coordinator start a new conversation, the CLI says so, releases the stream of the conversation it addressed and follows the new one's first turn from the start of its journal. Only when that turn's outcome is unknown, or the message went to another existing conversation, does it stop following and name `agenthail last`. A channel message sent from a conversation a restart replaced counts as sent by the coordinator, so the coordinator does not receive it back.

| State of the coordinator | What a send does |
| --- | --- |
| Running and idle | Delivers. |
| Running and busy | Steers the current turn when the session can be steered, otherwise queues. A sender that chooses a busy policy keeps it. |
| Ended, conversation on disk | Delivers through the surface's own resume. For Claude the Claude host resumes it. |
| Resume failed for now, or Agenthail cannot tell whether the conversation still exists | Queues the message. Agenthail never replaces a conversation because of a transient failure. |
| Conversation gone, or the name points at no session | Starts a new conversation from the spec. The first turn is the instructions, a line saying why it started, and the message, with any attached files named by their stored paths. When that turn is accepted, or its outcome is unknown, the alias moves to the new conversation and the old session's pending queue items, channel memberships and relay routes move with it; the receipt says it started, or that it was submitted. When the first turn fails outright, nothing moves: the alias, queue items, memberships and routes stay on the previous session, and the send fails for good rather than starting another conversation on a retry. |

A Claude conversation is gone only on positive evidence: every Claude config root was read without error, none holds a transcript of it, no process or starting background job holds it, and the session was registered or last active more than two minutes ago. A conversation that has just started has not written its transcript or process record yet, which the two minutes cover. Anything short of that, such as a root that cannot be read, a transcript in two places, or a lead seen moments ago, is unknown: the message queues for the lead as it would for a busy one, and nothing starts. Starting again holds a lock file per project next to the registry, named by a hash of the project id, and the alias moves with a compare-and-swap, so concurrent senders start at most one conversation for a project: the second re-reads the lead under the lock and finds the replacement, which is inside its two minutes and so never gone. A queued message to a coordinator whose conversation is gone becomes the first turn of the new conversation instead of resuming an empty one.

### Handover

A restart's handover is a `coordinator_history` row with action `replaced`. It records:

- who caused it (`changed_by`): the sending session, `agenthail-person`, `agenthail-cli`, or `daemon` for a queued message with no sender;
- why (`reason`): `gone: ` with the evidence, including when the old conversation was last seen.

A person's `set --replace` writes a `replaced` row with no reason, and nothing below applies to it.

The daemon announces each handover once. A pass right after the handover, and every 30 seconds until it is done, does the following in order:

1. It notifies the person on the desktop and on paired devices (event `coordinator.replaced`), and records that it did, so a retry or a restarted daemon does not notify them again. A crash between the two resends it under the same identifier, which replaces the first.
2. It gives an agent whose queued message caused the restart a notice keyed `coordinator-replaced:<history id>:sender`. A sender that waited for its send learns from the receipt instead, and a sender that no longer exists is skipped.
3. It marks the row announced.
4. It makes one attempt to tell the old conversation that it is no longer the lead and should end its turn. This happens only if the conversation turns out to still exist, and the attempt is direct, because a queued notice would be moved to the new lead.

Handovers recorded before schema 48 count as announced and acknowledged.

The person sees each handover until they acknowledge it:

- The snapshot's `leadReplacements` lists the open handovers, each with its id, project, name, both conversations, who caused it, why and when.
- The action `lead-replacement-ack {replacementId}` acknowledges one.
- A local project lead in the snapshot carries its last handover as `replaced`. `coordinator` and `coordinator list` print it as "replaced <when> by <who>, because <why> (was <old conversation>)".

A replaced conversation's session row carries `replacedLead`, the spans in which it held a coordinator's name, and `agenthail list` shows them in DETAIL.

The old lead's open requests are not a second "needs you":

- `requests` returns each with `replacedBy`, the current lead.
- Its session row's attention kind is `replacedRequest`, which does not count toward `attentionFamilies`.
- The action `request-withdraw {sessionId, requestId}` settles such a request through `respond` with a deny and a note naming the new lead. That denies a permission prompt and dismisses a question. It is refused with `not_replaced` for any other session's request.
- An answer the person gives instead still goes to the old conversation.

The registry remembers each conversation a restart replaced, and for which project. A send addressed to one of them, such as a reply to an old notification or a composer still showing the old conversation, and a queued message claimed for one, reach the project's current coordinator instead.

`send --no-queue` refuses to wait for a busy or unavailable coordinator, but still starts one whose conversation is gone. It is not available for a paired host's coordinator.

A Codex coordinator starts without a name or permission mode, which Codex threads do not take; `--permission-mode` is refused for it. Agenthail cannot yet tell a gone Codex thread from an unreachable one, so a Codex coordinator starts again only when its name points at no session.

Context growth is left to the agent's own compaction.

## Stuck sessions

A session that stops where an agent could help is told to one agent: its project's coordinator, or, when the project has none, its coordinator has never started a conversation, or the stuck session is the coordinator, the session that started it (`session_origins`). With neither, only the person hears. The person is notified in every case.

Every notice is a keyed row in `message_queue`: an ordinary queue item for a local session, a held host send (`QueueHostSend`) for a paired host's coordinator. Keys stay unique after delivery and a held send is replayed under its key, so a notice is delivered once even when the daemon restarts mid-announcement. A notice never starts a coordinator conversation.

- **Prompts.** `announceWaiting` resolves the request and queues a notice naming it and `agenthail answer <session> <allow|deny|"answer"> --request <id>`. The key is derived from the session and question; `withdrawWaitingNow` cancels the notice while it is still pending.
- **Goals.** Every 30 seconds `sweepStoppedGoals` reads the goal of each goal-capable, non-busy session active in the last day whose activity advanced since its last successful read, skipping sessions a `goal_completed` relay already reports on. A failed goal read or notice leaves the session due, so the next pass retries it. A `blocked`, `usageLimited` or `budgetLimited` goal queues a notice keyed by session, goal, status and update time, so each stop is told once and a stop after resuming is told again. The person is notified before that notice is queued, because the queued key is what ends the retries: a stop is never marked handled before the person was told, and a daemon stopped in between tells the person again rather than never. Within one daemon an in-memory record keeps it to once per stop, which is also the only record when no agent is told.

## Limits

A message that starts a new conversation loses its sender label, because the conversation is created with that message as its prompt.
