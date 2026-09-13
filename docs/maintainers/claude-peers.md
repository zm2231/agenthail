# Claude peer messaging

The daemon publishes one live helper process per discovered non-Claude agent. Each helper owns its real PID-named Claude registry file and Unix socket, so Claude can address the individual agent and send replies back. Registration itself performs no inference. Native Claude sessions already publish their own records. See `internal/daemon/claude_peers.go:registerRecentClaudePeers` and `internal/peerbridge/manager.go:Ensure`.

The default adapter discovery page defines “recent”, without an additional age cutoff. Startup and a 30-second refresh register every non-offline entry, including read-only entries. An older sender is registered on demand through `peerbridge.Send`. Helpers remain available until daemon shutdown, or are replaced after exit on the next registration attempt. Each helper uses a process and a SQLite connection; this is intentionally a per-agent identity, not a single shared alias. There is no eviction policy for agents that fall off the recent page.

## Operator path

```sh
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

`--from` resolves aliases and surface-qualified IDs. Otherwise identity comes from `AGENTHAIL_SESSION_ID`, `CODEX_THREAD_ID`, then `CLAUDE_SESSION_ID`. A sender already present in the registry is identity evidence; resolving it does not open or take ownership of that sender's writable transport. With no identity, the operator peer stores replies in history without dispatching a model. Explicit sender identity survives durable queue retries and relay routing. The dashboard send API accepts `sourceSessionId`; normal dashboard sends use the operator peer. See `internal/cli/source.go`, `internal/delivery/delivery.go`, `internal/registry/registry.go`, `internal/daemon/outbox.go`, and `internal/daemon/dashboard.go`.

Claude sees names like `agenthail/codex: builder`. These are external peers, not native Claude agents or permission authorities. The UUID is derived from the canonical surface and session ID. Alias changes update the published name. The wrapper carries the reply socket and source UUID, with no fabricated `from-mode`. Native peer text is preserved as peer provenance when queued into the original agent. Read-only and offline destinations are rejected; registration does not make them writable.

`peer_transport_accepted` means the socket closed after accepting bytes. It is not a completed model turn. A receiving Claude may hold or deny a message under its own policy; asynchronous status receipts appear in history. Native message IDs cannot be correlated reliably with transcript turn IDs, so `send --reply`, `--stream`, slash commands and steering are rejected. Transcript reads remain available. Interrupt/model control requires a Remote Control bridge. A socket failure does not trigger a second send through HTTP.

## Lifecycle and verification

Helpers exit on parent stdin EOF. Registration files are refreshed atomically, restored if removed, and never deliberately replace an existing foreign record. Socket permissions are 0600. Cleanup tests cover parent exit, forced child death and restart, duplicate registration, distinct per-agent PIDs, reply deduplication, read-only rejection, cancellation, and alias refresh. Sender persistence tests cover SQLite v1-to-v2 upgrades, ID merges, queue claiming, relays, dispatch and dashboard output.

Native Claude 2.1.267+ records and `ps` may render the same process start in UTC
and local time; older records use local time. Agenthail compares those values as
instants using the record version after validating the PID, socket path, socket
owner and socket type. A different start instant remains a
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

The subprocess tests build the real CLI and exercise native sockets against isolated peer fixtures. A plain-body wrapper emitted by Go was additionally accepted by the extracted installed Claude 2.1.263 `oF` parser and its canonical rebuild; that probe does not cover every Unicode body or permission policy. No messages were sent to live user Claude sessions, no model-response round trip was claimed, and the running daemon was not restarted. Review was performed locally under the user's instruction to keep the work with the implementing agent.
