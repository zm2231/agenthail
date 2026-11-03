# MCP server: session credentials, read tools, scope and limits

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
| `agenthail_requests` | Every visible session that is waiting on a prompt or question, or every open request of one session; `requestId` for one request's longer prompt |
| `agenthail_ledger_read` | The caller's project ledger: the board (counts and goals), one list paged with `cursor` (`goals`, `asks`, `decisions`, `questions`, `work`), or the top of the ranked `ready` queue |

`agenthail_read` and `/api/v1/session` share `readSessionPage`, and `agenthail_requests` and `/api/v1/requests` share `openRequests`.

## Output limits

- Every tool result is at most 16 KiB of JSON. Lists stop at the byte budget and return `truncated: true` with a `nextCursor`. A result that still exceeds the cap is refused with `invalid`.
- An item's text longer than 4 KiB is clipped at a character boundary and marked `truncated`. When the journal kept the full body, the item carries a `bodyRef`, and `agenthail_read` pages through it in whole-character chunks of up to 8 KiB.
- A list cursor continues one traversal. A traversal returns every entry that was in the list when it began, and is still in it, exactly once, and never repeats an entry. An entry that joins the list during a traversal may be missed; the next traversal from the start includes it. Cursors are keysets over an order that never changes for an entry: sessions by registry row order, ledger lists by id (each kind's ids count up in the order entries were made).
- The lists an agent acts on are not paged. `agenthail_requests` returns every waiting session in one call, and every open request of one session in one call, clipping prompts so they all fit (at least 256 bytes each, `clipped: true` when cut); `requestId` returns one request with its prompt up to 4 KiB. `ready` is a ranked queue that reorders as work moves; it returns its first `limit` entries (at most 50) as compact rows with `total`.
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
