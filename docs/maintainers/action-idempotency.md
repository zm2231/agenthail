# Action idempotency contract

The authenticated action endpoints accept an optional `Idempotency-Key` header:

- `POST /api/v1/actions`
- `POST /api/action`

The key is scoped to the authenticated principal. A paired API device uses its stable registry device ID; the local dashboard uses the fixed local-dashboard principal. Authentication and the existing control/cookie guards run before any receipt lookup.

The daemon canonicalizes the decoded JSON request and stores only its SHA-256 hash. It stores no request body, bearer token, cookie, or raw secret. Keys are bounded to 128 bytes; the canonical request and replay response are bounded by the action request/receipt limits.

Reservation is durable and atomic. The first request inserts a `pending` receipt before invoking the action. A later request with the same principal and key behaves as follows:

| Receipt | Same request hash | Different request hash |
| --- | --- | --- |
| pending | `202` with `{ok:true,status:"submitted"}`; no action dispatch | typed `409 action_idempotency_mismatch` |
| completed | replay the original status and response body | typed `409 action_idempotency_mismatch` |

After the action returns, the daemon durably records the bounded HTTP status/body envelope and allowlisted `Content-Type`. If the response or completion write cannot be bounded or persisted, the original effect is not retried and the caller receives the same neutral `202` submitted response. A process restart leaves `pending` receipts pending; there is no automatic replay, expiry, epoch clearing, or cancellation operation.

The receipt is an Agenthail reservation/replay record. It does not make an external provider effect transactional and does not claim that a `submitted` action completed.
