# Action idempotency contract

The authenticated action endpoints accept an optional `Idempotency-Key` header:

- `POST /api/v1/actions`
- `POST /api/action`

The key is scoped to the authenticated principal. A paired API device uses its stable registry device ID; the local dashboard uses the fixed local-dashboard principal. Authentication and the existing control/cookie guards run before any receipt lookup.

The daemon canonicalizes the decoded JSON request and stores only its SHA-256 hash. It stores no request body, bearer token, cookie, or raw secret. Keys are bounded to 128 bytes; the canonical request and replay response are bounded by the action request/receipt limits.

Reservation is durable and atomic. The first request inserts a `pending` receipt before invoking the action, and the daemon keeps the key in memory for as long as it runs the action. A later request with the same principal and key behaves as follows:

| Receipt | Same request hash | Different request hash |
| --- | --- | --- |
| pending, and this daemon is running it | `202` with `{ok:true,status:"submitted"}`; no action dispatch | typed `409 action_idempotency_mismatch` |
| pending, and no run holds it | `409 action_outcome_unknown`, kept as the receipt's outcome; no action dispatch | typed `409 action_idempotency_mismatch` |
| retry | runs the action again | typed `409 action_idempotency_mismatch` |
| completed | replay the original status and response body | typed `409 action_idempotency_mismatch` |

A pending receipt that no run holds belongs to a run that ended without recording its outcome: the daemon stopped mid-run, or the completion write failed. The action may have happened, so a resend learns that and is not run again. The answer says what to check (the session's transcript for a `send`) and that a new request needs a new key. The apps and the dashboard show it as an outcome that may have happened, as they do `host_outcome_unknown`, and keep the key.

After the action returns, the daemon records the bounded HTTP status/body envelope and allowlisted `Content-Type`. A reply larger than the receipt limit is recorded as its status with a short body, so a resend still learns whether it succeeded.

An answer that is not the action's outcome is recorded as `retry`, not completed: any `503`, which says the request was not served (such as `host_unreachable` or `ledger_owner_unreachable`), a forward to a paired host that did not answer (`504 host_outcome_unknown`), or one the host is still running (its own pending `202`). The next request with that key runs again. A forward records the host and the exact body it sent in the `retry` receipt, and the next request forwards that body to that host under the same key, so names or times that changed in between do not change the host's request hash. It goes to that host even if the action's target has moved since, because only that host holds a receipt for the key: a host that never got the request answers that the session moved, and the forward follows it. If that host is no longer paired, the answer is `409 action_outcome_unknown`. The host keeps its own receipt for the key, scoped to the forwarding host's device, so the forward replays the host's outcome, answers that it is still running, or runs once if the first forward never arrived.

Completed and retry receipts are deleted 30 days after their last update, in batches of 500 every 5 minutes. The apps retry a send on their own for an hour, but a send that stopped retrying can be retried by a tap at any time, and a paired host replays held sends and actions whenever the other host comes back. So a receipt has to outlast a long outage, not only the automatic retries. A held send or action that was first sent more than 29 days ago is not sent again: the host may have dropped its receipt by then, which would let the replay run twice. It is dead-lettered as unknown instead. A held send raises a delivery ask; a held action is recorded as failed and its sending session is told.

The receipt is an Agenthail reservation/replay record. It does not make an external provider effect transactional and does not claim that a `submitted` action completed.
