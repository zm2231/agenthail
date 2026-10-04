# tmux Codex launch correlation

## Contract

An Agenthail-owned tmux Codex launch must become a catalog session with the
provider-issued thread ID and a verified tmux location. A hand-started Codex
session must continue to require the existing live-PID and process-ancestry
proof. No path may choose a session from cwd, title, recency, or a guessed
local PID.

## Identity flow

1. The tmux launcher allocates a random tmux session name and passes that name
   and a 0600 receipt path to the existing `agenthail codex` wrapper.
2. The wrapper starts the managed Codex app-server, asks it for a new thread,
   and receives the provider-issued thread ID. It writes that ID and the
   launch-owned token to the receipt, then starts the interactive CLI with
   `codex resume <provider-thread-id> --remote unix://`.
3. The wrapper records the actual `TMUX_PANE` value in the receipt. The
   launcher records the returned tmux session and pane after `tmux new-session`
   returns. The receipt is never a provider/session ID by itself; it is only a
   binding between the generated tmux launch and the provider ID returned by
   `thread/start`.
4. tmux discovery verifies the pane is live and the receipt token, provider ID,
   tmux session, pane, and requested workspace agree. It may then return the
   provider ID for that exact launch. Other sessions still use PID ancestry.

Receipt absence, malformed or oversized data, token mismatch, provider ID
mismatch, missing `TMUX_PANE`, or a stale pane leaves the launch pending. The
receipt is a bounded 0600 record retained while the launch-owned tmux session
is the durable runtime association; cleanup is safe only after that session is
gone. The implementation does not infer an identity from cwd or silently
replace an unresolved launch.

## Non-goals

- No changes to Codex Desktop ownership or read-only policy.
- No live provider or terminal launch in tests.
- No mapping of arbitrary hand-started terminal sessions.
- No new public runtime JSON fields or registry schema migration.
