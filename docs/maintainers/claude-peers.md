# Claude peer messaging

The daemon publishes one live helper process per eligible non-Claude agent. Each helper owns its real PID-named Claude registry file and Unix socket, so Claude can address the individual agent and send replies back. Registration itself performs no inference. Native Claude sessions already publish their own records. See `internal/daemon/claude_peers.go:registerRecentClaudePeers` and `internal/peerbridge/manager.go:Ensure`.

Startup and a 30-second refresh pre-register busy agents and agents active within the last 24 hours. The refresh reads the session catalog that discovery commits rather than listing surfaces itself, and it skips catalog rows that discovery marked stale, so a failing surface is listed once per discovery pass. An older sender is registered on demand through `peerbridge.Send`. Idle helpers age out after 24 hours and are recreated automatically on the next outbound send. Each helper uses a process and a SQLite connection; this is intentionally a per-agent identity, not a single shared alias.

## Operator path

```sh
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

`--from` resolves aliases and surface-qualified IDs. Otherwise identity is resolved in this order: `AGENTHAIL_SESSION_ID`, `CLAUDE_SESSION_ID`, the verified native Claude caller discovered through same-user process ancestry matched against native Claude session records under `~/.claude/sessions/` (the innermost ancestor with a validated UDS endpoint), then `CODEX_THREAD_ID`. The verified native Claude caller is checked before `CODEX_THREAD_ID` so an inherited parent Codex thread cannot override a Claude-launched command's own identity. A sender already present in the registry is identity evidence; resolving it does not open or take ownership of that sender's writable transport. An ancestor record without a validated endpoint is an error rather than a fallback. When nothing matches, the operator peer stores replies in history without dispatching a model. A sender that is itself a native Claude session transmits from its own existing socket via `claudepeer.SendNative`, so the receiver's native reply reaches that session rather than an Agenthail helper. Explicit sender identity survives durable queue retries and relay routing. The dashboard send API accepts `sourceSessionId`; normal dashboard sends use the operator peer. See `internal/cli/source.go`, `internal/surface/surfaces/claude_peer.go`, `internal/delivery/delivery.go`, `internal/registry/registry.go`, `internal/daemon/outbox.go`, and `internal/daemon/dashboard.go`.

Claude sees names like `agenthail/codex: builder`. These are external peers, not native Claude agents or permission authorities. The UUID is derived from the canonical surface and session ID. Alias changes update the published name. The wrapper carries the reply socket and source UUID, with no fabricated `from-mode`. Native peer text is preserved as peer provenance when queued into the original agent. Read-only and offline destinations are rejected; registration does not make them writable.

## Durable reply endpoint design

A non-Claude source receives a generated `@handle` only when it sends to a Claude session for the first time. The registry reserves that handle transactionally without taking another session's name; a later explicit `identify` rename remains the source of truth. Background peer discovery may publish an existing handle but must not mint one. Both the helper record and the wire envelope use `agenthail/<surface>: @handle` so the visible peer and the received message agree. The canonical UUID remains an authenticated wire identifier, not a user-addressable substitute for the handle.

The worker's `/tmp/cc-socks/<pid>.sock` cannot be a restart-stable reply address: Claude validates that destination as a live, PID-named socket and replies to the literal `from` value in the received envelope. A durable design therefore starts one separate, per-source relay process only for a non-Claude source; that relay owns its own live PID-named socket across peer-worker replacement. The manager passes that relay socket through the private worker control request, while the published envelope keeps the source's canonical identity and reserved handle. The relay verifies each inbound Claude sender against a current same-user native record, retains provenance only for the live socket it observed, and queues into the source's logical registry session. It must neither store/reuse a retired worker socket nor infer a source from an untrusted literal ID. Native Claude-to-Claude sends continue to use the caller's actual native socket and do not create a relay or operator peer. Accepted peer frames emit one `received` status receipt; queue insertion remains transport acceptance, not model completion. The acceptance gate is one native reply delivered exactly once after replacing the source helper, using fresh disposable sessions.

`transport_accepted` evidence means the socket closed after accepting bytes. It is not a completed model turn. A receiving Claude may hold or deny a message under its own policy; asynchronous status receipts appear in history. Native message IDs cannot be correlated reliably with transcript turn IDs, so `send --reply`, `--stream`, and slash commands are rejected. Transcript reads remain available. Compact, model, interrupt, and steer require a separately resolved Remote Control identity. Those controls do not enter the peer message socket or ordinary message queue. A socket failure does not trigger a second send through HTTP.

## Lifecycle and verification

The daemon manager is the only routing authority. It holds a process-lifetime file lock and removes its public socket on shutdown only when the socket identity still matches. Callers send ensure or delivery requests to that private endpoint; they do not derive a worker control path from a session ID. A request whose response is lost after transmission has an unknown outcome and is not automatically retried. Each daemon generation gives its workers unique control sockets under `~/.agenthail/run/p/<generation>/`, while Claude-facing sockets retain the required `/tmp/cc-socks/<pid>.sock` shape. Before creating those artifacts, the worker writes a pending ownership manifest; it atomically records each socket identity as it is created, then promotes that manifest before publishing the Claude session record. The manifest ties the source session, daemon generation, worker PID/process start, random launch token, control socket inode, Claude-facing socket inode and Claude session record together. Registration checks the live control endpoint and replaces a worker whose endpoint disappeared or changed.

Helpers exit on parent stdin EOF. Registration files are rewritten atomically when the published name, working directory or status changes, restored if removed, and never deliberately replace an existing foreign record. An unchanged record is left alone, as Claude Code does with its own records. Socket permissions are 0600. On startup, Agenthail reconciles ownership manifests, retires proven Agenthail orphans and removes only artifacts whose manifest and file identity still match. Legacy sockets under `~/.agenthail/peers` are no longer reused and are left untouched because they do not carry enough ownership evidence for automatic deletion. Live foreign or ambiguous endpoints and non-socket files are preserved. It never scans and deletes arbitrary `/tmp/cc-socks` entries.

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

The subprocess tests build the real CLI and exercise native sockets against isolated peer fixtures. A plain-body wrapper emitted by Go was additionally accepted by the extracted installed Claude 2.1.263 `oF` parser and its canonical rebuild; that probe does not cover every Unicode body or permission policy. No messages were sent to live user Claude sessions, no model-response round trip was claimed, and the running daemon was not restarted. Review was performed locally under the user's instruction to keep the work with the implementing agent.
