# Inbox: what a session was sent

A session's inbox is every message sent to it, whether it was delivered at once or waited in the queue. Each message has one row in `delivery_intents`, and the inbox reads only that table. Two other tables hold related records but answer different questions:

| Table | One row per | Read by |
| --- | --- | --- |
| `delivery_intents` | message, sender to target, with its delivery status | `GET /api/v1/inbox` |
| `message_queue` | message or command still waiting or finished, with its retry state | `GET /api/v1/queue` (rows that can still be acted on; `projectId`, or `cwd` for the project that holds an absolute folder, keeps one project's targets), Settings > Delivery, retry and cancel |
| `delivery_history` | delivery attempt or outcome, capped at 2000 rows | `GET /api/v1/history`, Audit |

## Recording

The dispatcher records an intent before it tries a send (`prepareIntent` in `internal/delivery/delivery.go`), so a message delivered at once has one even though it never gets a queue row.

Every queued message row is written by `enqueueMessageTx` (`internal/registry/registry_queue_enqueue.go`), which writes the row's intent in the same transaction or binds the submitted intent it is given. `TestOnlyTheQueueChokepointWritesMessageRows` fails if non-test code inserts into `message_queue` anywhere else. Its allowlist is `QueueGoal` and `queueCompactTx`, which queue commands rather than messages, and `queueHostDeliveryTx`, which holds a send for a session another host owns: that host records the intent when the send reaches it.

An intent's sender is the agent that sent the message, or one of Agenthail's own senders, which a session cannot message:

- `agenthail-person`: the person, from any of their apps;
- `agenthail-cli`: `agenthail send`, a script or an API call;
- `agenthail`: Agenthail itself, for its notices and ledger turn-gate notices, Claude peer messages queued for the target, and any sender that is not a registered session.

Failure, refusal and held notices go only to agent senders (`readsNotices`), so a message from Agenthail itself never starts another notice. The session a notice is about stays in the queue row's `source_session_id`, and the inbox shows it as the item's source.

## Reading

`GET /api/v1/inbox[?sessionId=<id>][&before=<id>][&limit=<n>]` lists intents newest first: 50 by default, at most 200. `nextBefore` is the cursor for the next page. A `sessionId` routes to the host that owns the session. Without one the list covers this host's sessions only, so a client with several hosts asks each.

The iPhone inbox has two tabs. Current reads `GET /api/v1/queue`, which lists what can still be acted on and needs no paging. History reads this endpoint, reloads its first page every 5 seconds while it is on screen, and leaves out what Current shows. A host too old to serve this endpoint is named in the tab, and the other hosts still list.

A message that waited carries its `queueId`, its queue row's `queueStatus`, `attempts` and `lastError`, and its full text from the queue row. `queueStatus` is `removed` when the row no longer exists.

An item is `pending` while its intent is `submitted`, `queued` or `held` and its queue row, if it has one, is not `canceled`, `delivered` or `removed`. A cancel updates only the queue row, so the inbox settles the item from it: a canceled row reads `canceled` and a removed one `unknown`. The intent keeps its status, so a cancel raises no delivery problem. Otherwise the text is the intent's copy, which keeps the first 16 KiB; past that `truncated` is true, and the whole message is the turn in the target session's transcript.

Intents are not pruned.
