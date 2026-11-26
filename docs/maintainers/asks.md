# Asks: agent and client contract

An ask is a decision an agent puts to the person, or to the agent it reports to. It is one object, stored in the `asks` table (`internal/registry/asks.go`), whichever way it was raised. The design and the later layers are in [asks-design.md](asks-design.md).

## Raising one

An agent raises an ask and then ends its turn; the answer arrives as its next message. Raising does not end the turn.

- MCP: `agenthail_ask {"title": "...", "questions": [...], "item": "<work item>", "default": <option index>, "defaultAfterMinutes": <n>}`, or `{"withdraw": "<ask id>"}`. It returns `{"ok": true, "askId", "routedTo"}`, where `routedTo` is `person` or the hearer's `@alias` or id, plus `workItem` when the ask is on a work item, `note` when it could not pick one, and `defaultAt` when it carries a default.
- CLI: `agenthail ask "<title>" --option <label>[: <what it does>]... [--recommend <n>] [--link <title>=<url>] [--question <text> ...] [--item <work item>] [--default <n> --default-after <minutes>]`. The title is the first question unless `--question` follows it; options, a recommendation and links belong to the question before them. The CLI raises for the session it runs in (`AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` or `CODEX_THREAD_ID`).

Limits, refused as `invalid` with a message the agent can act on: a one-line title of at most 80 characters; 1 to 4 distinct questions of at most 160; 1 to 6 options per question, label at most 40, `does` at most 90; `recommended` an option index; at most 3 `http(s)` links per question.

## Defaults

No default is the normal case: an ask waits for its answer however long that takes. An agent may offer one when an option is safe to take without the person.

- `default` is an option index (`--default <n>` counts from 1, as `--recommend` does) and `defaultAfterMinutes` is 15 to 10,080; both or neither, and only on an ask with one question. The question carries the option's id as `default`, and the ask carries `defaultAt`.
- A default whose label or `does` reads as an outward act (sending, merging, deploying, paying, deleting: the word list ledger questions use) is refused: `an outward default; leave default out so the person answers`.
- An answer before `defaultAt` settles the ask as usual. At `defaultAt` the ask sweep, every 30 seconds, expires an open ask (`state` `expired`) and the session gets one message: `[agenthail] No answer by <time>; the default applies: <label>.` A daemon stopped between the two still sends it on a later sweep, within a day. The item's question, if the ask is on one, is resolved as `defaulted` with that message as its answer.

## Asks on held work

An ask from a session that holds work is about one work item, and the ledger records it there as a question.

- `item` names it, and must be an item the session holds `in_progress` or `blocked`; any other is refused as `invalid`. Without `item`, the ask is on the session's only item in progress. A session holding none raises a plain ask. With several in progress and none named, the ask is plain too, and the reply's `note` says to name one.
- The ask row keeps `work_item`, `ledger_key` and, once recorded, `ledger_question`. The ledger gets two daemon-only events, `work_item.question` (id prefix `IQ`, issued by the ledger's owner) and `work_item.question.resolved`. They carry the ask's title and questions, then how it ended: `answered`, `withdrawn` or `defaulted`, with the answer and who gave it. Agents cannot write them: a paired host sends them through the host-only `/api/v1/ledger/item-question`.
- The ask goes out at once whether or not the ledger is reachable. The daemon records the question whenever an ask changes and every 30 seconds, through the ledger's owner when a paired host keeps it. Each write carries a ref made from the ask (`item-question:<host>:<ask>`, `item-question-resolved:<host>:<ask>`), and the owner answers a ref it already holds with the question it issued the first time. So a retry after an outage or a lost reply records one question, resolved once. If the item was deleted before its question was recorded, the daemon stops trying and the ask stays a plain ask.
- The item shows the question until the ask ends, whoever holds the item by then. While the ask is open and with the person, the item waits on `person` and is in the board's Waiting on you lane, from the moment the ask is raised, before its question is recorded and through a handoff. An ask the lead holds does not mark it. In `/api/v1/decisions` the ask carries `workItem`, `workTitle` and `ledgerKey`.

## Routing

- A session an agent or coordinator started asks its hearer (`coordinator.PromptHearer`: the project lead, else its spawner) while that hearer runs. Any other session, a lead's own included, asks the person.
- The hearer gets one `[agenthail]` message naming the ask id and how to answer: `agenthail_answer {"requestId": "<ask id>", "answers": {...}}` or `agenthail ask answer <id> "<answer>"`, and how to pass it on: `decision: "escalate"` or `agenthail ask escalate <id>`.
- An ask moves one hop up (to its holder's own hearer, else the person) when the holder escalates it, when the holder stops running, or when it has sat with the holder for `askEscalateMinutes` (dashboard config, default 10). The daemon checks every 30 seconds. The holder's undelivered notice is withdrawn when the ask moves. A delivery ask moves only when its holder passes it on (see [Delivery problems](#delivery-problems)).
- A worker's turn that ends waiting on its lead, without an ask, moves to the person the same way, with its own windows ([turn judge](turn-judge.md#a-workers-wait-on-its-lead)). The ledger's end-of-turn notice tells a worker with open work and no progress to raise an ask if it waits on its lead.
- Only the person or the session the ask is routed to may answer it. An answer is delivered once; a second is refused as stale. A paired host answers for the session it names in `sourceSessionId`, from `agenthail_answer` or the CLI in that session's shell; the host that holds the ask checks that it is routed to that session and records the answer as that session's. On the same host, `agenthail answer` in an agent's shell names its session the way `send` does (`AGENTHAIL_SESSION_ID`, then `CLAUDE_CODE_SESSION_ID`, then `CODEX_THREAD_ID`), and the answer is that session's under the same check: an agent's answer is never recorded as the person's.

## Permission requests

A provider request the session waits on (a permission prompt, a question, MCP input) is an ask of origin `permission`; the request it holds is its `ref`, which stays internal. The daemon opens one when it observes the request and withdraws it when the provider closes it, so a restart keeps every open request.

- Its id is the request id everywhere: `waitingOn.id` and `waitingOn.open`, the ids `/api/v1/requests` and `agenthail_requests` list, a notification's `requestId`, and `respond`. A provider's own request id is never accepted.
- An answerable request follows the routing above, with the window `askPermissionEscalateMinutes` (default 3): it holds a tool call. The hearer's notice names the ask id, the command to read it and the decisions to answer it with. The person is notified only once it reaches them.
- A wait Agenthail cannot answer (a dialog in the terminal, a prompt it cannot reach) goes straight to the person, with no buttons anywhere; the notification says where to answer it. It is withdrawn when the wait ends.
- `waitingOn` is derived from the open permission asks: the oldest, with every open answerable one in `open`. It is not the row's `ask`, which stays an agent's ask.
- The decisions inbox shows a request routed to a running lead under "Handled by a lead"; the person can still answer it.

## Delivery problems

A message that did not reach its session, or whose arrival the daemon cannot confirm, is an ask of origin `delivery`. The daemon raises and withdraws these itself, in the same transaction as the delivery change, so there is nothing to dismiss elsewhere.

| Problem | Title | Options |
|---|---|---|
| The provider rejected a send, or queued work failed | Message not delivered | `retry` (only while its queue row is dead), `delete` (only when it never reached the session), `dismiss` |
| The target's Claude holds the message | Message held by its target | `dismiss` |
| A queued message whose delivery could not be confirmed | Message not confirmed | `retry`, `dismiss` |
| The target refused to start (an untrusted workspace, a missing settings file) | Target refused to start | `cancel`, `dismiss` |
| A queued message still deferred 15 minutes after its first deferral | Message not delivered yet | `cancel`, `dismiss` |
| The target's Claude conversation is open in several terminals | Message held: open in N terminals | `pid-<pid>` for each terminal, `cancel`, `dismiss` |

- The single question reads `<reason>: "<message>"`, the message clipped to 120 characters. The recommended option is the first.
- A message held among several terminals offers each one by its terminal program, terminal device, folder and pid, as far as they are known; the recommended option is `dismiss`, so an unanswered ask leaves it held. Its ref names the set of terminals, so a change in who holds the conversation raises a new ask. Each terminal is the process that held the conversation when the message was held: a pick never reaches a later process that reused its pid.
- A failure is deletable only when it failed while still `submitted` (the provider refused it) or `queued` (its queue row died before an attempt left it unconfirmed). A failure from `sent`, `unknown` or `held` may have reached the session and is never deletable.
- Only the sender decides. An agent that sent the message is asked, as a notice to it, and the ask stays with it until it answers or passes it on (`escalate`); it is not escalated after the window or when the agent stops. A message the person sent, from the app or the CLI, asks the person on the target's row. Nobody is asked about a relayed message (its source never sent it, and an answer would start a turn the same relay forwards again), one from a sender with no session on this host, or Agenthail's own notices. On start and on every sync, an open delivery ask that this rule no longer raises, or that Agenthail moved off the agent that sent its message, is withdrawn; one its holder passed on stays. The agent is asked again about its own.
- A failure stops asking after a day, or once a later message from the same sender reached the same target. A failure an earlier release recorded because no turn read the message within an hour never asks: the message reached its session. A held message asks until its target releases or drops it. A dead queue row asks for the dead-letter review window. A refusal asks until an attempt is not refused; a later refusal raises a new ask. A deferred message asks until it is delivered, canceled or fails; its later attempts do not raise it again, and once dismissed it does not ask again.
- A message a host failed to relay to a paired host's session asks on the sending host. `respond` for it is answered there, not forwarded.

Answer with `respond`, `decision.choices` a one-element list of the option id (`{"choices": ["retry"]}`). An agent answers with `agenthail_answer {"requestId", "decision": "<option id>"}`.

| Choice | Effect |
|---|---|
| `retry` | the dead queue row is pending again |
| `delete` | the dead queue row is canceled and kept in history; it cannot be retried |
| `cancel` | the refused or deferred queue row is canceled; a sender that passed the ask on is told |
| `pid-<pid>` | the held queue row is sent now to that terminal alone, after a restart too; if it no longer holds the conversation, the row is held again among the terminals that do, with a new ask |
| `dismiss` | nothing changes; the ask closes |

A queue row (`/api/v1/queue`, the dashboard queue) and an inbox message (`/api/v1/inbox`) carry `askId` while a delivery ask about them is open, so an app opens the ask from the row.

An answer to a delivery ask that is no longer open is `stale` (409), and its reply also carries `settled`: the id of the option that settled it (`retry`, `delete`, `dismiss` or `cancel`), or `withdrawn`. It is absent when the ask was reopened because the other answer could not apply. An app holding the message as not sent removes it on Delete unless it was retried, in which case it says the message is being sent again; without `settled` it keeps the message.

A choice the ask does not offer is `400 invalid_request`. A choice that can no longer apply (the row was already retried or canceled) is `409 conflict` and records the failure as the ask's apply status. `queue-cancel` refuses a dead row with `409 conflict`; answer its ask. There is no queue retry action.

A `send` or `steer` that fails after the daemon recorded its delivery answers with the error body plus the ask to answer:

```json
{"error":{"code":"bad_gateway","message":"the desktop app closed"},"askId":"ask-0fca204fdeb3"}
```

The status is `502 bad_gateway`, or `400 invalid_request` for a refusal of kind invalid request. A failure without `askId` left nothing on the daemon, so a client removes it locally.

## Ledger questions

A question in the intent ledger (`agenthail ledger question`) is an ask of origin `ledger` on the asking session's row. Only the person may answer it: the ledger takes an answer only from the person's own words, so it goes straight to the person, never to a hearer, and any session, one acting for the person included, is refused with `403 forbidden`.

- The host that runs the asking session holds the ask. It reconciles each project's questions whenever its copy of the ledger changes, and every 30 seconds. A question asked by a session on a paired host is asked there.
- The title is `<question id>: <question>`. The single question offers `recommended` (the recommendation, and the recommended option) and `default` (the default answer, left out when it is the recommendation). It takes typed answers and a note, as an agent's ask does. The ask carries the question's `deadline`.
- Answering records the person's words in the ledger as an utterance captured by `ask` and attaches them to the question as its answer, through the project's owner when another host keeps its ledger. A picked option is recorded as its full text, a note after the answer. The asking session gets one message: `[intent ledger] The person answered <id> ("<question>"): <words>`. An answer the ledger refuses, or whose owner cannot be reached, reopens the ask and returns the error: `409 conflict`, or the owner's `503` (unreachable) or `504` (unconfirmed).
- A question answered another way (`agenthail ledger attach --as answers`) withdraws its ask.
- A voice answer is the person's transcribed speech, so when the voice path answers ledger asks it must reach the ledger as a person capture, not as a session's words. The 403 is for agent sessions only.
- At the deadline, a default that applies itself expires the ask (`state` `expired`), and the asking session gets one message naming the default. A default that would narrow the work, and an `outward` question, leave the ask open with the person; nothing is announced again.

## Approvals

An agent refused an action that is the person's (`detail.rule: person_only`, `detail.approvable: true`, [mcp.md](mcp.md#the-cli-from-an-agents-shell)) asks for it by sending the same request again with `askPerson: true` and a `reason`; the CLI does this when the command is run again with `--ask-person [--reason "..."]`. That raises an ask of origin `approval` on the asking session's row, routed to the person only (`approvals.go`, `person_actions.go`).

- The `approvals` table keeps the exact body the command would have sent (`api` `action` or `settings`), its sha256, the daemon's own description of it, the agent's reason, and when it was decided, by whom, and what running it returned. Rows outlive the session, as the record of what was allowed.
- The title is the description, clipped to 80 characters; cards and notices put the asking session's name before it (`<name> asks: <title>`), so the title never repeats it. The one question is the description and `. Why: <reason>`. The description is the daemon's wording of the body, never the agent's, so an agent cannot label one action as another; a body the daemon cannot describe cannot be asked for (`400 invalid`).
- Asking again with the same body while one is open returns the open ask instead of a second.
- It offers `allow` (Allow once) and `deny` (Deny), `optionsOnly`. Only the person answers it: any session, one acting for the person included, is refused with `403 forbidden`.
- Allow runs the stored body once, with the answering person's reach: an action through the same path as `POST /api/v1/actions`, a setting through `/api/v1/settings`. The asking session gets one message: `[agenthail] The person allowed: <description> (ask <id>). It ran: <status> <body>`, or `It failed:` with the error. The ask id keeps two results of the same action distinct. The question is the description and `. Why: <reason>`, clipped to 160 characters with the description first. When it is clipped, the question's `detail` carries the whole text, up to 2000 characters, and the dashboard and the apps show it under the question. Its notifications carry no answer buttons, so it is answered only once opened, after the person can read every name. A second answer is `409 stale`. Deny runs nothing and sends `[agenthail] The person denied: <description> (ask <id>).`
- A send, steer, interrupt, archive or unarchive refused because its target is in another project ([mcp.md](mcp.md#control-scope)) is asked for the same way. The daemon rebuilds the body from that action's fields alone: the target, the message, `busyDelivery` for a send, and the asking session as `sourceSessionId`. Whatever else the request carried is dropped, and the scope is checked again from the rebuilt body. Run with the person's reach, an allowed message is still the agent's: its sender is the asking session, not the person.
- An approval is answered within 24 hours or expires (`state` `expired`); the agent is told it was not done.
- An allowed body the daemon had not run when it stopped is never run later: at start it is recorded `not_run` and the agent is told to ask again.

## Provider questions

Some providers ask through a tool that holds nothing open. Codex's `request_user_input_async` returns `{"accepted":true}` at once, records its questions on an agent message delivered `async`, and lets the turn go on or end. The answer reaches Codex as a later user message in its reply envelope:

```
<send_user_message_question_reply>
[{"answer":"Keep both","question":"Which database should the service use?","questionItemId":"[\"request_user_input_async\",\"call_q1\",0]"}]
</send_user_message_question_reply>
```

Such a question is an ask of origin `question` on the session's row; `ref` is `<thread>:<item>` and stays internal. One item asking several questions is one ask with several questions.

- The daemon raises it when an observation finds the question among the thread's newest 100 items (`thread/items/list`, newest first, across turns), routed like an agent's ask. A question is raised once: an ask answered, withdrawn or dismissed is not raised again. While it is open the session stays on the observer's watch list, so an idle thread is still read.
- It shows like an agent's ask: the card, the inbox, the ask on the row and on the session detail, `turnState` `waiting_person`, the header "Waiting on you" while the person holds it, and an `ask.opened` push. The turn that asked stays quiet.
- Answering it needs every question answered. It composes one envelope, one entry per question, each echoing the question's full text cut to 512 bytes with line breaks as spaces, and folds any note into the last answer so the message stays a bare envelope Codex parses. The person's answer is delivered as the person's message; a lead's is delivered unlabeled, since a `[from …]` prefix would break the envelope. A turn still running takes it as a steer, as Codex's own clients do.
- The ask is withdrawn when the thread shows every question answered by a reply envelope from another client, or when the person starts a later turn with anything else: Codex takes that turn as the answer. A reply counts only as Codex's parser reads it: complete, naming the exact question id. A steer into the turn that asked leaves the ask open, and so do messages Agenthail or another agent wrote (`[agenthail]`, `[from …]`).

## A question asked only in the reply

Nothing holds a turn whose reply asks the person something no ask carries. The turn judge reads that reply as waiting on the person, and the turn pushes Needs you ([turn judge](turn-judge.md#the-push)).

## Seeing an ask

Session rows (session view, snapshot, catalog stream, mirrored hosts) carry `ask`:

```json
"ask": {"id": "ask-447a922cae82", "title": "Ready to ship", "routedTo": "person", "askedAt": "2026-11-10T06:26:27.31Z"}
```

```json
"ask": {"id": "ask-447a922cae82", "title": "Ready to ship", "routedTo": "person", "askedAt": "2026-11-10T06:26:27.31Z", "answeredAt": "2026-11-10T06:30:02.5Z"}
```

- The row shows its oldest open ask; with none open, its newest answered ask until the session's next turn opens; otherwise no `ask` key.
- `routedTo` is `person` or a session id. Only `person` asks are the person's to answer: they set `attention {"kind": "ask", "reason": <title>}` and put the row in Needs you.
- `answeredAt` is absent while the ask is open. An answered ask must not be offered again.
- `deadline` is present on a ledger question's ask: when its default applies or it waits on the person's ack. The request from `/api/v1/requests` carries it too.
- `endTurn` no longer carries asks: it is `{headline, next, endedAt}`.

Fetch the card when `ask.routedTo` is `person`, `answeredAt` is absent and the session is not read-only, whatever `capabilities.respond` says. Key any cache on the session id, `waitingOn.id` and `ask.id`.

## The request

`GET /api/v1/requests?sessionId=<id>` lists each open person ask after any provider requests:

```json
{
  "id": "ask-447a922cae82",
  "kind": "ask",
  "tool": "",
  "title": "Ready to ship",
  "prompt": "Ready to ship",
  "questions": [
    {"question": "Deploy it now?", "header": "", "multiSelect": false,
     "options": [{"id": "o0", "label": "Deploy", "description": ""}, {"id": "o1", "label": "Wait", "description": "hold until Monday"}],
     "suggested": 0,
     "links": [{"title": "Runbook", "url": "https://example.com/runbook"}]}
  ],
  "actions": [{"id": "o0", "title": "Deploy"}, {"id": "o1", "title": "Wait"}, {"id": "reply", "title": "Reply…"}],
  "alwaysAllow": false,
  "createdAt": "2026-11-10T06:26:27.31Z"
}
```

- Every option of every request carries an `id`, unique within the request: `o0`, `o1`… for a request with one question, `q0o0`, `q1o2`… with several, a delivery ask's `retry`, `delete`, `cancel` or `dismiss`. Answer with ids, not labels.
- `suggested` is the index of the recommended option; mark it. `description` is the option's `does`.
- `links` open outside the app.
- `detail` is what a tool request asks to do, as `agenthail_requests` gives it: the command for a shell, the path and a summary of the change for an edit, the tool input otherwise, cut on a character at 2 KiB. Questions and input forms carry their own text and have none.
- `actions` exist only for a single-question ask: an option button each when it has 4 or fewer options, plus `reply`.
- When the session's provider cannot be read (its Codex is not running, its terminal stopped), the reply is still `200`: it lists every ask Agenthail holds for the session, no provider requests, and the provider's error in `providerError`. An approval or a delivery problem is usually raised for a session that is offline, so it must show then. `agenthail_requests` carries the same field.
- The decisions inbox (`/api/v1/decisions`) lists the same request under the session's `ask` item.
- Work parked on the person's decision is its own item there: `kind: "parked"`, `group: "waiting"`, `answerable: false`, with the work item's id in `workItem` and the session that parked it in `sessionId`. Clients key items by `sessionId` and `workItem` together, because one session can have both its own item and parked ones.

## The card

Render it with the question pager, as for `kind: "question"`, except:

- Heading: the session's display name, "asks:", then `title`.
- Never send on one tap; a pick waits for Submit.
- An optional note: on the page for a single question, on the review step for several.
- No Decline. The person can leave the ask open.

## Answering

Post `respond` with an `Idempotency-Key`:

```json
{"action": "respond", "sessionId": "<id>", "requestId": "ask-447a922cae82",
 "decision": {"choices": ["o1"], "message": "After lunch."}}
```

`decision` has four fields and no others; any other field, such as `behavior` or `always`, is `400 invalid_request`. Every request is answered this way, whatever raised it.

- `choices`: ids the request lists. For a question, one option id per question, or several for a multi-select question; the session receives the labels, a multi-select question's joined with ", ". A one-question ask sends a one-element list.
- `answers`: a question answered in the person's own words, keyed by the question's text, and left out of `choices`.
- `message`: an ask's note, a denial's reason, or the text of a `reply`. Omit it when empty.
- `content`: an MCP server's form values, with `{"choices": ["allow"]}`.

By kind of request:

| Request | `decision` |
|---|---|
| Approval | `{"choices": ["allow"]}`, or `always`, `deny`, `never`, `stop`, each where `actions` lists it; `{"choices": ["reply"], "message": "<reason>"}` denies with a reason |
| Provider question | the option ids from `/api/v1/requests` (every option carries one), typed answers in `answers`; every question needs an answer. `{"choices": ["deny"]}` declines it |
| Agent ask | as a provider question, but questions may be left unanswered as long as something is answered or a note written |
| Ledger question | `{"choices": ["recommended"]}` or `["default"]`, or the person's words in `answers`, with an optional note in `message`; `deny` is refused (400) |
| Refused action | `{"choices": ["allow"]}` or `["deny"]`; it has `optionsOnly: true` and only the person answers it (403 for any session) |
| Delivery problem | one of its option ids; it has `optionsOnly: true`, so it takes no typed answer, note or content (400) and lists no `reply` action; the card shows neither Other nor Note |
| MCP server input | `{"choices": ["allow"], "content": {<field values>}}` (the content is unchanged), `{"choices": ["allow"]}` for a link or an empty form, or `{"choices": ["deny"]}` |

An agent the ask was routed to passes it on with `{"choices": ["escalate"]}`. An id the request does not offer, two for a single-choice question, or a provider question left unanswered is `400 invalid_request`.

The session receives one message, from the person (or from the answering agent):

```
1. Deploy it now? Wait

After lunch.
```

| Status | Body | Meaning |
|---|---|---|
| 200 | `{"ok": true, "result": {"requestId", "outcome": "answered", "receipt": {...}}}` | sent; the row's `ask.answeredAt` is set |
| 200 | the same body again | a retry with the same `Idempotency-Key` and body |
| 409 | code `stale` | already answered or withdrawn; drop the card |
| 400 | code `invalid_request` | nothing answered and no note, or a button the ask does not offer |
| 403 | code `forbidden` | an agent the ask is not routed to |
| 502 | code `bad_gateway` | delivery failed. If the message says it "may have reached the session", the ask stays answered and must not be retried; otherwise it is open again |

Refetch the row and requests after any response.

## Notifications

- A person ask is announced when it is raised or escalated to the person: "<session> needs you: <title>", event `ask.opened`, `requestId` the ask id, collapse key `ask-<hash>`, and buttons from the request's `actions`. A paired viewer announces a mirrored host's ask under the same dedupe key, so a phone paired to both shows it once.
- It is withdrawn from the desktop and phones when answered, withdrawn, expired or deleted. For an ask the person may have been shown (announced, or ever routed to the person), triggers on `asks` queue the withdraw in `notification_withdrawals` in the same write that closes it, whichever path closes it; a paired host's ask is queued by the viewer that announced it. A queued withdraw leaves the table once the desktop and every device were tried and it was not queued again meanwhile. The daemon sends the queue when it starts, after delivery syncs and on each ask sweep, so a withdraw cut off by a stop is sent after the restart. An ask that closed while its announcement was on its way is withdrawn again once the announcement finishes.
- Every ask push carries `requestId`, the ask id, and `sessionId` as the pushing host routes it: `<host>::<id>` for an ask a paired host holds. A notification extension reads the ask from `/api/v1/requests?sessionId=` on that host and picks the request by id; it lists the options' ids, `actions`, `optionsOnly` and `deadline`.
- A browser notification for an ask shows the request's `actions` as buttons, except `reply`, up to the browser's limit (`Notification.maxActions`, often 2). A button posts `respond` with `{"choices": ["<id>"]}` from the dashboard's service worker; when that is refused or cannot be sent, it opens the session instead. Ask pushes go to browsers with high urgency.
- The turn that leaves an open person ask completes with outcome `needs_you` and `requestId` the ask id, and sends no completion push of its own.

## Command endpoint

`/api/v1/asks` serves the `agenthail` command on this machine only (the local dashboard token): `GET` lists open asks, `?id=` one ask, `?routedTo=<session>` those routed to or raised by a session; `POST {"op": "raise"|"withdraw"|"answer"|"escalate", "sessionId", "askId", "title", "questions", "note"}`, where `sessionId` is the caller.
