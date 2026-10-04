# Delivery problem snapshot and dismissal

Snapshots expose at most the 50 newest undismissed delivery intents whose
status is `failed` or `expired` as `deliveryProblems`. Each item contains the
numeric `deliveryId`, target and sender session IDs, bounded message, failure
reason, status, and the status update time in RFC3339 form.

A problem, its `delivery.problem` catalog event, and the sender's failure
notice are produced only when a provider rejects a submitted delivery or queued
work later fails or expires. The daemon publishes the event to live catalog
subscribers as soon as it commits. Refusals that happen before any provider
effect (busy target with queuing disabled, unwritable session, queue storage
failure) return their error synchronously, keep a history audit entry, and
leave no intent, problem, or notice.

Clients dismiss a problem with an authenticated `POST /api/v1/actions` body:

```json
{"action":"delivery-dismiss","deliveryId":123}
```

The registry records `dismissed_at` transactionally with one catalog event of
type `delivery.dismissed`, whose payload contains the numeric delivery ID.
Repeating the action is an idempotent no-op and never resends the original
message or creates another event. Invalid non-positive IDs are rejected.
