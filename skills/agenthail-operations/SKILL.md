---
name: agenthail-operations
description: "Operate AgentHail safely: install and diagnose it, resolve existing Claude, Codex, and Notion sessions, send or stream work, manage aliases, channels, relays, queues, and the daemon."
---

# AgentHail Operations

Use this skill for the `agenthail` CLI. It connects to existing Claude, Codex, and Notion sessions; it does not create a Photon or Spectrum/iMessage integration.

## Start Safely

AgentHail targets macOS. It needs Go, Node.js, Python 3.10+, Chrome, and a signed-in desktop or web surface for each target. Install from a checkout with:

```bash
./install.sh
agenthail doctor --json
agenthail list --all --json
```

`doctor` is the first check after a desktop app or Chrome update. For Codex, start its loopback renderer bridge when needed:

```bash
agenthail launch codex
agenthail doctor
```

Use `AGENTHAIL_CHROME_PROFILE` for the signed-in Chrome profile. Use `AGENTHAIL_PYTHON` to pin a Python 3.10+ sidecar interpreter and `AGENTHAIL_CODEX_REMOTE` for a non-default Codex renderer-debugging port. Fresh AgentHail launches do not enable Node `--inspect`; an already-open app may temporarily use the delayed compatibility bridge until its next clean launch. Do not print or collect browser cookies, credentials, or other secrets.

## Resolve Before Sending

List first, then use an unambiguous target in automation:

```bash
agenthail list --all
agenthail identify claude:<session-id> researcher
agenthail identify codex:<session-id> builder
agenthail identify notion:<thread-uuid> notes
agenthail identify list
```

Targets accept `@alias`, PID, session-ID prefix, cwd/name fragment, or `surface:target`. If resolution is ambiguous, qualify it as `claude:...`, `codex:...`, or `notion:...`; never choose a candidate heuristically. Existing Notion thread UUIDs can be resolved even when they are outside the recent-list window. Use `notion:new` or `notion:new:<name>` to create a persistent thread; keep the returned UUID for later aliases and follow-ups.

## Send, Read, and Control

```bash
agenthail send @researcher "Investigate the failing test." --reply --timeout 5m
agenthail send @builder "Implement the confirmed fix." --stream
agenthail reply @builder
agenthail last @builder 5 --full
agenthail steer @builder "Prioritize the registry path."
agenthail queue @builder "Then add focused tests."
agenthail queue list --all
agenthail queue retry 12
```

`send` delivers to an idle target. If it is busy, it queues by default; use `--no-queue` when immediate delivery is required. Use `queue` for the next instruction after the active turn finishes. Use `steer` only to influence the active turn now. Queued work needs a running daemon.

`--reply` waits for a new completed reply; `--stream` watches a supported active turn. Long input may be read from stdin: `agenthail send @builder - < prompt.txt`.

| Operation | Claude | Codex | Notion |
|---|---:|---:|---:|
| Find existing sessions; send; read reply | yes | yes | yes |
| Stream; interrupt; steer; compact | yes | yes | no |
| Session model | yes | yes | no |
| Per-message `send --model` | no | yes | yes |
| Goal tracking | no | yes | no |

Do not attempt `stream`, `steer`, `interrupt`, `compact`, persistent session-model changes, or goals on Notion. Notion supports `send --model` for a single message. Claude model changes use the session's `/model` command and require confirmation. Check support with `agenthail doctor` and handle an unsupported-operation error rather than assuming parity.

## Coordinate Sessions

```bash
agenthail channel create launch
agenthail channel add launch @researcher
agenthail channel add launch @builder
agenthail channel send launch "Keep the existing API compatible." --from operator

agenthail relay add @researcher @builder 'FAIL|NO-SHIP|root cause'
agenthail relay list
agenthail relay rm 3
```

Channels broadcast to all members. Busy members are queued; partial channel failure exits nonzero. Relays deliver completed replies from one session to another. The optional relay filter is a regular expression, and cycles are rejected. Start the daemon before relying on queues or relays.

## Daemon Lifecycle

```bash
agenthail daemon status
agenthail daemon start
agenthail daemon stop
agenthail daemon install
agenthail daemon uninstall
```

`daemon start` is an on-demand process. `daemon install` creates and starts a supervised macOS launchd service. A launchd-managed daemon must be stopped with `daemon uninstall`, not `daemon stop`. Check `daemon status` and `queue list --all` before and after lifecycle changes; do not retry an `unknown` queue delivery automatically.

## Verification Boundary

For a non-mutating health check, run:

```bash
agenthail version --json
agenthail doctor --json
agenthail list --all --json
agenthail daemon status
agenthail queue list --all
```

`daemon status` exits nonzero when it is stopped; report that state rather than treating it as a failed installation. Sending, queueing, steering, interruption, compaction, aliases, channels, relays, and daemon install/uninstall all alter state or sessions. Perform them only when the requested operation calls for it, then verify the resulting status and delivery output.
