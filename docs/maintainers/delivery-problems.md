# Delivery problem snapshot and dismissal

Snapshots expose at most the 50 newest undismissed delivery intents whose
status is `failed` or `held` as `deliveryProblems`. A failure leaves the list
once a later message from the same sender reached the same target (`sent` or
`delivered`), or once it is more than a day old; a held message stays until
its target releases or drops it. Every 10 seconds the daemon dismisses the
failures that left the list, with the same `delivery.dismissed` event a
person's dismissal records, so live clients drop them. The failed intent and
its queue row stay in delivery history and `agenthail queue list --all`. Each item contains the
numeric `deliveryId`, target and sender session IDs, bounded message, failure
reason, status, and the status update time in RFC3339 form.

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
