# Turn judge

The turn judge reads how each turn ended and records what it finds. A local model answers four questions about the reply, and the daemon stores the answers next to what it knows itself about the work still going on. The judge is built in layers. Only the first layer exists so far: judgments are recorded and change nothing else. The thresholds for later layers are set from these records.

## Turning it on

It is off until `~/.agenthail/config.json` names a System One endpoint:

```json
{"turnJudge": {"url": "http://192.0.2.10:8100", "model": "d1-3b"}}
```

The block has the same shape as `ledgerSorter`, and both go through `internal/systemone`. A malformed block keeps the judge off, and `GET /api/v1/turn-judgments` reports the error in `judge.error`.

## What is judged

When the observer sees a session's turn complete with a finished reply, it records one judgment for that turn. It skips stale completions, such as one read again after a restart, and failed turns. The reply goes to the model in two parts:

- `reply`: the last two sentences of the reply. Code blocks are dropped, including an unclosed one through the end, and so is a closing block of links or `Sources:`. Each line, including each list item, is split separately. A promise to act next sits at the end, and a longer ending dilutes it. On 200 hand-labeled turns, the last paragraph raised false holds from 10 to 63 at the threshold that kept the known positives.
- `full_reply`: the whole reply, up to its last 6000 characters.

The questions, all asked in one call:

| Name | Type | Asks |
|---|---|---|
| `promise` | probability | whether `reply` ends by promising work the agent starts now or does next, without waiting for the person |
| `needs` | probability | whether `full_reply` asks the person for a question, decision or action |
| `needs_tail` | probability | the same question asked of `reply` |
| `state` | choice | `done`, `waiting_person`, `waiting_work` or `continuing` |

`needs` is asked both ways because the two disagree on recorded turns, and the records decide which one the needs layer uses.

## What the daemon knows

The facts are read when the turn ends, not when the answer arrives, because the work a turn waits on may have finished by then:

- `backgroundRunning`, `backgroundSource`, `backgroundTasks`: the session's background tasks as the observer saw them. `backgroundSource` is empty when no task was listed. For a hosted Claude session the source is `host`: the Claude host keeps the set of live tasks from the SDK's `background_tasks_changed` messages. Codex tasks come from the app server's list of background terminals, or from the transcript when that list can't be read.
- `openAsks`: the session's open asks, of any origin.
- `childrenWorking`: sessions it started that are busy or whose task is running.
- `awaitedRelays`: active one-time relays into the session from a live session, that is, replies it is waiting for.

A turn with any of these will be woken again by that work, so it cannot leave a promise unkept. The judgment also keeps the turn's `end_turn` headline, if the agent gave one.

## Load

Recording a judgment does not delay the turn's completion, its notification or anything else. A judgment is queued, and two workers ask the model, each with a 15 second limit. The queue holds 64 turns. A turn that doesn't fit is not recorded and counts in `judge.dropped`, so a full queue never adds a write to the turn's completion. A failed call is recorded with its error, counts in `judge.failures` until the next answer, and is not retried. The daemon logs each change between up and down once. Judgments older than 30 days are deleted as new ones are written.

Measured on a Linux build host against the endpoint: one call takes about 0.5 seconds. Twenty calls at once took a median of 4.1 seconds, and 77 of 100 took more than 2. That is why the queue limits concurrency, and why no layer may wait on the model for every turn.

## Reading the records

`GET /api/v1/turn-judgments` (read scope) returns `judge`, the endpoint's health (`model`, `since`, `error`, `failures`, `dropped`; null when the judge is off), and `judgments`, newest first. `sessionId` filters to one session, and `limit` takes 1 to 1000 (default 50). Each judgment has the turn, `endedAt`, `judgedAt`, `model`, `ms`, the four answers with the state's probabilities, the `ending` that was asked about, the `headline`, the `facts` and any `error`.

## Later layers

1. Judgments are recorded. This exists.
2. State and headline drive the row, the status dot and the push. They arrive asynchronously: the completion notification waits at most 2 seconds for the judgment and then goes out without it, and the row fills in when the judgment lands. `end_turn` becomes optional; an explicit headline still wins, but the state, the unkept promise and the needs answer always come from the judge. The backlog line moves into the UserPromptSubmit hook's added context and the lead's panel.
3. The unkept hold. A turn is held only when nothing is pending, no ask is open, and the promise score is high. The judge is called synchronously from the Stop hook only when the facts already say nothing is pending, which is a small minority of turns. The hook passes its `last_assistant_message`: the transcript is written only after a tool returns, so a check made during the turn cannot read the reply from it. A hosted Claude session's host already knows whether the turn wrote a reply from the SDK's assistant messages, and could report it next to the tasks. Codex holds through its Stop hook; a turn with no hook gets a follow-up message instead.
4. The needs item: a judged question to the person raises an Inbox item with its own ask origin, added to every origin filter.
