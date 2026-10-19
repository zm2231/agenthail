# Claude peer messaging

The daemon publishes one live helper process per advertised non-Claude agent, bounded by the peer pool described below, plus one per recent sender. Each helper owns its real PID-named Claude registry file and Unix socket, so Claude can address the individual agent and send replies back. Registration itself performs no inference. Native Claude sessions already publish their own records. See `internal/daemon/claude_peers.go:registerRecentClaudePeers`, `internal/peerbridge/manager.go:Advertise` and `internal/peerbridge/manager.go:Ensure`.

Startup, every catalog session change that discovery commits, and a 30-second refresh advertise busy agents and agents active within the last 24 hours, ranked busy first and then by most recent activity and capped at the pool size minus a 16-slot sender reserve (112 of 128). Registration reads the session catalog that discovery commits rather than listing surfaces itself, and it skips catalog rows that discovery marked stale, so a failing surface is listed once per discovery pass and a newly discovered agent becomes a peer without waiting for the refresh. Advertising is not activity: it never refreshes a helper's last use, so a session that leaves the advertised set retires on the next pass unless it sent within 24 hours. Any other sender is registered on demand through `peerbridge.Send`, which counts as activity. When the pool is full, an advertised session displaces the least recently used helper outside the advertised set, and a sender displaces that same helper or, failing one, the lowest ranked advertised helper. The operator helper is never displaced, so a send does not fail for peer capacity. A displaced helper is recreated on its next outbound send. Each helper uses a process and a SQLite connection; this is intentionally a per-agent identity, not a single shared alias.

## Operator path

```sh
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

`--from` resolves aliases and surface-qualified IDs. Otherwise identity is resolved in this order, at call time: `AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` (the conversation ID Claude Code sets in every command it runs), then `CODEX_THREAD_ID`. Claude Code is checked before Codex so an inherited parent Codex thread cannot override a Claude-launched command's own identity. Agenthail strips all of these, plus Claude's Remote Control and messaging variables, from the environment of every agent it starts. A sender already present in the registry is identity evidence; resolving it does not open or take ownership of that sender's writable transport. When none of these is set, the operator peer stores replies in history without dispatching a model. When one is set but names no session of this Agenthail, `sourceSessionID` refuses with the variable, the registry path, and two remedies: `--from`, or unsetting that variable and every later one that is set. It does not send as the operator. A sender that is itself a native Claude session transmits from its own existing socket via `claudepeer.SendNative`, so the receiver's native reply reaches that session rather than an Agenthail helper. Explicit sender identity survives durable queue retries and relay routing. The dashboard send API accepts `sourceSessionId`; normal dashboard sends use the operator peer. Composers declare `"author": "person"`, which the handler rejects together with `sourceSessionId`. `delivery.RouteFor` sends a person's message through `Claude.SendHumanTurn` to the session's Remote Control attachment whenever one exists. The author persists as `message_queue.author`, and the route is recomputed at drain time, so a queued composer message replays as a user turn. Unmarked sends and every send with an agent sender keep the native socket when the session has one, as do person-authored sends to sessions without a Remote Control attachment; bridge-only sessions keep receiving every send through Remote Control. See `internal/cli/source.go`, `internal/surface/surfaces/claude_peer.go`, `internal/delivery/delivery.go`, `internal/registry/registry.go`, `internal/daemon/outbox.go`, and `internal/daemon/dashboard.go`.

Claude sees names like `agenthail/codex: builder`. These are external peers, not native Claude agents or permission authorities. The UUID is derived from the canonical surface and session ID. Alias changes update the published name. The wrapper carries the reply socket and source UUID, with no fabricated `from-mode`. Native peer text is preserved as peer provenance when queued into the original agent. Read-only and offline destinations are rejected; registration does not make them writable.

## Durable reply endpoint design

A non-Claude source receives a generated `@handle` only when it sends to a Claude session for the first time. The registry reserves that handle transactionally without taking another session's name; a later explicit `identify` rename remains the source of truth. Background peer discovery may publish an existing handle but must not mint one. Both the helper record and the wire envelope use `agenthail/<surface>: @handle` so the visible peer and the received message agree. The canonical UUID remains an authenticated wire identifier, not a user-addressable substitute for the handle.

The worker's `/tmp/cc-socks/<pid>.sock` cannot be a restart-stable reply address: Claude validates that destination as a live, PID-named socket and replies to the literal `from` value in the received envelope. A durable design therefore starts one separate, per-source relay process only for a non-Claude source; that relay owns its own live PID-named socket across peer-worker replacement. The manager passes that relay socket through the private worker control request, while the published envelope keeps the source's canonical identity and reserved handle. The relay verifies each inbound Claude sender against a current same-user native record, retains provenance only for the live socket it observed, and queues into the source's logical registry session. It must neither store/reuse a retired worker socket nor infer a source from an untrusted literal ID. Native Claude-to-Claude sends continue to use the caller's actual native socket and do not create a relay or operator peer. Accepted peer frames emit one `received` status receipt; queue insertion remains transport acceptance, not model completion. The acceptance gate is one native reply delivered exactly once after replacing the source helper, using fresh disposable sessions.

`transport_accepted` evidence means the socket closed after accepting bytes. It is not a completed model turn. A receiving Claude may hold or deny a message under its own policy; asynchronous status receipts appear in history. Native message IDs cannot be correlated reliably with transcript turn IDs, so `send --reply`, `--stream`, and slash commands are rejected. Transcript reads remain available. Compact, model, interrupt, and steer require a separately resolved Remote Control attachment; see [Remote Control evidence](#remote-control-evidence). Those controls do not enter the peer message socket or ordinary message queue. A socket failure does not trigger a second send through HTTP.

## Lifecycle and verification

The daemon manager is the only routing authority. It holds a process-lifetime file lock and removes its public socket on shutdown only when the socket identity still matches. Callers send ensure or delivery requests to that private endpoint; they do not derive a worker control path from a session ID. A request whose response is lost after transmission has an unknown outcome and is not automatically retried. Each daemon generation gives its workers unique control sockets under `~/.agenthail/run/p/<generation>/`, while Claude-facing sockets retain the required `/tmp/cc-socks/<pid>.sock` shape. Before creating those artifacts, the worker writes a pending ownership manifest; it atomically records each socket identity as it is created, then promotes that manifest before publishing the Claude session record. The manifest ties the source session, daemon generation, worker PID/process start, random launch token, control socket inode, Claude-facing socket inode and Claude session record together. Registration checks the live control endpoint and replaces a worker whose endpoint disappeared or changed.

Helpers exit on parent stdin EOF. Registration files are rewritten atomically when the published name, working directory or status changes, restored if removed, and never deliberately replace an existing foreign record. An unchanged record is left alone, as Claude Code does with its own records. Socket permissions are 0600. On startup, Agenthail reconciles ownership manifests, retires proven Agenthail orphans and removes only artifacts whose manifest and file identity still match. Legacy sockets under `~/.agenthail/peers` are no longer reused and are left untouched because they do not carry enough ownership evidence for automatic deletion. Live foreign or ambiguous endpoints and non-socket files are preserved. It never scans and deletes arbitrary `/tmp/cc-socks` entries.

Cleanup tests cover parent exit, daemon crash and restart, preservation of unowned legacy sockets, dead ownership manifests, preservation of live or ambiguous artifacts, forced child death and restart, duplicate registration, distinct per-agent PIDs, manager-routed sends, reply deduplication, read-only rejection, cancellation, alias refresh and idle retirement. Sender persistence tests cover schema upgrades, ID merges, queue claiming, relays, dispatch and dashboard output.

The manager admits at most 128 peer workers and 32 concurrent manager requests. A new worker beyond the first bound displaces an existing one as described above; a request beyond the second bound receives an explicit retry error instead of creating another goroutine.

The `ps` probe and Claude 2.1.267+ records use UTC; older native records use
local time. Agenthail compares them as instants using the record version after
validating the PID, socket path, socket owner and socket type. A different start instant remains a
recycled-PID mismatch and the socket is rejected. Without that normalization, a
valid Claude 2.1.270 peer can be misclassified as a non-UDS session and a message
can remain queued despite the peer being idle.

Validation commands:

```sh
env -u CODEX_THREAD_ID -u CLAUDE_CODE_SESSION_ID -u AGENTHAIL_SESSION_ID \
  go test -race -p 1 -count=1 -timeout 90s ./...
go vet ./...
go build ./...
go mod verify
node --check internal/daemon/dashboard/app.js
bash scripts/check-public-copy.sh
git diff --check
```

The subprocess tests build the real CLI and exercise native sockets against isolated peer fixtures. A plain-body wrapper emitted by Go was additionally accepted by the extracted installed Claude 2.1.263 `oF` parser and its canonical rebuild; that probe does not cover every Unicode body or permission policy. No messages were sent to live user Claude sessions, no model-response round trip was claimed, and the running daemon was not restarted. Review was performed locally under the user's instruction to keep the work with the implementing agent.

## Remote Control evidence

Every Remote Control control first reads `GET /v1/code/sessions/<cse id>` and
requires that exact session with `connection_status` `connected`. A bridge id in
the local session record only shows the session was attached at some point.

| Control | Request | Evidence |
| --- | --- | --- |
| model | `control_request` `set_model` | `delivered` when a `control_response` with the same `request_id` succeeds; `failed` on its error |
| interrupt | `control_request` `interrupt` | `delivered` when the response succeeds and the turn that was active ends; a turn that finished on its own also ends |
| steer | user event | `transport_accepted` with the posted event uuid |
| context, usage, effort, rename | slash command | `reply_observed` when a `local_command` record with the same `commandRun` is joined to the request by `parentUuid`, or by a cloud event after it; a competing prompt first makes it `unknown` |
| status | none | `reply_observed`; the model comes from `external_metadata` |
| compact | `/compact` | `delivered` when a `compact_boundary` follows the exact request |

Responses are read from `GET /v1/sessions/<session id>/events?after_id=<uuid>`,
starting at the posted event. An accepted request whose response or effect is not
observed before the deadline is `unknown`, carries its request id, and is not
retried. History records each control under the kind that reads back as its
evidence, with the request id as the result.
