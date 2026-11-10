# Queue snapshots and reconciliation

`Registry.ListQueue` and `QueueItem` are read-only snapshots. They query the
current persisted state and do not raise or withdraw delivery asks as a side
effect of a GET.

Delivery asks belong to writers: every queue and delivery mutation syncs them in
its own transaction, and the daemon's 10-second pass withdraws the ones that
aged out, such as a dead row past the review window. This keeps dashboard and
mobile GETs repeatable while a queue write shows its ask at once.

Catalog session projections carry the queue count captured by their producer.
The registry validates that count inside the same SQLite transaction that
persists the projection and its `session.upserted` event. A stale discovery or
queue-refresh projection is rejected and rebuilt from the transaction's actual
count; it must never overwrite a newer `session.queue` event with an older
count.
