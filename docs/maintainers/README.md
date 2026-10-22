# Maintainer documentation

These notes cover Agenthail's implementation, packaging, release automation, and service integrations. Normal installation and use do not require them.

- [Packaging and releases](../packaging.md)
- [Self-hosted CI runners](self-hosted-ci.md)
- [Linux daemon: systemd service, updates and doctor](linux-daemon.md)
- [Daemon runtime profiles: capturing heap, allocs and goroutines](debug-profiles.md)
- [Claude session host](claude-host.md)
- [Claude goals: transcript records, statuses and clients](claude-goals.md)
- [Codex integration decisions](../codex-protocol.md)
- [Codex Voice orchestrator: setup, flow, limits, and verification](codex-voice.md)
- [Push notification service](push-relay.md)
- [Mobile session fidelity audit](mobile-session-ux.md)
- [Desktop parity: web dashboard vs Mac app](desktop-parity.md)
- [Subagents: identity, families, targets, and cost](subagents.md)
- [Projects, scopes and archive](projects.md)
- [Paired hosts: pairing, ownership, routing, and unreachable delivery](hosts.md)
- [Moving a session to another host: protocol and failure model](host-move.md)
- [Registry migrations: adding a table, column, index or data step](registry-migrations.md)
- [Coordinator: one per project, its saved spec, @lead, and how a send always lands](coordinator.md)
- [Agent card: what a session knows about itself, and who started it](agent-card.md)
- [Dashboard design reference](../../design.md)
