---
name: agenthail-operations
description: "Operate AgentHail across Claude, Codex, and Notion: discover sessions, create Notion threads, send and observe work, manage queues, aliases, channels, relay subscriptions, daemon notifications, and the dashboard."
---

# AgentHail Operations

AgentHail connects to agents that already exist on the local Mac. Its CLI owns
target resolution, surface capability checks, durable delivery, and ambiguity
errors. Do not build a second target picker or choose between ambiguous matches.

## Install And Verify

```bash
brew install zm2231/tap/agenthail
brew services start agenthail
agenthail version --json
agenthail doctor --json
agenthail list --json
```

For a source checkout:

```bash
./install.sh
agenthail daemon install
agenthail doctor --json
```

AgentHail targets macOS. Claude and Notion use a signed-in Chrome profile. Codex
uses AgentHail's local app-server bridge. Relevant overrides are
`AGENTHAIL_CHROME_PROFILE`, `AGENTHAIL_PYTHON`, `AGENTHAIL_CODEX_BIN`,
`AGENTHAIL_CODEX_REMOTE`, `AGENTHAIL_NOTION_SPACE`, and
`AGENTHAIL_NOTION_USER`. Never print browser cookies, dashboard tokens, or
credentials.

AgentHail reads these overrides from the environment of each CLI or daemon
process and stores shared session, alias, queue, relay, and delivery state in
`~/.agenthail/registry.db`. A launchd service does not inherit variables that
exist only in an interactive shell. Put required `AGENTHAIL_*` overrides in the
service environment as well as the shell that runs the operator.

One unhealthy surface does not block the others. Preserve the `errors` object
from JSON discovery output when reporting partial availability.

## Surface Contract

| Operation | Claude | Codex | Notion |
|---|---:|---:|---:|
| Find existing sessions | yes | yes | yes |
| Send and read replies | yes | yes | yes |
| Start a new session or thread | manual | TTY or dashboard | CLI |
| Stream, interrupt, steer, compact | yes | yes | no |
| Persistent session model | yes | yes | no |
| One-message model override | no | yes | yes |
| Goal tracking | no | yes | no |

Claude model changes use the session's `/model` flow and require confirmation.
Notion supports one-message model overrides. Do not attempt streaming, steering,
interruption, compaction, persistent model changes, or goals on Notion.

Codex sessions have separate read and write boundaries:

| Codex launch path | Read | Send |
|---|:---:|:---:|
| `agenthail codex` | yes | yes |
| Desktop launched with `agenthail launch codex` | yes | yes |
| Desktop opened normally | yes | no |
| Plain `codex` terminal | yes | no |

Let AgentHail report read-only sessions. Do not claim a send succeeded when the
target has no writable transport.

## Find And Resolve Targets

For a user-facing current view, show open Claude sessions, busy or loaded Codex
sessions active within the configured recent window (five hours by default),
and busy or recently active Notion threads. Use `--all` only when the user asks
for history or a target is older or missing from the current view.

```bash
agenthail list --json
agenthail list --all --json
```

`doctor` is a health probe. Its per-surface session counts are full discovery
inventory counts, not current, active, or recent totals. Never use those counts
in a session summary. Session lists and counts must come from `list` output
after applying the current-view policy above. A surface discovery error means
that surface is unavailable for this call; report it and continue with sessions
from healthy surfaces. Do not describe an unavailable Notion surface as active
or configured.

Targets accept `@alias`, PID, session-ID prefix, cwd/name fragment, or a
qualified `surface:target` value. If AgentHail reports ambiguity, show the
candidates and ask for one qualification. Never select heuristically.

```bash
agenthail identify claude:<session-id> researcher
agenthail identify codex:<session-id> builder
agenthail identify notion:<thread-uuid> notes
agenthail identify list
agenthail identify rm notes
```

## Start New Sessions And Threads

Notion is the only surface with non-interactive CLI thread creation:

```bash
agenthail send notion:new "Start a research thread" --reply --json
agenthail send notion:new:<name> "Draft the launch notes" --reply --json
```

Creation must happen immediately and cannot be queued. The receipt contains the
real persisted thread UUID. `notion:new:<name>` also stores `<name>` as a durable
AgentHail alias. Keep the UUID or alias for replies and future sends. Notion
chooses the visible title from the first message.

Start a writable Codex thread with `agenthail codex` in a human TTY. It uses the
caller's current directory unless `--cd` is provided. The enabled AgentHail
dashboard can also create one. `agenthail launch codex` starts Desktop with a
writable bridge but does not itself create a thread. AgentHail does not expose a
non-interactive CLI command for creating a Claude session. Open Claude manually,
then discover it with `agenthail list`.

## Send, Read, And Control

```bash
agenthail send @builder "Implement the confirmed fix." --reply --json --timeout 5m
agenthail send @builder "Implement the confirmed fix." --json
agenthail send @builder "Walk through the issue." --stream --timeout 2m
agenthail send @builder - --reply --json < prompt.txt

agenthail reply @builder --json
agenthail last @builder 5 --full --json
agenthail stream @builder --timeout 10s
agenthail steer @builder "Prioritize the failing test."
agenthail interrupt @builder
agenthail compact @builder
agenthail model @builder
agenthail model @builder <model-name>
agenthail goal @builder --json
agenthail goal @builder "Ship the verified fix."
agenthail goal @builder clear
```

`send` queues a busy target by default. `--no-queue` requires immediate
delivery. `--reply` waits for one new completed reply only when delivery is
immediate. A queued `--reply` returns the durable queue receipt. `--stream`
watches live deltas and cannot be combined with `--reply` or `--json`.

Use `--model` only for a one-message Codex or Notion override. Use `--from` when
the receiving agent needs a human-readable sender label. Long content should go
through stdin.

## Follow, Subscribe, And Notify

AgentHail has four distinct completion behaviors:

1. `send --reply` waits for one completed turn.
2. `stream` or `send --stream` watches one live Claude or Codex turn.
3. A relay is a persistent agent-to-agent subscription to completed turns.
4. Daemon notifications alert the human through the AgentHail macOS app.

Persistent agent-to-agent subscription:

```bash
agenthail relay add @researcher @builder
agenthail relay add @researcher @builder 'FAIL|NO-SHIP|root cause'
agenthail relay list
agenthail relay rm <id>
```

The first target is the completion source. The second receives each matching
completed reply. The optional filter is a regular expression. Relays reject
self-routes and cycles, remember delivered completion IDs across restarts, and
require the daemon.

Human completion notifications:

```bash
agenthail daemon notify on
agenthail daemon notify status
agenthail daemon notify test
agenthail daemon notify settings
agenthail daemon notify off
```

Notifications require a supporting AgentHail build, the macOS companion app,
and System Settings authorization. They are not agent-to-agent relays.

There is no generic CLI callback that subscribes an arbitrary Photon, Slack, or
webhook conversation to future queued completions. Do not promise automatic
delivery back to the current messaging conversation unless the host application
implements that callback itself.

When a user says "subscribe," determine whether they mean a one-turn reply, a
live stream, a persistent relay to another agent, or a human macOS notification.

## Durable Queue And History

```bash
agenthail queue @builder "Then add focused tests."
agenthail queue list --json
agenthail queue list --all --json
agenthail queue retry <id>
agenthail queue rm <id>
agenthail queue clear @builder
agenthail history --json
agenthail history @builder 25 --json
```

The daemon delivers queued work when a target becomes idle. Queue delivery is
ordered per session. Known pre-dispatch failures retry with bounded backoff.
Repeated failures become dead letters. An `unknown` outcome means delivery may
already have happened; inspect history and the target before retrying.

## Channels

```bash
agenthail channel create launch
agenthail channel add launch @researcher
agenthail channel add launch @builder
agenthail channel list
agenthail channel send launch "Keep the existing API compatible." --from operator
agenthail channel rm launch @researcher
agenthail channel rm launch --all
```

Channels broadcast to every member. Busy members queue. Partial failure exits
nonzero so the caller can report which deliveries failed.

## Daemon

```bash
agenthail daemon status
agenthail daemon start
agenthail daemon stop
agenthail daemon restart
agenthail daemon install
agenthail daemon uninstall
```

Homebrew manages its service with `brew services start|stop|restart agenthail`.
For a source install, `daemon install` creates a supervised launchd service.
Remove that service with `daemon uninstall`, not `daemon stop`. Check daemon
status before relying on queues, relays, or background observation.

## Dashboard

```bash
agenthail dashboard enable
agenthail dashboard enable --no-open
agenthail dashboard disable
agenthail dashboard status
agenthail dashboard
agenthail dashboard config --codex-recent-hours 12
agenthail dashboard remote
agenthail dashboard remote status
agenthail dashboard remote off
```

The local dashboard binds to loopback behind a per-install token. Remote access
uses a private Tailscale Serve route. Its authenticated URL and QR code contain
the dashboard token. Share them only when the user explicitly requests access.

## Launching Surfaces

```bash
agenthail launch codex
agenthail codex
```

`launch codex` opens Desktop with AgentHail's writable renderer bridge.
`agenthail codex` starts an interactive writable terminal session and requires a
human TTY. Claude and Notion must be opened and signed in manually.

## Verification Boundary

Non-mutating checks:

```bash
agenthail version --json
agenthail doctor --json
agenthail list --json
agenthail daemon status
agenthail queue list --json
agenthail history 10 --json
agenthail identify list
agenthail channel list
agenthail relay list
agenthail dashboard status
```

Status commands may exit nonzero for stopped, disabled, unavailable, or partly
unhealthy states. Preserve and interpret their output instead of hiding it.
Sending, queueing, steering, interruption, compaction, model or goal changes,
aliases, channels, relays, notification changes, dashboard changes, and daemon
lifecycle operations mutate local state or active sessions. Run them only when
the user's request calls for them, then verify the resulting status or receipt.
