# Maintainer documentation

These notes cover Agenthail's implementation, packaging, release automation, and service integrations. Normal installation and use do not require them.

## Running the whole Go suite

`go test ./...` runs the real Claude host, browser host and cookie bridge, so a checkout needs the shipped Node and each bundle's packages first, as CI stages them:

```bash
scripts/fetch-node.sh
npm ci --prefix claude-host --omit=optional --ignore-scripts --no-audit --no-fund
npm ci --prefix browser-host --omit=optional --ignore-scripts --no-audit --no-fund
npm ci --prefix sidecar --ignore-scripts
go test ./...
```

A test that runs a host whose packages are missing fails at once and names the `npm ci` command, rather than timing out waiting for the host. The browser tests fetch Chromium on first use; set `AGENTHAIL_BROWSER_TEST_BROWSERS` to a directory to fetch it once and share it across tests and runs.

- [Packaging and releases](../packaging.md)
- [Self-hosted CI runners](self-hosted-ci.md)
- [iPhone live harness: real daemons for connection tests](ios-live-harness.md)
- [Linux daemon: systemd service, updates and doctor](linux-daemon.md)
- [Daemon runtime profiles: capturing heap, allocs and goroutines](debug-profiles.md)
- [Claude session host](claude-host.md)
- [Browser host: Chromium on first use, supervision and status](browser-host.md)
- [Simulator host: shared iOS simulators, leases and actions](simulator-host.md)
- [Extensions: pages, actions, jobs and the extension host](extensions.md)
- [Claude goals: transcript records, statuses and clients](claude-goals.md)
- [Codex integration decisions](../codex-protocol.md)
- [Codex Voice orchestrator: setup, flow, limits, and verification](codex-voice.md)
- [Code mode: tool calls written as scripts, detection and presentation](code-mode.md)
- [Push notification service](push-relay.md)
- [Completion triggers: a command or file touch when a turn finishes](completion-triggers.md)
- [Mobile session fidelity audit](mobile-session-ux.md)
- [Desktop parity: web dashboard vs Mac app](desktop-parity.md)
- [Subagents: identity, families, targets, and cost](subagents.md)
- [Projects, scopes and archive](projects.md)
- [Pinned sessions, and reaching a session whose process ended](pinned-sessions.md)
- [Paired hosts: pairing, ownership, routing, and unreachable delivery](hosts.md)
- [Moving a session to another host: protocol and failure model](host-move.md)
- [Registry migrations: adding a table, column, index or data step](registry-migrations.md)
- [Coordinator: one per project, its saved spec, @lead, and how a send always lands](coordinator.md)
- [Agent card: what a session knows about itself, and who started it](agent-card.md)
- [MCP server: session credentials, read tools, scope and limits](mcp.md)
- [Agent messages in the transcript: the `agent` field and the fold rule](agent-messages.md)
- [Inbox: what a session was sent, where it is recorded, and how it is read](inbox.md)
- [Dashboard design reference](../../design.md)
