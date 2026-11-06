# Delivery problem snapshot, dismissal and deletion

Snapshots expose at most the 50 newest undismissed delivery intents whose
status is `failed` or `held` as `deliveryProblems`. A failure leaves the list
once a later message from the same sender reached the same target (`sent` or
`delivered`), or once it is more than a day old; a held message stays until
its target releases or drops it. Every 10 seconds the daemon dismisses the
failures that left the list, with the same `delivery.dismissed` event a
person's dismissal records, so live clients drop them. The failed intent and
its queue row stay in delivery history and `agenthail queue list --all`. Each item contains the
numeric `deliveryId`, target and sender session IDs, bounded message, failure
reason, status, and the status update time in RFC3339 form. A failure that
never reached its session also carries `"deletable":true`; the field is
omitted otherwise.

A problem, its `delivery.problem` catalog event, and the sender's failure
notice are produced only when a provider rejects a submitted delivery or queued
work later fails. Deliveries the daemon runs (API, voice, queue)
publish the event to live catalog subscribers as soon as it commits; CLI
commands deliver in-process, so their problems reach subscribers on the
daemon's next queue scan, within its 5-second poll interval. Refusals that
happen before any provider effect (busy target with queuing disabled,
unwritable session, queue storage failure, or any adapter `delivery did not
start` error such as an unreachable app-server or steering an idle thread)
return their error synchronously, keep a history audit entry, and leave no
intent, problem, or notice.

Clients dismiss a problem with an authenticated `POST /api/v1/actions` body:

```json
{"action":"delivery-dismiss","deliveryId":123}
```

The registry records `dismissed_at` transactionally with one catalog event of
type `delivery.dismissed`, whose payload contains the numeric delivery ID.
Repeating the action is an idempotent no-op and never resends the original
message or creates another event. Invalid non-positive IDs are rejected.

## Deleting a message that never reached its session

A failure is deletable when its intent is `failed` and it failed while still
`submitted` (the provider refused the send) or `queued` (the queue row died
before any attempt left it unconfirmed). The registry records the status an
intent failed from in `delivery_intents.failed_from`. A failure from `sent`,
`unknown` or `held` may have reached the session and is never deletable;
neither is a `submitted` or `unknown` intent that has not failed.

### Failed send and steer replies

A `send` or `steer` action that fails after the daemon recorded its delivery
answers with the usual error body plus two top-level fields:

```json
{"error":{"code":"bad_gateway","message":"the desktop app closed"},"deliveryId":123,"deletable":true}
```

The status is `502 bad_gateway`, or `400 invalid_request` for a refusal of
kind invalid request. `deliveryId` and `deletable` always appear together.
A failure without them left nothing on the daemon (it was refused before
starting, or the request was invalid), so a client removes it locally.
`deletable:false` means the message may have reached the session: offer no
Delete.

### Delete

Clients delete with an authenticated `POST /api/v1/actions` body (control
scope; a person or a paired device, never an agent session):

```json
{"action":"delivery-delete","deliveryId":123}
```

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"deleted":true}` | removed |
| 200 | `{"ok":true,"deleted":false}` | already removed or dismissed, nothing changed |
| 400 | `invalid_request` | `deliveryId` missing or not positive |
| 403 | `forbidden` | the caller is an agent session |
| 404 | `not_found` | no such delivery |
| 409 | `conflict` | the delivery is not a deletable failure |

No error body of this action carries `deliveryId`. In one transaction the
daemon sets `dismissed_at`, cancels the dead queue row linked to the delivery,
so neither a queue retry nor a drain brings it back, and appends the same
`delivery.dismissed` catalog event a dismissal records, so every live client
drops the problem. The delivery history keeps the message and records the
canceled queue row.
