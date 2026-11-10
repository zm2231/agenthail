# Asks: one object for everything that needs the person (design)

Status: layer 1 built (schema 90); layers 2 to 5 are design. "Why" and the table below describe the code before layer 1, at `fe198bfd` (schema 89).

## Why

Since 2026-11-07 there were 386 turn ends and 3 with `needs: "you"`, all from host-run Claude workers routing to their lead. The sessions that owe the person decisions are leads, and leads mostly run in a terminal (Claude) or in Codex. They never reach Needs you:

- `agenthail_end_turn` is the only way an agent raises a question, and only sessions holding an MCP credential have it. The Stop hook nudges a session to call it only when it holds a live credential (`internal/cli/turn_stop.go:45`).
- Terminal Claude has no Stop hook at all; settings.json installs PermissionRequest, SessionStart and UserPromptSubmit only (`internal/claudehooks/hooks.go:27-30`). The claude-host Stop hook is registered in code (`claude-host/host.mjs:1028-1043`).

Meanwhile four mechanisms each carry "the person must decide something", with different storage, routing, answer paths and notifications:

| Today | Stored in | Routed to | Answered by | Notified |
|---|---|---|---|---|
| End-turn asks | `turn_ends.asks_json`, `routed_to`, `answer_*` (`internal/registry/turn_ends.go:11-43`) | lead **or** person (`coordinator.PromptHearer`, `internal/coordinator/lead.go:29`) | person only, `respond` with `turn-end-<ms>`; agents get 403 (`internal/daemon/dashboard_session_actions.go:218-221`). A routed ask is never marked answered and `agenthail_wait` can't see it. | `turn.completed` push with buttons; never withdrawn |
| Permission requests | provider (claude-host `pending`, Codex server requests `internal/surface/surfaces/codex_requests.go:358,392`); only the oldest is persisted in `session_runtime.waiting_*`, the rest in memory (`internal/daemon/open_requests.go:10-20`) | lead **and** person at once (`internal/daemon/waiting.go:183-264`, `lead_notice.go` `noticeWaiting`) | `respond` / `agenthail_answer`; provider vocabularies differ (Claude allow/always/deny + reason, Codex allow/always/never/stop, no reason) | `session.waiting` push with buttons, withdrawn |
| Delivery problems | `delivery_intents` failed/held, `attention_items` | sender agent gets a notice; person sees banners | `delivery-dismiss`, `delivery-delete`, `queue-retry`, `queue-cancel` | none, except `delivery.refused` (`internal/daemon/outbox.go:315`) |
| Ledger questions | `ledger_events` `question*` (`internal/ledger/board_questions.go:8-33`); recommendation + default + deadline, no options | asking session only | agent attach `--as answers` with the person's words; no device UI | only at expiry, no request id (`internal/daemon/ledger_questions.go:76-86`) |

The clients already have one inbox: `GET /api/v1/decisions` (`internal/daemon/decisions.go:62-117`) groups the daemon's Needs-you rows into Stopped on you / Holding up work / Waiting on you, with "Accept recommended" on Mac and the dashboard (`native/macOS/DecisionInboxPane.swift:52-54`, `internal/daemon/dashboard/js/decision-card.js:160-173`). It reaches answers through each source's own request list, which is why ledger questions and routed end-turn asks are missing from it.

## Decisions taken

(a) One **ask** replaces the four. (b) Every session can raise one, through `agenthail ask` and the MCP tool, both backed by the same object; the Stop hook reminds a lead whose reply asks the person something and that raised none. Nothing infers asks with a model. (c) Leave room for a later path where an agent calls the person and the open ask is read aloud; not built now.

## The ask

```json
{
  "id": "ask-01JC…",
  "sessionId": "…",            // whose work waits on the answer
  "projectId": "…",
  "origin": "agent",           // agent | permission | delivery | ledger
  "ref": "…",                  // the source's own id: provider request, delivery intent, ledger Q-id; unique per origin
  "title": "Ready to ship",
  "questions": [{
    "question": "Deploy it now?",
    "options": [{"id": "o0", "label": "Deploy", "does": ""}, {"id": "o1", "label": "Wait", "does": "hold until Monday"}],
    "recommended": "o0",
    "multiSelect": false,
    "freeText": true,
    "links": [{"title": "Runbook", "url": "https://…"}]
  }],
  "routedTo": "person",        // "person" or a session id
  "route": [{"to": "<lead id>", "at": "…", "why": "raised"}, {"to": "person", "at": "…", "why": "escalated"}],
  "deadline": "…",             // optional; ledger questions
  "onDeadline": "o0",          // optional option id applied at the deadline; absent means escalate
  "state": "open",             // open | answered | withdrawn | expired
  "answer": {"choices": ["o0"], "text": ""},
  "answeredBy": "person",      // person | session id
  "answeredVia": "iphone",     // mac | iphone | dashboard | notification | cli | mcp | voice
  "answeredAt": "…",
  "applied": "…",              // when the answer reached the source; applyError if it could not
  "createdAt": "…"
}
```

Limits are the ones `agenthail_end_turn` had (`validAsk`, `internal/daemon/asks.go`): 1–4 questions, question ≤160 characters, 1–6 options, label ≤40, does ≤90, ≤3 links. Short labels are also what makes an ask speakable later.

Every origin maps onto this shape:

- **agent**: what the session wrote.
- **permission**: one question; options are the provider's actions (`internal/surface/request_actions.go:21-94`): allow, always, deny, never, stop, with `freeText` only where the provider takes a reason (Claude yes, Codex `NoReason`). AskUserQuestion and Codex `requestUserInput` keep their own questions and options. `recommended` is the provider's suggestion where it gives one, else none.
- **delivery**: "Message to X could not be delivered: <reason>", options Retry / Delete / Dismiss (Cancel for an incoming dead queue row).
- **ledger**: the question text; options are the recommendation (recommended) and the default when it differs, plus free text. `deadline` and `onDeadline` come from the question.

### Answering

One action, the existing `respond {sessionId, requestId: <ask id>, decision}` with an `Idempotency-Key`, on `/api/v1/actions` (devices, dashboard), MCP `agenthail_answer {requestId: <ask id>}` and `agenthail ask answer` (agents); clients answer an ask as they answer a question. The daemon claims the ask (open to answered, at most once), then an **applier** per origin delivers the answer:

| origin | applier |
|---|---|
| agent | composes one numbered message (`composeAskAnswer`, `internal/daemon/asks.go`) and delivers it to the session as the person, or as the answering lead |
| permission | `Responder.Respond` with the chosen action mapped to the provider's `Decision` (`internal/daemon/requests.go:93-131`) |
| delivery | retry / delete / dismiss / cancel, as the four actions do now |
| ledger | records the person's words as an utterance and attaches it `As=answers` to the question (`internal/ledger/attach_answer.go:8-37`) |

An apply failure keeps the answer and records `applyError`. The ask shows in the inbox as stopped with that error, rather than reopening, so nobody answers twice.

### Closing without an answer

- The source resolves it: the provider closes the request, a delivery settles (`SettleDeliveryProblems`), a ledger question is answered on another path. The ask becomes `withdrawn`.
- The deadline passes: `onDeadline` is applied and the ask becomes `expired`, or with no `onDeadline` it escalates one hop. This replaces `ExpireQuestions`' notification (`internal/ledger/question_expiry.go:43-130`). The ledger keeps recording `question.resolved` / `question.escalated` events; the ask is the device-facing side.
- Every terminal state withdraws its notifications on every device. That closes today's gap for answered end-turn asks.

## Schema 90

One table in the registry, held by the host that holds the session (asks are not federated rows; a paired viewer sees `row.ask` and reads the questions through the existing `?host=` fan-out of `/api/v1/requests`). Layer 1 creates what agent asks use; the columns the other origins need (`project_id`, `ref` with `UNIQUE (origin, ref)`, `deadline_ms`, `on_deadline`, `expired`) come with the layer that needs them.

```sql
CREATE TABLE IF NOT EXISTS asks (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  origin TEXT NOT NULL,                    -- 'agent' in layer 1
  title TEXT NOT NULL,
  questions_json TEXT NOT NULL,
  routed_to TEXT NOT NULL,                 -- 'person' or a session id
  routed_at_ms INTEGER NOT NULL,           -- when it reached routed_to, for escalation
  route_json TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('open','answered','withdrawn')),
  answer_json TEXT NOT NULL DEFAULT '',
  answered_by TEXT NOT NULL DEFAULT '',
  answered_via TEXT NOT NULL DEFAULT '',
  answered_at_ms INTEGER NOT NULL DEFAULT 0,
  applied_at_ms INTEGER NOT NULL DEFAULT 0,
  apply_status TEXT NOT NULL DEFAULT '',
  apply_id TEXT NOT NULL DEFAULT '',
  created_at_ms INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS asks_open ON asks(routed_to) WHERE state = 'open';
CREATE INDEX IF NOT EXISTS asks_session ON asks(session_id, created_at_ms);
```

- `asks` joins `presentationInputs` (`internal/registry/catalog_presentation_version.go:20-40`), so row attention and the decisions revision (`internal/daemon/decisions_revision.go`) move when an ask does.
- 90 covers the first layer only. Each later layer below removes old columns or tables and needs its own number; ask the coordinator for each at the time.
- Column drops use the rename-and-copy rebuild already used for CHECK changes (`internal/registry/registry_migrations.go:609`).

## Removing the four old paths

Each source is migrated in its own layer, and its old path goes in the same change.

1. **End-turn asks** (with schema 90).
   - `agenthail_end_turn` loses `asks` and `needs`: a turn needs the person exactly when it leaves an open ask routed to the person.
   - Open, unanswered, unrouted `turn_ends` rows with asks become `asks` rows in the 90 migration. Routed ones become asks routed to their `routed_to`. Answered history is not copied; `delivery_history` already has the `request-answered` rows.
   - Dropped: `turn_ends.asks_json`, `routed_to`, `answered_at_ms`, `answer_status`, `answer_message_id`. `endAsksPerson`, the synthetic `turn-end-*` request in `/api/v1/requests`, `answerTurnEnd` and its `respond` branch all go. `docs/maintainers/end-turn-asks.md` is replaced by the asks client contract.
2. **Permission requests.**
   - The daemon writes one ask per open provider request, `ref` = the provider request id, when it observes it, and withdraws it when the provider closes it.
   - Dropped: `session_runtime.waiting_*`, the in-memory `openRequestSets` (so a restart no longer loses the open set), `respond`, `agenthail_requests` (now an asks read), and the separate lead notice in `noticeWaiting`.
   - `catalog_sessions.waiting_on` and the delivery hold while waiting (`internal/delivery/delivery.go:366-379`) read open permission asks instead.
3. **Delivery problems.**
   - Failed or held intents and dead incoming queue rows each raise an ask, `ref` = the intent or queue id.
   - Dropped: `attention_items`, `delivery_intents.dismissed_at`, the snapshot's `attention` and `deliveryProblems` arrays, the four delivery actions, and the client banners (`AttentionBanner`, `DeliveryProblemBanner`). `docs/maintainers/delivery-problems.md` is folded into the asks contract.
4. **Ledger questions.**
   - Asking a question raises an ask on the ledger's planning host (`ProjectsPlannedHere`, `internal/ledger/sync.go:55`), where the expiry sweep already runs.
   - Dropped: the `ledger.question.escalated` notification and the expiry notice text. Ledger events stay the ledger's synced record.

## Raising an ask

- MCP `agenthail_ask {title, questions}` returns the ask id and where it went, or `{withdraw: <ask id>}` withdraws one. Routing is the daemon's; an agent does not choose it.
- `agenthail ask "Deploy it now?" --option Deploy --option "Wait: hold until Monday" --recommend 1 [--link <title>=<url>] [--question …] [--json]`. The caller is resolved the way `agenthail send` resolves it (`AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`; `internal/cli/help.go:229-230`), so terminal Claude and Codex leads can raise asks without an MCP credential. It posts to `POST /api/v1/asks` on the local daemon.
- `agenthail ask list|show|answer|escalate|withdraw` and MCP `agenthail_answer` cover the rest. A session may withdraw only its own asks.
- An agent's ask does not stop its turn. It ends its turn normally, and the open ask is what puts it in Needs you.

### Stop reminder for leads

- A Stop hook for terminal Claude joins the settings.json set (`internal/claudehooks/hooks.go`). The Codex Stop hook (`internal/surface/surfaces/codex_hooks_install.go:26`) and the claude-host hook keep calling `agenthail hook stop`.
- `turnStop` (`internal/cli/turn_stop.go`) no longer requires an MCP credential. It still names `agenthail_end_turn` to a session holding one whose turn has not ended, in the same hold.
- It holds the stop once per turn (`stop_hook_active` lets the next stop through) when all of these hold:
  - the session is a lead: a project coordinator, or the spawner of a running session;
  - the hook input's `last_assistant_message` (Claude Code and Codex both send it) has a line outside fenced code that ends in `?`;
  - no ask was raised since the turn opened (`AskRaisedSince` the latest `turn_ends.started_at_ms`).
- The reason it gives: "Your reply asks the person something. Raise it with agenthail ask (or agenthail_ask) so it reaches Needs you, then stop."
- The settings.json hook runs `hook stop --surface claude --terminal`, which does nothing unless `CLAUDE_CODE_ENTRYPOINT` is `cli`: Claude host sessions load the person's settings too, and only the host's own hook knows which background tasks are ambient.
- This is a fixed text rule, not inference. A reply that only asks rhetorically costs one extra stop.

## Routing: worker → lead → person

- At raise time `routedTo` is the session's hearer from `coordinator.PromptHearer` (project lead first, then spawner), if that hearer is running. Otherwise it is the person. A lead's own asks, and asks from sessions the person started, go to the person.
- The hearer is told once with a notice carrying the ask id. The notice tells it to answer with `agenthail_answer` or `agenthail ask answer <id>`, not `agenthail send`, so the ask closes.
- The hearer can **escalate** (`agenthail ask escalate <id>` / `agenthail_answer` with `escalate`). The ask moves one hop up and the hop is appended to `route`.
- The ask escalates by itself when its hearer:
  - stops running;
  - is archived;
  - or has not answered within the escalation window: 10 minutes for agent and delivery asks, 3 minutes for permission asks, which hold a tool call. Both are config values.
- The person can always answer any open ask. Asks routed to an agent show under "Handled by a lead" (`internal/daemon/decisions.go:73-77`), as they do now.
- Notifications go only to the current `routedTo`: a push for the person, a notice for an agent. Escalating to the person is what pings them.
- This makes permission requests follow the same single route as everything else. Today they go to the lead and the person at once.
- `agenthail_wait` wakes on asks routed to the waiting session.

## How the clients read it

- `GET /api/v1/decisions` stays the inbox, and its answerable items carry asks instead of `requests: [SessionRequest]`:
  - **Waiting on you**: open asks routed to the person.
  - **Holding up work**: open asks routed to the person whose session has queued messages behind it (`queuedBehind`).
  - **Handled by a lead**: open asks routed to an agent.
  - **Stopped on you**: the remaining non-ask attention kinds (usage limit, stopped turn, keep-alive failed) and asks whose answer failed to apply.
- Session rows carry `ask {id, title, routedTo, askedAt, answeredAt}` (the oldest open ask, else the newest answered one until the next turn) and `attention.kind = "ask"` while a person ask is open; `endTurn` no longer carries asks. The client contract is [asks.md](asks.md).
- In layer 1 `/api/v1/asks` serves only the `agenthail` command on the same machine. A device-facing `GET /api/v1/asks` for the notification extension and the voice path comes with the client cleanup layer.
- **Mac, iPhone and dashboard:**
  - Every answer posts `respond` with the ask id.
  - "Accept recommended" works for every origin, and iPhone gets it too (it lacks it today).
  - The Mac menu bar's Needs you and Swift `SessionAttention.build` read the decisions inbox instead of building attention in the app. This finishes the plan in `docs/maintainers/session-presentation-design.md`.
- **Pushes:**
  - One event type, `ask.opened`, with the ask id as `requestId` and a collapse id derived from it. The extension fetches the ask and builds the category from its options: up to four option buttons, plus reply when `freeText`.
  - Every terminal state sends `withdraw`.
  - The paired-host dedupe key is a hash of the holding host and the ask id, so a phone paired to the host and a viewer shows it once.
- **Browser notifications** get the same option actions.

## Room for calling the person (not built)

Nothing in the ask is screen-specific: a title, short questions, short option labels, a recommendation, an answer that is option ids plus text. A later announcer can:

- start a call: a PushKit VoIP push to CallKit, or the voice operator (`internal/voice`), or a voice-mode MCP tool on the Mac;
- read the open ask aloud from `GET /api/v1/asks/<id>`;
- answer with `respond` and `answeredVia: "voice"`.

What this design fixes now so that path needs no new model:

- announcing goes through one internal interface per channel (push, desktop, web push, notice), so a voice channel is one more implementation;
- escalation already has a "nobody answered" step where "call the person" would sit.

## Layers

Each layer works end to end on its own and removes the path it replaces.

1. Schema 90, ask store, `agenthail ask` / `agenthail_ask`, end-turn asks moved over, Stop reminder for leads in terminal Claude and Codex, decisions inbox items carry asks, answers through `respond`. This is the layer that fixes "Needs you is empty".
2. Permission requests as asks; one routing path with escalation.
3. Delivery problems as asks.
4. Ledger questions as asks.
5. Client cleanup: iPhone "Accept recommended", browser notification actions, Swift attention builder removed.

## Settled

1. **Escalation window.** 10 minutes for agent asks, and for delivery asks, which escalate on the same window. 3 minutes for permission asks. Both are config values.
2. **First hop for delivery problems.** Delivery problems go to the sending agent first, then escalate.
3. **Free text on ledger asks.** Free text is always offered next to the recommended option.
