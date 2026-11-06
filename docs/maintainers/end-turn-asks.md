# End-of-turn asks: client contract

An agent that ends its turn with `agenthail_end_turn` and `needs: "you"` leaves questions for the person. Clients show them as a card in the question pager and send one answer. This is everything a client needs; the daemon side is in [work-record.md](work-record.md).

## Seeing an ask

The session row (in the session view and the snapshot) carries the standing end of turn:

```json
"endTurn": {
  "headline": "Ready to ship",
  "needs": "you",
  "asks": [{"question": "Deploy it now?", "options": [{"label": "Deploy"}, {"label": "Wait", "does": "hold until Monday"}], "suggested": 0, "links": [{"title": "Runbook", "url": "https://..."}]}],
  "endedAt": "2026-11-06T04:52:30Z",
  "requestId": "turn-end-1793940750123",
  "answeredAt": "2026-11-06T04:55:02Z"
}
```

- `requestId` is set while the ask waits on the person and after it is answered. It is absent for `needs: "nothing"` and for a needs-you an agent was told of instead (`askedSession` set).
- `answeredAt` is absent until the person answers. An answered ask is no longer in Needs you (`attention` is cleared) and must not be offered again.
- The row's `attention.kind` is `ask` while it waits.

Fetch the card when the row has a `requestId` and no `answeredAt`, and the session is not read-only. This is independent of `capabilities.respond`: a session whose provider answers no requests through Agenthail still lists its ask. Key any cache on the session id, `waitingOn.id` and the ask's `requestId`, so a new ask refetches.

## The request

`GET /api/v1/requests?sessionId=<id>` lists it after any provider requests:

```json
{
  "id": "turn-end-1793940750123",
  "kind": "ask",
  "tool": "",
  "title": "Ready to ship",
  "prompt": "Ready to ship",
  "questions": [
    {"question": "Deploy it now?", "header": "", "multiSelect": false,
     "options": [{"label": "Deploy", "description": ""}, {"label": "Wait", "description": "hold until Monday"}],
     "suggested": 0,
     "links": [{"title": "Runbook", "url": "https://..."}]}
  ],
  "actions": [{"id": "o0", "title": "Deploy"}, {"id": "o1", "title": "Wait"}, {"id": "reply", "title": "Reply…"}],
  "alwaysAllow": false,
  "createdAt": "2026-11-06T04:52:30Z"
}
```

- Up to 4 questions, each single-select with 1 to 6 options; question texts are distinct. `description` is the option's `does`.
- `suggested` (optional) is the index of the option the agent recommends; mark it.
- `links` (optional, up to 3) open outside the app.
- `tool` is always present and empty, so decoders that require it keep working.
- `actions` follow the question rules: buttons only for a single ask, an option button each when it has 4 or fewer options, plus `reply`.

## The card

Render it with the question pager, as for `kind: "question"`, with these differences:

- Heading: the session's display name, then "asks:", then `title`.
- Never send on one tap, even for a single single-select ask; a pick waits for Submit.
- An optional note field: on the page for a single ask, on the review step for several.
- No Decline. The person can leave the ask, or write in the composer, which starts a new turn and retires it.

## Answering

Post the existing `respond` action with an `Idempotency-Key` header:

```json
{"action": "respond", "sessionId": "<id>", "requestId": "turn-end-1793940750123",
 "decision": {"behavior": "allow", "answers": {"Deploy it now?": "Wait"}, "message": "After lunch."}}
```

- `answers` maps each question text to the picked label or the typed "Other" text. Omit `message` when the note is empty.
- A notification button posts `{"choice": "o1"}`, or `{"choice": "reply", "message": "<typed text>"}`, as it does for questions.
- `personName` may be sent as with `send`; otherwise the daemon uses the person's saved name.

The daemon sends the session one message as the person:

```
1. Deploy it now? Wait

After lunch.
```

Responses:

| Status | Body | Meaning |
|---|---|---|
| 200 | `{"ok": true, "result": {"requestId", "outcome": "answered", "receipt": {...}}}` | sent; the row's `answeredAt` is set |
| 200 | the same body again | a retry with the same `Idempotency-Key` and body |
| 409 | `{"error": {"code": "stale", "message"}}` | already answered (by any device or key), or a newer turn retired the ask; drop the card |
| 400 | code `invalid_request` | nothing answered and no note, or a button the ask does not offer |
| 403 | code `forbidden` | an agent tried to answer; asks are the person's |
| 502 | code `bad_gateway` | delivery failed. If the message says it "may have reached the session" the ask stays answered and must not be retried; otherwise it is open again and can be answered again |

After any response, refetch the session row and requests, as after answering a question.

## Notifications

The turn-completed push for a needs-you turn carries the ask's request id (`requestId` / `AGENTHAIL_REQ_<id>` category), so the phone builds buttons from the request's `actions` exactly as for a question. The push text is "<surface> needs you: <headline>".
