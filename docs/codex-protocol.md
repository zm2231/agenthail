# Maintainer note: Codex integration decisions

This document records implementation boundaries for contributors. Normal use does not require it.

Agenthail implements Codex protocol methods only when they support a user-facing workflow and can be verified across the full path.

## Supported now

- Discover, read, resume, and observe threads
- Start a managed thread and its first turn from the CLI or dashboard
- Send, steer, interrupt, compact, and change models
- Stream active turn events into the dashboard

## Deferred

`thread/fork` is intentionally deferred. Forking is useful, but Agenthail does not yet have a CLI or dashboard workflow that chooses the source turn, names the branch, and makes the returned thread discoverable. The unused `Fork` field was removed from `surface.Capabilities` instead of advertising support that no surface provides. It can return as a dedicated interface when that workflow is implemented end to end.

Archive, delete, rollback, shell-command, moderation, and approval methods remain out of the public capability contract. They should only be added with explicit product behavior, safety rules, and tests.
