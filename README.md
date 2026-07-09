# agenthail

Multi-surface agent communication. Send messages to AI agent sessions (Claude Code, Codex Desktop), name them, group them into channels, and auto-relay work between them.

Go binary + a tiny Python sidecar for Claude's Cloudflare-protected HTTP API.

## Install

```bash
git clone <repo> && cd agenthail
./install.sh
agenthail doctor
```

Requires Go, Node, and Python 3 with `curl_cffi` (`pip install curl_cffi`).

## Architecture

```
agenthail (Go binary)
  ├── Claude surface  → exec claude-worker (Python sidecar, curl_cffi)
  ├── Codex surface   → CDP into Codex main process (pure Go)
  ├── SQLite registry (sessions, aliases, channels, routes)
  └── daemon (turn-completion watcher, auto-relay)
```

Claude send goes through a Python sidecar because Claude's edge runs an
anti-bot fingerprint lottery that blocks every pure-Go TLS library
(utls, CycleTLS, bogdanfinn/tls-client). `curl_cffi` uses the real BoringSSL
C library (curl-impersonate) for byte-exact Chrome impersonation and passes
reliably. See `.research/claude-send-tls.md` for the full investigation.

Cookies are loaded by the sidecar via a Node bridge (`@steipete/sweet-cookie`),
which reads fresh Chrome cookies including the session-bound `cf_clearance`.

## Surfaces

| Surface | Transport | Requirements |
|---------|-----------|--------------|
| Claude Code | Events API (cookie auth via sweetcookie) | Chrome logged into claude.ai |
| Codex Desktop | CDP into main process -> app-server stdin JSON-RPC | Codex launched with `--inspect=127.0.0.1:9230` |

### Codex launch

```bash
# Via agenthail
agenthail launch codex

# Or manually
open -a Codex --args --inspect=127.0.0.1:9230 --remote-debugging-port=9231
```

The inspect flag exposes Codex's main V8 inspector, which lets agenthail reach the app-server child process and drive Desktop-visible sessions directly.

## Commands

### Sessions

```bash
agenthail list                          # show active sessions
agenthail send <target> "message"       # send a message
agenthail send <target> "msg" --stream  # send and stream the response
agenthail reply <target>                # fetch last assistant reply
agenthail stream <target>               # tail live activity
agenthail goal <target> "ship the thing"
agenthail compact <target>              # compress context
agenthail model <target> [name]         # get/set model
agenthail interrupt <target>            # stop current turn (Claude control_request or Codex turn/interrupt)
agenthail steer <target> "msg"          # inject into the running turn (errors if idle)
agenthail queue <target> "msg"          # hold until current turn completes, then deliver
```

### Identity

```bash
agenthail identify <target> writer      # name a session
agenthail identify list                 # show all names
agenthail send @writer "status?"        # @name resolves anywhere
```

### Channels

```bash
agenthail channel create team
agenthail channel add team @writer
agenthail channel add team @reviewer
agenthail channel list
agenthail channel send team "standup time"   # broadcast to all members
```

### Routing (auto-relay)

```bash
agenthail relay add @worker @reviewer        # relay worker's output to reviewer
agenthail relay add @worker @reviewer "FAIL.*"  # only on pattern match
agenthail relay list
agenthail relay rm 1
```

### Daemon

The daemon runs in the background, watches sessions, and on turn-completion: (1) fires matching relay rules, (2) drains the message queue.

```bash
agenthail daemon start      # spawn background daemon
agenthail daemon status     # is it running?
agenthail daemon stop       # stop it
```

Logs at `~/.agenthail/daemon.log`.

## Targets

Targets resolve in this order:
1. `@name` (registry alias)
2. exact session ID or ID prefix
3. PID
4. fuzzy match against cwd or name

## Capabilities by surface

| | Claude | Codex |
|---|---|---|
| send | yes | yes (auto-queues if busy) |
| stream | yes | yes |
| reply | yes | yes |
| goal | yes | yes |
| compact | yes | yes |
| model | yes | no |
| interrupt | yes | yes |
| steer | yes (send now) | yes (turn/steer; idle errors) |
| queue | yes (daemon) | yes (daemon) |
| fork | no | yes |

**steer** injects into the running turn (Codex `turn/steer`; Claude sends now).
Returns an error if the session is idle. **queue** holds the message until the
current turn completes, then delivers it (daemon-mediated for both surfaces).
**send** delivers immediately when idle, and auto-queues when the session is busy.

## Data

```
~/.agenthail/
  registry.db   # sessions, aliases, channels, routes, steer queue
  daemon.pid    # daemon process id
  daemon.log    # daemon stdout/stderr
```

## Configuration

- `AGENTHAIL_CHROME_PROFILE` - Chrome profile name for cookies (default: `Default`)
- `AGENTHAIL_SIDECAR` - path to the curl_cffi sidecar (Python)
- `AGENTHAIL_COOKIE_BRIDGE` - path to the Node cookie bridge
- `AGENTHAIL_CODEX_INSPECT` - Codex Node inspector port (default: 9230)
- `AGENTHAIL_CODEX_REMOTE` - Codex renderer CDP port (default: 9231)
- `AGENTHAIL_NOTION_SPACE` - Notion space ID (auto-detected if unset)
- `AGENTHAIL_NOTION_USER` - Notion user ID (auto-detected if unset)
- `AGENTHAIL_NOTION_TZ` - Notion timezone (auto-detected if unset)

## Layout

```
cmd/agenthail/        entry point
internal/
  surface/            Surface interface + types
    surfaces/         Claude (sidecar) + Codex (CDP) adapters
      transport.go    claude-worker subprocess bridge
  registry/           SQLite store
  daemon/             background watcher + auto-relay
  cli/                command dispatch
sidecar/              Python claude-worker + Node cookie bridge
```

Each surface implements one interface. The daemon and CLI are surface-agnostic;
adding a new agent (e.g. Notion) means implementing one adapter.
