# Asks: agent and client contract

An ask is a decision an agent puts to the person, or to the agent it reports to. It is one object, stored in the `asks` table (`internal/registry/asks.go`), whichever way it was raised. The design and the later layers are in [asks-design.md](asks-design.md).

## Raising one

An agent raises an ask and then ends its turn; the answer arrives as its next message. Raising does not end the turn.

- MCP: `agenthail_ask {"title": "...", "questions": [...]}`, or `{"withdraw": "<ask id>"}`. It returns `{"ok": true, "askId", "routedTo"}`, where `routedTo` is `person` or the hearer's `@alias` or id.
- CLI: `agenthail ask "<title>" --option <label>[: <what it does>]... [--recommend <n>] [--link <title>=<url>] [--question <text> ...]`. The title is the first question unless `--question` follows it; options, a recommendation and links belong to the question before them. The CLI raises for the session it runs in (`AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` or `CODEX_THREAD_ID`).

Limits, refused as `invalid` with a message the agent can act on: a one-line title of at most 80 characters; 1 to 4 distinct questions of at most 160; 1 to 6 options per question, label at most 40, `does` at most 90; `recommended` an option index; at most 3 `http(s)` links per question.

## Routing

- A session an agent or coordinator started asks its hearer (`coordinator.PromptHearer`: the project lead, else its spawner) while that hearer runs. Any other session, a lead's own included, asks the person.
- The hearer gets one `[agenthail]` message naming the ask id and how to answer: `agenthail_answer {"requestId": "<ask id>", "answers": {...}}` or `agenthail ask answer <id> "<answer>"`, and how to pass it on: `decision: "escalate"` or `agenthail ask escalate <id>`.
- An ask moves one hop up (to its holder's own hearer, else the person) when the holder escalates it, when the holder stops running, or when it has sat with the holder for `askEscalateMinutes` (dashboard config, default 10). The daemon checks every 30 seconds. The holder's undelivered notice is withdrawn when the ask moves.
- Only the person or the session the ask is routed to may answer it. An answer is delivered once; a second is refused as stale.

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

- The single question reads `<reason>: "<message>"`, the message clipped to 120 characters. The recommended option is the first.
- A failure is deletable only when it failed while still `submitted` (the provider refused it) or `queued` (its queue row died before an attempt left it unconfirmed). A failure from `sent`, `unknown` or `held` may have reached the session and is never deletable.
- Routing follows the rule above, starting at the sender: an agent that sent the message is asked first, then its hearer, then the person. A message from the person, the CLI or a relay asks the person on the target's row. A relay source is never asked: its answer would start a turn the same relay forwards again.
- A failure stops asking after a day, or once a later message from the same sender reached the same target. A held message asks until its target releases or drops it. A dead queue row asks for the dead-letter review window. A refusal asks until an attempt is not refused; a later refusal raises a new ask.
- A message a host failed to relay to a paired host's session asks on the sending host. `respond` for it is answered there, not forwarded.

Answer with `respond`, `decision.choices` a one-element list of the option id (`{"choices": ["retry"]}`). An agent answers with `agenthail_answer {"requestId", "decision": "<option id>"}`.

| Choice | Effect |
|---|---|
| `retry` | the dead queue row is pending again |
| `delete` | the dead queue row is canceled and kept in history; it cannot be retried |
| `cancel` | the refused queue row is canceled |
| `dismiss` | nothing changes; the ask closes |

A queue row (`/api/v1/queue`, the dashboard queue) and an inbox message (`/api/v1/inbox`) carry `askId` while a delivery ask about them is open, so an app opens the ask from the row.

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

## The Stop reminder

`agenthail hook stop` (Claude host, Codex, and terminal Claude through `agenthail hooks install`) holds a stop once when the session is a lead (a project coordinator or the spawner of a running session), the turn's `last_assistant_message` has a line outside fenced code that ends in `?`, and the session raised no ask since its turn opened. The reason tells it to raise the question as an ask. A session holding Agenthail's tools is also asked, in the same hold, to call `agenthail_end_turn` when it has not. Terminal Claude's settings hook carries `--terminal` and does nothing under the Claude host, which runs its own.

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
- `actions` exist only for a single-question ask: an option button each when it has 4 or fewer options, plus `reply`.
- The decisions inbox (`/api/v1/decisions`) lists the same request under the session's `ask` item.

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
- It is withdrawn from the desktop and phones when answered or withdrawn.
- Every ask push carries `requestId`, the ask id, and `sessionId` as the pushing host routes it: `<host>::<id>` for an ask a paired host holds. A notification extension reads the ask from `/api/v1/requests?sessionId=` on that host and picks the request by id; it lists the options' ids, `actions`, `optionsOnly` and `deadline`.
- A browser notification for an ask shows the request's `actions` as buttons, except `reply`, up to the browser's limit (`Notification.maxActions`, often 2). A button posts `respond` with `{"choices": ["<id>"]}` from the dashboard's service worker; when that is refused or cannot be sent, it opens the session instead. Ask pushes go to browsers with high urgency.
- The turn that leaves an open person ask completes with outcome `needs_you` and `requestId` the ask id, and sends no completion push of its own.

## Command endpoint

`/api/v1/asks` serves the `agenthail` command on this machine only (the local dashboard token): `GET` lists open asks, `?id=` one ask, `?routedTo=<session>` those routed to or raised by a session; `POST {"op": "raise"|"withdraw"|"answer"|"escalate", "sessionId", "askId", "title", "questions", "note"}`, where `sessionId` is the caller.
