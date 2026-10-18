# Maintainer note: Codex integration decisions

This document records implementation boundaries for contributors. Normal use does not require it.

Agenthail implements Codex protocol methods only when they support a user-facing workflow and can be verified across the full path.

## Supported now

- Discover, read, resume, and observe threads
- Start conversations from the dashboard, the Mac app and `thread create`, on Codex Desktop when its bridge is reachable and on Codex's app-server daemon otherwise
- Send, steer, interrupt, compact, and change models
- Stream active turn events into the dashboard
- Keep current work bounded through loaded-thread and state-database views
- Search older local Codex history only when an operator requests it

## Desktop-owned conversations

Codex Desktop keeps its app-server on private stdio and holds the active thread writer lock. Agenthail reaches that same child through the Desktop renderer’s loopback-only Chrome DevTools endpoint, so it does not compete for ownership with a second app-server. `agenthail launch codex` starts Desktop with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9231`. A Desktop already running without that launch path must be quit and relaunched before Agenthail can send to its active conversations. The endpoint grants code execution to local processes that can reach it; use it only on a trusted local account.

Agenthail uses the renderer’s `mcp-request` and `mcp-notification` bridge to the Desktop-owned app-server. Before delivering to an unloaded Desktop conversation, it reads compact thread state, resumes the thread through Desktop, confirms direct-input capability, then starts the turn. It uses a one-turn listing to detect active work; transcript hydration is never part of send readiness. It does not rely on Node’s `--inspect` interface, which current signed Codex Desktop builds can disable. `AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT` changes the loopback port when `9231` is unavailable.

Desktop renderer notifications carry `method` and `params` at the top level of
the `mcp-notification` message. Responses carry the RPC response in `message`.
The bridge accepts only `hostId: local`, retains a bounded sequence-numbered event
ring, and uses a versioned renderer namespace so a corrected hook can be installed
without restarting Desktop. The runtime regression executes the generated hook
against those actual message envelopes.

## Routes

Codex enforces one writer per thread: the app-server process that has a thread loaded holds a per-thread lock in `$CODEX_HOME/thread-writer-locks/`, and a second process that tries to load it gets `thread <id> already has an active writer`. Codex returns that as the generic invalid-request code, so Agenthail matches the message. A process unloads a thread, releasing the lock, after it has had no subscribers and no active turn for `thread_unload_delay_secs` (default 1800).

Agenthail reaches two app-servers and has one route to each:

- `desktop`: Codex Desktop's private app-server, through the renderer bridge.
- `managed`: Codex's app-server daemon, through `$CODEX_HOME/app-server-control/app-server-control.sock`. A terminal attached to the daemon (`agenthail codex`, `codex --remote`) writes through the daemon too.

One function decides a thread's route from what Codex reports:

1. The runtime that has the thread loaded (`thread/loaded/list`) owns it.
2. An unloaded thread goes by the creator Codex recorded in `originator` (the creating client's name, `Codex Desktop` for Desktop). When the thread object lacks it, Agenthail reads `originator` from the rollout's `session_meta`. `source` does not identify Desktop: daemon threads also report `vscode`.
3. A thread created by any client other than Desktop routes to the daemon. A Desktop thread, or one whose creator is unknown, routes to Desktop, and is `readOnly` while the bridge is unreachable. The daemon never loads it.

A thread the daemon has loaded locks Desktop out of it until the daemon unloads it, up to 1800 seconds after its last turn. That is why an unknown creator never routes to the daemon.

Writes resolve the route at the moment of the write. Before a turn, Agenthail reads `canAcceptDirectInput` on the route's runtime, resumes the thread there when it is not the live writer, and reads it again. A writer conflict is a terminal ownership conflict; any other resume failure leaves the message undelivered and retryable.

Creation uses Codex Desktop when its bridge is reachable and the daemon otherwise. Every created thread carries `threadSource: agenthail`; its route afterwards follows the rules above.

## Codex's app-server daemon

Agenthail starts the daemon with `codex app-server daemon start` only for a managed-route write or a creation without a Desktop bridge. Discovery, listing and reads only connect to a running daemon: when its socket is absent or refuses connections, nothing is loaded there. `start` is idempotent, serialized by Codex, and replaces a stale socket left by a crashed daemon. Agenthail never stops, restarts, updates or bootstraps the daemon on its own; Codex's own updater manages versions, and a restart interrupts running turns. `agenthail codex --repair-managed-runtime` restarts it on request. None of this touches Desktop's app-server, which is a private stdio child of Desktop without a control socket or pid file.

Agenthail runs lifecycle commands with the binary Codex's daemon installed for itself (`$CODEX_HOME/packages/app-server-daemon/current/bin/codex`, or the older `$CODEX_HOME/packages/standalone/current/codex`), otherwise the first `codex` on `PATH`, never the copy inside a Desktop app bundle. Codex decides whether that CLI can start its daemon: 0.163 installs its own package on first start, while 0.151 requires the standalone runtime and says so with its install command, which Agenthail reports unchanged and never runs.

Runtime status carries a typed `problem` that selects the repair: `bridge-unavailable` (launch Desktop through Agenthail), `runtime-stopped` (the daemon is not running; the dashboard can start it), `unsupervised`, and `codex-missing` (no Codex CLI and no Desktop).

Turns run inside the app-server, so they survive Agenthail restarts. The next send to an idle thread resumes it first. If the daemon dies mid-turn, Codex reports that turn as `interrupted` after the daemon restarts, and the next send resumes the thread and starts a new turn (verified against Codex 0.163). A turn or steer whose request was written but whose response was lost stays unknown and is never retried automatically.

## Protocol scope

`thread/search` is available through the app-server experimental API capability. Agenthail requests that capability when it initializes its managed app-server and calls the method only for an explicit history search. Desktop requests use the Desktop app-server’s existing capability set. If a newer Codex version promotes the method to the normal protocol, the method name and request shape remain the same. If an older version does not support it, Agenthail retains its local conversation catalog and reports that full Codex history search is unavailable rather than scanning rollout files.

Archive, delete, rollback, shell-command, moderation, and approval methods remain out of the public capability contract. They should only be added with explicit product behavior, safety rules, and tests.
