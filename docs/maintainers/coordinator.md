# Coordinator

The coordinator is the one agent a person sends work to. It is a role on an ordinary session, not a new kind of session.

## State

The owning host keeps one row in the `coordinator` table of its registry: the name, the agent (`claude` or `codex`), working directory, model, permission mode and standing instructions. The coordinator is whichever session the alias `@<name>` names. Aliases follow session rekeys, so the address survives them without extra bookkeeping. `coordinator set` points the alias at an existing session and saves its agent, working directory and configured model. `coordinator new` saves a spec with no session, so the first message starts one.

## Delivery

`internal/coordinator` owns every delivery to the coordinator. The CLI `send`, the `send` action of `/api/v1/actions` (dashboard, Mac and iPhone composers, notification replies) and the delivery queue all use it.

| State of the coordinator | What a send does |
| --- | --- |
| Running and idle | Delivers. |
| Running and busy | Steers the current turn when the session can be steered, otherwise queues. A sender that chooses a busy policy keeps it. |
| Ended, conversation on disk | Delivers through the surface's own resume. For Claude this is `claude --bg --resume`. |
| Resume failed for now, or Agenthail cannot tell whether the conversation still exists | Queues the message. Agenthail never replaces a conversation because of a transient failure. |
| Conversation gone, or the name points at no session | Starts a new conversation from the spec, points the alias at it, and moves pending queue items from the old session. The first turn is the instructions, a line saying why it started, and the message. The receipt says it started. |

A Claude conversation is gone when it has no transcript and no process holds it. A process that holds it counts because a conversation that has just started has not written its transcript yet. Starting again holds `coordinator.lock` next to the registry, and the alias moves with a compare-and-swap, so concurrent senders in any Agenthail process start at most one conversation. A queued message to a coordinator whose conversation is gone becomes the first turn of the new conversation instead of resuming an empty one.

The registry remembers each conversation a restart replaced. A send addressed to one of them, such as a reply to an old notification or a composer still showing the old conversation, and a queued message claimed for one, reach the current coordinator instead.

`send --no-queue` refuses to wait for a busy or unavailable coordinator, but still starts one whose conversation is gone.

A Codex coordinator starts without a name or permission mode, which Codex threads do not take; `--permission-mode` is refused for it. Agenthail cannot yet tell a gone Codex thread from an unreachable one, so a Codex coordinator starts again only when its name points at no session.

Context growth is left to the agent's own compaction.

## Limits

The coordinator is local to the host that owns it. Other hosts reach it as `host::@<name>`, like any other session. A message that starts a new conversation loses its sender label, because the conversation is created with that message as its prompt.
