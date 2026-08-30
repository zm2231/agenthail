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

`thread/search` is available through the app-server experimental API capability. Agenthail requests that capability on initialization and calls the method only for an explicit history search. If a newer Codex version promotes the method to the normal protocol, the method name and request shape remain the same. If an older version does not support it, Agenthail retains its local conversation catalog and reports that full Codex history search is unavailable rather than scanning rollout files.

## Deferred

`thread/fork` is intentionally deferred. Forking is useful, but Agenthail does not yet have a CLI or dashboard workflow that chooses the source turn, names the branch, and makes the returned thread discoverable. The unused `Fork` field was removed from `surface.Capabilities` instead of advertising support that no surface provides. It can return as a dedicated interface when that workflow is implemented end to end.

Archive, delete, rollback, shell-command, moderation, and approval methods remain out of the public capability contract. They should only be added with explicit product behavior, safety rules, and tests.
