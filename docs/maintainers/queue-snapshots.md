# Queue snapshots and reconciliation

`Registry.ListQueue`, `QueueItem`, and `ListAttentionItems` are read-only
snapshots. They query the current persisted state and do not expire messages,
create attention rows, or resolve attention rows as a side effect of a GET.

Expiry and attention reconciliation belong to writers: the daemon background
scan runs `ExpireMessages` followed by `ReconcileAttentionItems`, while queue
state producers reconcile after delivery failure, retry, cancel, claim timeout,
acknowledgement, and related transitions. This keeps dashboard and mobile GETs
repeatable while preserving immediate attention after a queue write.
