# Claude peer messaging

The daemon publishes one live helper process per eligible non-Claude agent. Each helper owns its real PID-named Claude registry file and Unix socket, so Claude can address the individual agent and send replies back. Registration itself performs no inference. Native Claude sessions already publish their own records. See `internal/daemon/claude_peers.go:registerRecentClaudePeers` and `internal/peerbridge/manager.go:Ensure`.

Startup and a 30-second refresh pre-register busy agents and agents active within the last 24 hours. An older sender is registered on demand through `peerbridge.Send`. Idle helpers age out after 24 hours and are recreated automatically on the next outbound send. Each helper uses a process and a SQLite connection; this is intentionally a per-agent identity, not a single shared alias.

## Operator path

```sh
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

`--from` resolves aliases and surface-qualified IDs. Otherwise identity comes from `AGENTHAIL_SESSION_ID`, `CODEX_THREAD_ID`, then `CLAUDE_SESSION_ID`. A sender already present in the registry is identity evidence; resolving it does not open or take ownership of that sender's writable transport. With no identity, the operator peer stores replies in history without dispatching a model. Explicit sender identity survives durable queue retries and relay routing. The dashboard send API accepts `sourceSessionId`; normal dashboard sends use the operator peer. See `internal/cli/source.go`, `internal/delivery/delivery.go`, `internal/registry/registry.go`, `internal/daemon/outbox.go`, and `internal/daemon/dashboard.go`.

Claude sees names like `agenthail/codex: builder`. These are external peers, not native Claude agents or permission authorities. The UUID is derived from the canonical surface and session ID. Alias changes update the published name. The wrapper carries the reply socket and source UUID, with no fabricated `from-mode`. Native peer text is preserved as peer provenance when queued into the original agent. Read-only and offline destinations are rejected; registration does not make them writable.

`transport_accepted` evidence means the socket closed after accepting bytes. It is not a completed model turn. A receiving Claude may hold or deny a message under its own policy; asynchronous status receipts appear in history. Native message IDs cannot be correlated reliably with transcript turn IDs, so `send --reply`, `--stream`, and slash commands are rejected. Transcript reads remain available. Compact, model, interrupt, and steer require a separately resolved Remote Control identity. Those controls do not enter the peer message socket or ordinary message queue. A socket failure does not trigger a second send through HTTP.

## Alternate Claude config directories

Agenthail discovers `~/.claude` and the directory in `CLAUDE_CONFIG_DIR`.
`AGENTHAIL_CLAUDE_CONFIG_DIRS` adds directories using the platform path-list
separator (`:` on macOS). Values are absolute config directories, not the
`CLAUDE.md` file or its parent home. Paths are normalized and symlink aliases
deduplicated. A session ID published by multiple roots is rejected as ambiguous.

```sh
export AGENTHAIL_CLAUDE_CONFIG_DIRS="/absolute/gentle/.claude"
agenthail list --json
agenthail send claude:<exact-session-id> "Your instruction"
agenthail last claude:<exact-session-id> 3 --json
```

Configure both the CLI and daemon with the same directories. `agenthail daemon
install` saves these variables in launchd; changing a shell variable does not
change an already running daemon. The registry remains in `~/.agenthail`.
Session JSON carries `configDir`, including saved/search/dashboard results.
Queued work retains it through registry reopen. Transcripts and background
lifecycle commands use the selected session's root. New background agents
still start in the default `~/.claude` root.

The peer manager uses the target socket's PID and path hash to find its native
authentication key across configured roots. Duplicate keys and missing native
keys fail explicitly. Tokens are read in place and never copied or emitted.
Agenthail helper peers retain their daemon-owned records and sockets. Configuring
a second Claude root does not create another daemon or change the operator's HOME.

An opt-in live test creates an isolated Agenthail registry and manager, sends
one message to the explicit target and waits for a transcript answer describing
that root. It does not restart the installed daemon:

```sh
go build -o /tmp/agenthail-claude-roots ./cmd/agenthail
AGENTHAIL_LIVE_BINARY=/tmp/agenthail-claude-roots \
AGENTHAIL_LIVE_CLAUDE_CONFIG_DIR=/absolute/config/.claude \
AGENTHAIL_LIVE_CLAUDE_TARGET=<exact-session-id> \
  go test ./internal/surface/surfaces -run '^TestLiveAlternateClaudeConfigMessaging$' -v -count=1
```

## Lifecycle and verification

The daemon manager is the only routing authority. It holds a process-lifetime file lock and removes its public socket on shutdown only when the socket identity still matches. Callers send ensure or delivery requests to that private endpoint; they do not derive a worker control path from a session ID. A request whose response is lost after transmission has an unknown outcome and is not automatically retried. Each daemon generation gives its workers unique control sockets under `~/.agenthail/run/p/<generation>/`, while Claude-facing sockets retain the required `/tmp/cc-socks/<pid>.sock` shape. Before creating those artifacts, the worker writes a pending ownership manifest; it atomically records each socket identity as it is created, then promotes that manifest before publishing the Claude session record. The manifest ties the source session, daemon generation, worker PID/process start, random launch token, control socket inode, Claude-facing socket inode and Claude session record together. Registration checks the live control endpoint and replaces a worker whose endpoint disappeared or changed.

Helpers exit on parent stdin EOF. Registration files are refreshed atomically, restored if removed, and never deliberately replace an existing foreign record. Socket permissions are 0600. On startup, Agenthail reconciles ownership manifests, retires proven Agenthail orphans and removes only artifacts whose manifest and file identity still match. Legacy sockets under `~/.agenthail/peers` are no longer reused and are left untouched because they do not carry enough ownership evidence for automatic deletion. Live foreign or ambiguous endpoints and non-socket files are preserved. It never scans and deletes arbitrary `/tmp/cc-socks` entries.

Cleanup tests cover parent exit, daemon crash and restart, preservation of unowned legacy sockets, dead ownership manifests, preservation of live or ambiguous artifacts, forced child death and restart, duplicate registration, distinct per-agent PIDs, manager-routed sends, reply deduplication, read-only rejection, cancellation, alias refresh and idle retirement. Sender persistence tests cover schema upgrades, ID merges, queue claiming, relays, dispatch and dashboard output.

The manager admits at most 128 peer workers and 32 concurrent manager requests. Calls beyond either bound receive an explicit retry/retirement error instead of creating another process or goroutine.

The `ps` probe and Claude 2.1.267+ records use UTC; older native records use
local time. Agenthail compares them as instants using the record version after
validating the PID, socket path, socket owner and socket type. A different start instant remains a
recycled-PID mismatch and the socket is rejected. Without that normalization, a
valid Claude 2.1.270 peer can be misclassified as a non-UDS session and a message
can remain queued despite the peer being idle.

Validation commands:

```sh
env -u CODEX_THREAD_ID -u CLAUDE_SESSION_ID -u AGENTHAIL_SESSION_ID \
  go test -race -p 1 -count=1 -timeout 90s ./...
go vet ./...
go build ./...
go mod verify
node --check internal/daemon/dashboard/app.js
bash scripts/check-public-copy.sh
git diff --check
```

The subprocess tests build the real CLI and exercise native sockets against isolated peer fixtures. Live-session verification is opt-in and requires an explicitly authorized target. A held inbound message is not a completed round trip; verify the matching reply separately. A transcript reply also does not prove that the reverse socket callback succeeded.
