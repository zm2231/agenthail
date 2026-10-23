# Claude peer messaging

The daemon publishes one live helper process per advertised non-Claude agent, bounded by the peer pool described below, plus one per recent sender. Each helper owns its real PID-named Claude registry file and Unix socket, so Claude can address the individual agent and send replies back. Registration itself performs no inference. Native Claude sessions already publish their own records. See `internal/daemon/claude_peers.go:registerRecentClaudePeers`, `internal/peerbridge/manager.go:Advertise` and `internal/peerbridge/manager.go:Ensure`.

Startup, each catalog session change that discovery commits (a burst of changes within a second is one pass), and a 30-second refresh advertise busy agents and agents active within the last 24 hours, ranked busy first and then by most recent activity and capped at the pool size minus a 16-slot sender reserve (112 of 128). Registration reads the catalog rows that discovery committed as busy or active within those 24 hours, rather than the whole catalog or the surfaces themselves, and it skips catalog rows that discovery marked stale, so a failing surface is listed once per discovery pass and a newly discovered agent becomes a peer without waiting for the refresh. Advertising is not activity: it never refreshes a helper's last use, so a session that leaves the advertised set retires on the next pass unless it sent within 24 hours. Any other sender is registered on demand through `peerbridge.Send`, which counts as activity. When the pool is full, an advertised session displaces the least recently used helper outside the advertised set, and a sender displaces that same helper or, failing one, the lowest ranked advertised helper. The helpers for the person and the Agenthail CLI are never displaced, so a send does not fail for peer capacity. A displaced helper is recreated on its next outbound send. Each helper uses a process and a SQLite connection; this is intentionally a per-agent identity, not a single shared alias.

## Operator path

```sh
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

A target may be the `from-session` id a peer message carries: it names the session with that peer id, on this host or a paired one (`registry.CanonicalSession`, which both `agenthail send` and `/api/v1/actions` resolve through). Peer ids are derived rather than stored, so resolving one hashes every session id; with about 1,600 sessions that takes a few milliseconds, and only a target that matches no session id reaches it. `--from` resolves aliases and surface-qualified IDs. Otherwise identity is resolved in this order, at call time: `AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` (the conversation ID Claude Code sets in every command it runs), then `CODEX_THREAD_ID`. Claude Code is checked before Codex so an inherited parent Codex thread cannot override a Claude-launched command's own identity. Agenthail strips all of these, plus Claude's Remote Control and messaging variables, from the environment of every agent it starts. A sender already present in the registry is identity evidence; resolving it does not open or take ownership of that sender's writable transport. When none of these is set, the send comes from `agenthail-cli`, whose peer no model reads; its envelope says no one reads replies, and a SendMessage to it is refused. When one is set but names no session of this Agenthail, `sourceSessionID` refuses with the variable, the registry path, and two remedies: `--from`, or unsetting that variable and every later one that is set. It does not send as the CLI. A sender that is itself a native Claude session transmits from its own existing socket via `claudepeer.SendNative`, so the receiver's native reply reaches that session rather than an Agenthail helper. Explicit sender identity survives durable queue retries and relay routing. The dashboard send API accepts `sourceSessionId`. Composers declare `"author": "person"`, which the handler rejects together with `sourceSessionId`; a sourceless send is from `agenthail-person` when the person wrote it and from `agenthail-cli` otherwise (`surface.SourcelessSender`). On the peer route the person's message starts with a line naming them (`agenthail person name`, else the account's name; a paired host forwards the name it has for them) and saying they read the reply in that session, and the agent card prints the person peer's `from-session` so a session can tell them from a peer that borrows the name. `delivery.RouteFor` sends a person's message through `Claude.SendHumanTurn` to the session's Remote Control attachment whenever one exists. The author persists as `message_queue.author`, and the route is recomputed at drain time, so a queued composer message replays as a user turn. Unmarked sends and every send with an agent sender keep the native socket when the session has one, as do person-authored sends to sessions without a Remote Control attachment; bridge-only sessions keep receiving every send through Remote Control. See `internal/cli/source.go`, `internal/surface/surfaces/claude_peer.go`, `internal/delivery/delivery.go`, `internal/registry/registry.go`, `internal/daemon/outbox.go`, and `internal/daemon/dashboard.go`.

Claude sees names like `agenthail/codex: builder`. These are external peers, not native Claude agents or permission authorities. The UUID is derived from the canonical surface and session ID. Alias changes update the published name. The wrapper carries the reply socket and source UUID. An external peer states no `from-mode`, because it has no Claude permission mode. A Claude session sending through its own socket states its permission class only when the Claude host runs it and reports its live mode: `bypass` for `bypassPermissions`, `prompting` for the other modes except `plan`, whose class Claude decides from settings the host cannot see. Any other sender states none, since Claude records no mode beside its socket and a transcript holds only the mode of its last prompt. Native peer text is preserved as peer provenance when queued into the original agent. Read-only and offline destinations are rejected; registration does not make them writable.

## Claude config roots

Claude Code keeps `sessions/`, peer key files, `jobs/` and `projects/` under one config root: `CLAUDE_CONFIG_DIR` when it is set, otherwise `~/.claude`. Further roots are listed in `~/.agenthail/config.json`:

```json
{ "claudeConfigDirs": ["/Users/me/.claude-work"] }
```

`surface.ClaudeConfigRoots` is the one resolver the CLI, the daemon and the peer workers use. It returns the primary root first, then each listed root, once each even when named twice or through a symlink. A config that is not a JSON object, a `claudeConfigDirs` that is not a list of absolute directory paths, or a file over 64 KiB is an error: discovery fails with the config path in the message, and `doctor` reports it. A changed config applies on the next discovery pass.

No root is stored. Each pass reads the session records and transcripts of every root, and a session belongs to the root that holds them. Sends and lifecycle actions run in that root: `claude agents`, `stop` and the background resume get `CLAUDE_CONFIG_DIR=<root>`, and the Claude host opens a resumed conversation with `configDir` set to it. A native send authenticates with the peer key beside the target process's record, in the root that holds it. Agenthail's own peer workers use the primary root. A new session uses the primary root unless its creation names a `configRoot`. `surface.ResolveClaudeConfigRoot` accepts a root's name (`default`, or the folder name, with the parent's when the folder is `.claude`, prefixed with parent folders while two roots would share it) or the root's path. The dashboard and app pickers send the root's path, or nothing for the primary. The host start then gets `configDir`, the background start gets `CLAUDE_CONFIG_DIR`, and a terminal launch runs `env CLAUDE_CONFIG_DIR=<root> claude`. The primary root is never named, so an explicit `default` still starts with the inherited environment.

### Current login

A root is a config folder, not an account. Claude Code keeps one login per root and `/login` can replace it at any time. Every Claude Code process of the root, including one that started under the earlier login, uses the root's current login from its next request on, and the change drops the root's Remote Control bridges. A proxied install has no login at all.

`surface.DescribeClaudeConfigRoots` reads the global config Claude Code keeps for each root: `<root>/.claude.json` for a root named with `CLAUDE_CONFIG_DIR`, and `~/.claude.json` for the primary root when `CLAUDE_CONFIG_DIR` is unset. From `oauthAccount` it takes the email, organization and plan as `signedInAs`, and the account and organization ids, which are never serialized. Credentials are never read. Claude Code's own `cachedUsageUtilization` is not read: it is refreshed only when Claude's usage view runs and is dropped when the root signs into another login. The parse is cached by file size and modification time.

`Claude.ClaudeConfigRoots` adds what only the process that reads browser cookies knows: `signin`, the sign-in profile signed into the root's login on claude.ai, and `usage`, that login's limits from `GET /api/organizations/<org>/usage` as of `fetchedAt` (see [claude-human-turn-profiles.md](claude-human-turn-profiles.md)). A root with no matching profile has `signinProblem`, and a failed usage read `usageProblem`. The daemon serves it at `GET /api/v1/claude/roots`.

The described roots reach `agenthail roots [--json]`, `doctor`, `signin list`, the `claudeConfigRoots` list of `/api/v1/session-options` (answered by the host that owns the session, without usage, which the form reads per root from `/api/v1/session-options/claude-usage`), and each Claude row's `claudeConfigRoot` (`name`, `root`, and `signedInAs`, the root's current login), which is derived from the root that holds the transcript. A row whose transcript does not exist yet has no config root until Claude writes it.

### Adopting a session

`Claude.AdoptSession` (`surface.SessionAdopter`) reopens a conversation in the host, in the root that holds it, under the conversation lock. A conversation the host already holds is returned unchanged. A holder that is running a turn, waiting on a request, or running background work (`surface.BackgroundRunning` of its tasks) is never stopped: adoption is refused with `AdoptBusyError` or waits up to `Wait`. An idle background job is stopped with `claude stop`, and an idle terminal is sent SIGTERM only when the request carries `StopTerminal`; without it adoption returns `AdoptConsentError`. Once no process holds the conversation it is resumed in the host. The dashboard action is `session-adopt` with `stopTerminal`; it answers 409 with `busy` or `needsConsent`.

When the API refuses a turn, Claude Code writes an assistant record with `isApiErrorMessage` in place of the reply. The timeline turns it into a `notice` item; a usage limit (`error: "rate_limit"` or `apiError: "usage_limit_reached"`) has status `usage_limit` and `resetsAt` from `quotaLimits` or `apiErrorParams.rate_limit_info`, carried through the stream event and the journal. Any such record ends its turn rather than interrupting it, in the live stream, transcript reads and turn observation alike: its `done` item has status `failed` and the refusal as its text, so `send --stream`, `send --reply` and voice delegation fail with it; the reply's error is the limit with its reset time, or `Claude API error: <message>` for another refusal. The record's `<synthetic>` model is never taken as the session's model.

A conversation whose records or transcripts appear in two roots is refused for sends, reads and lifecycle actions with an error naming both roots, rather than resolved to either copy. It is still listed.

## Durable reply endpoint design

A non-Claude source receives a generated `@handle` only when it sends to a Claude session for the first time. The registry reserves that handle transactionally without taking another session's name; a later explicit `identify` rename remains the source of truth. Background peer discovery may publish an existing handle but must not mint one. Both the helper record and the wire envelope use `agenthail/<surface>: @handle` so the visible peer and the received message agree. The canonical UUID remains an authenticated wire identifier, not a user-addressable substitute for the handle.

The worker's `/tmp/cc-socks/<pid>.sock` cannot be a restart-stable reply address: Claude validates that destination as a live, PID-named socket and replies to the literal `from` value in the received envelope. A durable design therefore starts one separate, per-source relay process only for a non-Claude source; that relay owns its own live PID-named socket across peer-worker replacement. The manager passes that relay socket through the private worker control request, while the published envelope keeps the source's canonical identity and reserved handle. The relay verifies each inbound Claude sender against a current same-user native record, retains provenance only for the live socket it observed, and queues into the source's logical registry session. It must neither store/reuse a retired worker socket nor infer a source from an untrusted literal ID. Native Claude-to-Claude sends continue to use the caller's actual native socket and do not create a relay or a person or CLI peer. Accepted peer frames emit one `received` status receipt; queue insertion remains transport acceptance, not model completion. The acceptance gate is one native reply delivered exactly once after replacing the source helper, using fresh disposable sessions.

`transport_accepted` evidence means the socket closed after accepting bytes. It is not a completed model turn. A receiving Claude may hold or deny a message under its own policy; asynchronous status receipts appear in history. Claude holds a message to a session that runs without permission prompts unless its sender states that it does too. Every message is sent under its delivery's uuid, so Claude's reports name the delivery. When the Claude host runs the target, its `peer_message_hold` reports set the delivery to `held`, listed among delivery problems with the cause in plain words and announced to an agent sender with one `[agenthail delivery held ...]` notice, then to delivered when Claude releases it or to failed when Claude drops it. A hosted session has no prompt to approve a held message, so it expires. The sender's peer worker records an expired, refused or dropped receipt the same way. Native message IDs cannot be correlated reliably with transcript turn IDs, so `send --reply`, `--stream`, and slash commands are rejected. Transcript reads remain available. Compact, model, interrupt, and steer require a separately resolved Remote Control attachment; see [Remote Control evidence](#remote-control-evidence). Those controls do not enter the peer message socket or ordinary message queue. A socket failure does not trigger a second send through HTTP.

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
