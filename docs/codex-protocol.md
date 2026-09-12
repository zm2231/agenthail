# Maintainer note: Codex integration decisions

This document records implementation boundaries for contributors. Normal use does not require it.

Agenthail implements Codex protocol methods only when they support a user-facing workflow and can be verified across the full path.

## Supported now

- Discover, read, resume, and observe threads
- Start Desktop-owned conversations through the dashboard and `thread create`
- Send, steer, interrupt, compact, and change models
- Stream active turn events into the dashboard
- Keep current work bounded through loaded-thread and state-database views
- Search older local Codex history only when an operator requests it

## Desktop-owned conversations

Codex Desktop keeps its app-server on private stdio and holds the active thread writer lock. Agenthail reaches that same child through the Desktop renderer’s loopback-only Chrome DevTools endpoint, so it does not compete for ownership with a second app-server. `agenthail launch codex` starts Desktop with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9231`. A Desktop already running without that launch path must be quit and relaunched before Agenthail can send to its active conversations. The endpoint grants code execution to local processes that can reach it; use it only on a trusted local account.

Agenthail uses the renderer’s `mcp-request` and `mcp-notification` bridge to the Desktop-owned app-server. Before delivering to an unloaded Desktop conversation, it reads compact thread state, resumes the thread through Desktop, confirms direct-input capability, then starts the turn. It uses a one-turn listing to detect active work; transcript hydration is never part of send readiness. It does not rely on Node’s `--inspect` interface, which current signed Codex Desktop builds can disable. `AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT` changes the loopback port when `9231` is unavailable.

## Ownership

Every writable Codex conversation has one owner:

- `desktop`: Codex Desktop owns the writer; Agenthail uses the renderer bridge.
- `managed`: an `agenthail codex` terminal owns the remote-control session for its lifetime.
- `readOnly`: the conversation is catalogued but no writable owner is available.

`agenthail codex` starts the managed terminal path. `agenthail launch codex` exposes the Desktop bridge. Dashboard and `thread create codex` conversations are Desktop-owned. A conversation's original creator does not decide its current owner. A managed runtime must not be used to load or send to a Desktop-owned conversation.

When Desktop reports that another runtime owns a conversation, Agenthail returns a terminal ownership conflict and does not keep retrying it. Once the operator has confirmed that no managed `agenthail codex` terminal is active, `agenthail codex --repair-managed-runtime` restarts the managed remote-control runtime so stale managed leases are released. It never restarts Codex Desktop.

`thread/search` is available through the app-server experimental API capability. Agenthail requests that capability when it initializes its managed app-server and calls the method only for an explicit history search. Desktop requests use the Desktop app-server’s existing capability set. If a newer Codex version promotes the method to the normal protocol, the method name and request shape remain the same. If an older version does not support it, Agenthail retains its local conversation catalog and reports that full Codex history search is unavailable rather than scanning rollout files.

Archive, delete, rollback, shell-command, moderation, and approval methods remain out of the public capability contract. They should only be added with explicit product behavior, safety rules, and tests.
