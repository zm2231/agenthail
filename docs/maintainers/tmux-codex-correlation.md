# Terminal Codex launch correlation

## Contract

An Agenthail-owned tmux or cmux Codex launch must become a catalog session
with the provider-issued thread ID and a verified terminal location. A hand-started Codex
session must continue to require the existing live-PID and process-ancestry
proof. No path may choose a session from cwd, title, recency, or a guessed
local PID.

## Identity flow

1. The launcher allocates a random launch token and a 0600 receipt path for
   the existing `agenthail codex` wrapper. tmux supplies `TMUX_PANE`; cmux
   supplies its protected `CMUX_WORKSPACE_ID` and `CMUX_SURFACE_ID` variables
   through the Agenthail-owned launcher command.
2. The wrapper starts the managed Codex app-server, asks it for a new thread,
   and receives the provider-issued thread ID. It writes that ID and the
   launch-owned token to the receipt, then starts the interactive CLI with
   `codex resume <provider-thread-id> --remote unix://`.
3. The wrapper records the actual terminal identity in the receipt. tmux uses
   the wrapper's `TMUX_PANE`; cmux uses the protected workspace and surface
   identifiers. The receipt is never a provider/session ID by itself; it is a
   binding between the generated launch and the provider ID returned by
   `thread/start`.
4. tmux discovery verifies the live pane and current session inventory. cmux
   discovery verifies the receipt surface through the supported
   `--json --id-format both tree --workspace <id>` inventory command. Other sessions still use
   PID ancestry.

Receipt absence, malformed or oversized data, token mismatch, provider ID
mismatch, missing terminal identity, or a stale surface leaves the launch
pending. The receipt is a bounded 0600 record retained while the launch-owned
terminal association is unresolved; cleanup is safe only after a fresh tmux
inventory check proves the generated session is gone. The implementation does
not infer an identity from cwd or silently replace an unresolved launch.

## Non-goals

- No changes to Codex Desktop ownership or read-only policy.
- No live provider or terminal launch in tests.
- No mapping of arbitrary hand-started terminal sessions.
- No hook/config readiness claim for a generic CMUX launcher command.
- No new public runtime JSON fields or registry schema migration.
