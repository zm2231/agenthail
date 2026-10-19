# Claude session host

The Claude host is a Node process that runs Claude sessions through the Claude
Agent SDK, so a person's message can reach a session as their own turn without
Remote Control. Its source is [`claude-host/host.mjs`](../../claude-host/host.mjs);
the Go side is [`internal/claudehost`](../../internal/claudehost).

## One host per user

The daemon owns the host. Every 30 seconds, and once at startup, it calls
`Manager.Ensure` ([`claudehost/manager.go`](../../internal/claudehost/manager.go)):

1. Dial `~/.agenthail/claude-host.sock` and exchange `hello`
   (`{protocol, hostVersion}`). A host of the daemon's version is used as is.
2. A host of another protocol or version is asked to drain. It refuses new
   sessions, closes each session as it goes idle and exits when none remain;
   the daemon starts the new host after that.
3. If nothing answers, take an exclusive `flock` on
   `~/.agenthail/claude-host/lock`. If another process holds it, a host is
   starting: wait for its `hello`.
4. Otherwise start the host in its own session (`setsid`), passing the locked
   file as fd 3. The host checks that fd 3 is that lock file and holds the lock
   for its lifetime, so two hosts can never serve the same home.

The host and its Claude processes outlive the daemon. The launchd service sets
`AbandonProcessGroup`, and a restarted daemon attaches to the running host
instead of starting another. Each Claude process is started detached: when
the host dies, a running turn finishes, and the next host reopens the session
with `resume`. A session whose previous process still runs is reported as held
until that process exits.

The host runs the user's own `claude` (`surface.ClaudeBinary`) with the same
environment as the daemon's other Claude launches (`claudeCommandEnv`), so
`CLAUDE_CONFIG_DIR` and the account apply unchanged. Its log is
`~/.agenthail/claude-host/host.log`.

## Protocol

Newline-delimited JSON over the socket, one request per connection:
`hello`, `open`, `send`, `interrupt`, `close`, `list`, `drain`, and `subscribe`,
which streams events after a sequence number and reports a gap when the ring
buffer has moved past it. A failed request returns `{code, message}`:

| Code | Meaning | Delivery outcome |
|---|---|---|
| `not_open` | the host does not run that session | unavailable |
| `held` | the session's previous process still runs | held, retried |
| `draining` | the host is being replaced | held, retried |
| `unknown` | the SDK had not taken the message within ten seconds | unknown, never retried |
| `invalid` | malformed request | terminal |

A `send` is answered only after the SDK has taken the message for Claude's
input, so a success means the message has left the host's queue.

Every permission request is answered. Agenthail does not approve tools on a
person's behalf, so the host denies a tool that the session's permission mode
would prompt for, with a reason, and emits a `permission_denied` event.

## Delivery

A Claude process that the host runs for the conversation it lists is
discovered with runtime kind `claude-host`; the Claude adapter serves both
`claude-cli` and `claude-host` and decides per session from that kind. The
process also serves Claude's native messaging socket, so its transport is
`uds`. It is open while the host lists it; a CLI session is open while its pid
is a running `claude` process. When the host stops listing a process that still
runs, discovery records it as `claude-cli` again.

- A person's message ([`RouteHumanTurn`](../../internal/delivery/route.go))
  goes through the host's `send` with origin `human`. Its uuid is the delivery
  intent's uuid, which Claude stores as the transcript record's uuid: the turn
  is followed exactly, and a resend of the same intent runs once, also across a
  host restart. Steer sends with priority `now`; interrupt goes to the session's
  query.
- Agent, relay and channel messages take `RoutePeer` over the native socket,
  where Claude records the verified sender and the reply address. Claude drops a
  peer origin given on SDK input, so the host is not used for them.
- While the host is down and the session's process still runs, a person's
  message is held and retried.

Every delivery intent has a uuid (`delivery_intents.uuid`). The dispatcher
passes it to the adapter for direct sends, steers and busy handling, and the
outbox for queued messages (`surface.WithMessageUUID`).

## Remote Control

A Claude process started through the SDK does not register a Remote Control
bridge, with or without `--remote-control`, while `claude --bg` under the same
account does. Host sessions therefore have no `bridgeSessionId` and are not
controllable from claude.ai or the Claude apps.

## Packaging

`scripts/stage-claude-host.sh` stages `host.mjs` with its locked dependencies
beside the `agenthail` binary as `claude-host/`. The SDK's bundled Claude
binaries are optional dependencies and are omitted; the remaining dependencies
are plain JavaScript, so one tree serves every platform, and the script fails
if a native module appears. The Mac package runs the host with its bundled
Node. Release archives and `install.sh` installs use `node` from `PATH`.
`AGENTHAIL_CLAUDE_HOST` overrides the script path.

## Tests

```bash
npm ci --prefix claude-host --omit=optional
npm test --prefix claude-host
go test ./internal/claudehost/... ./internal/surface/surfaces/ ./internal/daemon/ -run 'Host|Ensure|Subscribe|Client' -race
```

The tests run the real host against a fake Claude CLI
([`claude-host/test/fake-claude.mjs`](../../claude-host/test/fake-claude.mjs))
that speaks the stream-json protocol, writes a session record and serves a
messaging socket.
