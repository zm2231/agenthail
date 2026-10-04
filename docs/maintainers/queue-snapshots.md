# Queue snapshots and reconciliation

`Registry.ListQueue`, `QueueItem`, and `ListAttentionItems` are read-only
snapshots. They query the current persisted state and do not expire messages,
create attention rows, or resolve attention rows as a side effect of a GET.

Expiry and attention reconciliation belong to writers: the daemon background
scan runs `ExpireMessages` followed by `ReconcileAttentionItems`, while queue
state producers reconcile after delivery failure, retry, cancel, claim timeout,
acknowledgement, and related transitions. This keeps dashboard and mobile GETs
repeatable while preserving immediate attention after a queue write.

Catalog session projections carry the queue count captured by their producer.
The registry validates that count inside the same SQLite transaction that
persists the projection and its `session.upserted` event. A stale discovery or
queue-refresh projection is rejected and rebuilt from the transaction's actual
count; it must never overwrite a newer `session.queue` event with an older
count.
