# MCP server: session credentials, injection, read tools, scope and limits

The daemon serves an MCP server named `agenthail` at `/mcp` on its own listener (`127.0.0.1:7412` by default). It uses the official Go SDK (`github.com/modelcontextprotocol/go-sdk`) over streamable HTTP in stateless mode with JSON responses: every request carries its own bearer, and no `Mcp-Session-Id` state is kept.

The server negotiates only protocol revisions `2026-07-28`, `2025-11-25` and `2025-06-18`, the ones with `structuredContent`, which every tool result uses. A client that asks for an older revision is answered with `2025-11-25`.

## Who may call it

- Only `ahs_` session credentials are accepted. The dashboard token and paired-device tokens are refused.
- Only callers on this machine are accepted. A request from a non-loopback address, or one carrying forwarding headers (`Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host`, `X-Real-Ip`, `Tailscale-User-Login`), is refused with `403 remote_refused`. Remote access publishes the listener through a proxy that also arrives from loopback, so the address alone is not enough.
- A cross-origin browser request is refused (`http.CrossOriginProtection`), and the SDK's localhost DNS-rebinding check stays on.
- All of this runs before any MCP method. A refused request gets an HTTP error with `{error: {code, message}, retryable}`:

| Status | Code | When |
| --- | --- | --- |
| 401 | `unauthenticated` | No credential, or one that is unknown |
| 401 | `credential_expired` | `detail.reason: "idle"`: unused for a day, `retryable: true`, revived by the session's next turn. `detail.reason: "max"`: 30 days old, `retryable: false`, only a newly issued credential works |
| 401 | `credential_revoked` | Rotated, revoked, or its session was archived or moved to another host; `detail.reason` names which |
| 403 | `remote_refused` | Not a local caller |

## Session credentials

The registry table `session_credentials` holds at most one live credential per session (a partial unique index on `session_id` where `revoked_at_ms=0`).

- **Token:** `ahs_` plus 32 random bytes in base64url. Only its SHA-256 is stored; the raw token is returned once, when it is minted, and is never logged.
- **Issue:** `issueSessionCredential` reuses the live credential when its capabilities and ceiling still match, and otherwise revokes it (`rotated`) and mints a new one. A reused credential's token cannot be recovered, so the caller keeps the token it was first given.
- **Default capabilities:** `read.self`, `read.project`, `spawn`, `control.descendants`, `requests.answer`, `ledger.read` and `ledger.write`. A project's coordinator also gets `control.project`. The ceiling of a person-started session is unbounded (`{}`).
- **Idle expiry, 24 hours:** every authenticated call refreshes it. An expired credential is suspended, not revoked: the next turn the observer sees (a new active turn or a new completion) refreshes it.
- **Maximum expiry, 30 days from issue:** nothing extends it, and a turn does not revive it. Issuing again after it mints a fresh credential.
- **Revocation:** archiving a session revokes its credential in the same transaction. Authentication also revokes a credential whose session is archived or now belongs to another host. Deleting a session deletes its credentials.

## How a session gets the server

Every Claude conversation the host runs and every Codex thread Agenthail starts, resumes or forks is given the `agenthail` server with its own bearer.

Its tools run without asking for approval in both runtimes. The credential's capabilities are the gate, so a per-call prompt would only stall agents (and a voice operator mid-call) without adding a check.

The raw token exists in these places only, never in the registry or a log:

- the daemon that minted it, in memory, until the daemon exits;
- the runtime that uses it: the Claude host's session (until the conversation closes) and Codex's loaded thread config;
- for a terminal launch, the launch file described below, until the CLI takes it or it is swept.

**Pending credentials.** A new session has no row to bind a credential to when its token must already be in the start call, and a Codex thread has no id until `thread/start` returns. So the daemon mints the credential unbound, with a `pending_ref` and a short lifetime:

- an unbound credential never authenticates;
- the ref is claimed once for one session id (`claimed_session_id`); a second claim, or one after the lifetime, is refused;
- the claimed credential binds when that session registers, in the registration transaction, and replaces any live credential the session had;
- an unbound credential past its lifetime is deleted when the daemon next issues one and when the MCP server starts.

The lifetime is the start timeout plus its naming margin (50 s) for starts the daemon runs itself, and the pending-launch lifetime (5 min) for terminal launches.

**Codex.** Every `thread/start`, `thread/resume` and `thread/fork` goes through `codexThreadRequest`, which adds `config.mcp_servers.agenthail` (`url`, `http_headers.Authorization`, `tool_timeout_sec: 3700`, `default_tools_approval_mode: "approve"`). `TestOnlyCodexThreadRequestSendsThreadStartResumeOrFork` fails if any other code sends those methods.

- Start and fork carry a pending credential and claim it for the returned thread id.
- Codex connects a thread's MCP servers from the config of the call that loads the thread and ignores the config of a resume that finds it loaded. A resume therefore carries the session's token when the daemon holds it. When it does not (after the daemon restarts), the resume goes out bare and `mcpServerStatus/list` for that thread shows whether the server is connected. If not, the credential is rotated and the thread is unsubscribed and resumed with the new token, then checked again; a thread another client keeps loaded is logged and gets the server at its next load.

**Terminal launches.** A tmux or cmux Codex launch runs `agenthail codex` in the terminal, which starts the thread itself. The daemon mints a pending credential for the launch; the launcher writes it to `~/.agenthail/launches/<launch id>.mcp` (mode 0600) before the terminal starts, and removes it if the launch never starts. Files older than the pending-launch lifetime are swept whenever the daemon issues a launch credential or starts the MCP server. The CLI takes the file (reads it once and removes it) before any other step that can fail, starts its thread with the token, and claims the ref through `POST /api/v1/mcp/claim` (`{ref, threadId}`, dashboard token). The claimed credential binds when the daemon registers the located session.

**Claude host.** Every host `open` goes through `hostOpen`, which passes `mcp: {url, token}`; `TestOnlyHostOpenOpensAClaudeHostConversation` keeps it the only caller. The host gives Claude an http MCP server named `agenthail` whose tools run without asking (`allowedTools: ["mcp__agenthail"]`). A new conversation claims a pending credential for the id chosen before `open`; a resume sends the held token, or rotates the session's credential, or, for a conversation not yet registered here, claims a pending one. Opening a conversation the host already runs with a different token replaces the server on the live query (`setMcpServers`). When a retiring host still holds the conversation, `open` hands that host the new token through `replaceMcp`. A swap the server refuses is returned as `mcpError` on the opened session and logged; the host records a new token only after its swap succeeds, so the next open retries a failed one. A narrowing checker opens without the server.

**Permission changes.** A session's capabilities change when it becomes or stops being its project's coordinator: a session in the registry's local lead set (`LocalLeadSessions`) holds `control.project`, every other session does not. Reconciliation is level-triggered. On every pass the daemon reads every live credential and the lead set from the registry and rotates each credential whose capabilities differ from what its session should hold, so the old token is refused as `credential_revoked`. It remembers nothing between passes, so a restart, a failed rotation, a coordinator start that fails part-way, and a lead change made while the daemon was stopped are all corrected the same way.

- The status loop runs a pass every second, and the first pass after the daemon starts fixes whatever it finds. The daemon also runs a pass at once after its own coordinator changes (set, clear, replace, and any outcome of starting a coordinator's conversation).
- A rotation that fails is logged and tried again on the next pass.
- A coordinator's new conversation is minted with `control.project` before it is registered, so its first turn already has project control; if the start fails before the handover, the next pass takes that capability away.
- Rotated tokens reach their runtimes after the pass releases its lock.
- A hosted Claude conversation gets the new token at once through the host's `replaceMcp` call.
- A Codex thread is marked for re-attach. Its next input or resume unsubscribes and resumes it with the new token, even when Codex still has it loaded, and checks the server connected. Until then its old token is refused.

## Read scope

A session reads:

- itself (`read.self`);
- every session it spawned, transitively through `session_origins.creator_session_id` (`control.descendants`);
- the live local sessions of its own project (`read.project`). A session's project is the registered project containing its cwd; a session with no project reads only itself and its descendants.

Archived sessions and sessions owned by other hosts are outside every scope. A session outside the caller's scope is refused with `out_of_scope`; an unknown one with `not_found`.

## Tools

| Tool | What it returns |
| --- | --- |
| `agenthail_whoami` | The calling session, alias, surface, cwd, host, project, coordinator role, capabilities, ceiling and credential expiry |
| `agenthail_capabilities` | Capabilities, ceiling, read reach, configured surfaces and limits |
| `agenthail_sessions` | Visible sessions with a `mine`, `project` or `all-visible` filter, paged with `cursor` |
| `agenthail_read` | Timeline items of one visible session: the latest page; older pages through `before`; newer journal items through `since` (pass back `latestSeq`); a clipped item's full body through `bodyRef` and `offset` |
| `agenthail_requests` | The visible sessions that are waiting on a prompt or question, or the open requests of one session, in one call without a cursor; `requestId` for one request's longer prompt |
| `agenthail_ledger_read` | The caller's project ledger: the board (counts and goals), one list paged with `cursor` (`goals`, `asks`, `decisions`, `questions`, `work`), or the top of the ranked `ready` queue |

`agenthail_read` and `/api/v1/session` share `readSessionPage`, and `agenthail_requests` and `/api/v1/requests` share `openRequests`.

## Output limits

- Every tool result is at most 16 KiB of JSON. A paged list (`agenthail_sessions`, the ledger lists, `agenthail_read`) stops at the byte budget and returns `truncated: true` with a `nextCursor`. A result that still exceeds the cap is refused with `invalid`.
- An item's text longer than 4 KiB is clipped at a character boundary and marked `truncated`. When the journal kept the full body, the item carries a `bodyRef`, and `agenthail_read` pages through it in whole-character chunks of up to 8 KiB.
- A list cursor continues one traversal. A traversal returns every entry that was in the list when it began, and is still in it, exactly once, and never repeats an entry. An entry that joins the list during a traversal may be missed; the next traversal from the start includes it. Cursors are keysets over an order that never changes for an entry: sessions by registry row order, ledger lists by id (each kind's ids count up in the order entries were made).
- The lists an agent acts on are not paged. `agenthail_requests` returns the waiting sessions, or one session's open requests, in one call, clipping prompts so they fit (at least 256 bytes each, `clipped: true` when cut); `requestId` returns one request with its prompt up to 4 KiB. When even the clipped rows exceed the 16 KiB cap, it returns the oldest that fit, with `total` and `truncated: true` and no cursor. `ready` is a ranked queue that reorders as work moves; it returns its first `limit` entries (at most 50) as compact rows with `total`.
- A read-back cursor names the page and the oldest item already returned, so a page the byte cap cut resumes inside that page. If the session's latest page moved on in between, the read is refused as `not_found` with `detail.stale: true`. Following a cursor past the oldest retained history returns an empty, final page.

## Tool refusals

A tool that refuses returns an MCP tool error (`isError: true`) whose structured content is `{code, message, retryable, detail}`. Errors from the shared operations map onto these codes by explicit rules in `mcpCodeFor`:

- any `400` → `invalid`; any `404` → `not_found`;
- `409 stale` → `not_found` with `detail.stale`;
- `409 move_busy` and `session_adopt_busy` → `busy`, retryable;
- other `409`s → `refused`;
- `502 control_failed` → `busy`, retryable;
- `502 *_unknown` → `busy` with `detail.outcome: "unknown"`, not retryable;
- other `502`s and any `500` → `refused`, retryable.

`TestEveryActionErrorMapsToAnMCPCode` fails when the parity table records an error code with no rule.
