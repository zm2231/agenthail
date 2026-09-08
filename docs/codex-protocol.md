# Maintainer note: Codex integration decisions

This document records implementation boundaries for contributors. Normal use does not require it.

Agenthail implements Codex protocol methods only when they support a user-facing workflow and can be verified across the full path.

## Supported now

- Discover, read, resume, and observe threads
- Start a managed thread and its first turn from the CLI or dashboard
- Send, steer, interrupt, compact, and change models
- Stream active turn events into the dashboard
- Keep current work bounded through loaded-thread and state-database views
- Search older local Codex history only when an operator requests it

## Desktop-owned conversations

Codex Desktop keeps its app-server on private stdio and holds the active thread writer lock. Agenthail reaches that same child through the Desktop renderer’s loopback-only Chrome DevTools endpoint, so it does not compete for ownership with a second app-server. `agenthail launch codex` starts Desktop with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9231`. A Desktop already running without that launch path must be quit and relaunched before Agenthail can send to its active conversations. The endpoint grants code execution to local processes that can reach it; use it only on a trusted local account.

Agenthail uses the renderer’s `mcp-request` and `mcp-notification` bridge to the Desktop-owned app-server. It does not rely on Node’s `--inspect` interface, which current signed Codex Desktop builds can disable. `AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT` changes the loopback port when `9231` is unavailable.

`thread/search` is available through the app-server experimental API capability. Agenthail requests that capability when it initializes its managed app-server and calls the method only for an explicit history search. Desktop requests use the Desktop app-server’s existing capability set. If a newer Codex version promotes the method to the normal protocol, the method name and request shape remain the same. If an older version does not support it, Agenthail retains its local conversation catalog and reports that full Codex history search is unavailable rather than scanning rollout files.

## Deferred

`thread/fork` is intentionally deferred. Forking is useful, but Agenthail does not yet have a CLI or dashboard workflow that chooses the source turn, names the branch, and makes the returned thread discoverable. The unused `Fork` field was removed from `surface.Capabilities` instead of advertising support that no surface provides. It can return as a dedicated interface when that workflow is implemented end to end.

Archive, delete, rollback, shell-command, moderation, and approval methods remain out of the public capability contract. They should only be added with explicit product behavior, safety rules, and tests.
