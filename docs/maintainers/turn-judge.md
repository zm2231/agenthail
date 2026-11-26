# Turn judge

The turn judge reads how each turn ended and records what it finds. A local model answers four questions about the reply, and the daemon stores the answers next to what it knows itself about the work still going on. The judge is built in layers. The first records judgments, and the second shows each turn's state on its session row. The thresholds for later layers are set from these records.

## Turning it on

It is off until `~/.agenthail/config.json` names a System One endpoint:

```json
{"turnJudge": {"url": "http://192.0.2.10:8100", "model": "d1-3b"}}
```

The block has the same shape as `ledgerSorter`, and both go through `internal/systemone`. A malformed block keeps the judge off, and `GET /api/v1/turn-judgments` reports the error in `judge.error`.

`promiseModel` names a different model for the promise question alone, at the same `url`:

```json
{"turnJudge": {"url": "http://192.0.2.10:8100", "model": "d1-3b", "promiseModel": "d1-3b-promise"}}
```

This is how a model tuned for the promise question is served without changing the other answers, which were measured on `model`. Without it, both calls go to `model`. A `promiseModel` that is present but blank, null or not a string keeps the judge off, with the error in `judge.error`.

## What is judged

When the observer sees a session's turn complete with a finished reply, it records one judgment for that turn. It skips stale completions, such as one read again after a restart, and failed turns. The reply goes to the model in two parts:

- `reply`: the last two sentences of the reply. Code blocks are dropped, including an unclosed one through the end, and so is a closing block of links or `Sources:`. Each line, including each list item, is split separately. A promise to act next sits at the end, and a longer ending dilutes it. On 200 hand-labeled turns, the last paragraph raised false holds from 10 to 63 at the threshold that kept the known positives.
- `full_reply`: the whole reply, up to its last 6000 characters.

The model reads everything in a call's state, so the questions go in two calls. `promise` is asked alone, with only `reply` in its state: next to the whole reply, the model loses a promise made at the end. On 204 labeled turns, asking it beside `full_reply` ranked unkept promises barely better than chance (AUC 0.54), and asking it on `reply` alone with the wording below ranked them at 0.79. The other three questions share the second call, which has both parts: on 100 held-out lead replies, `needs_tail` ranked replies that need the person better with `full_reply` in the state than on `reply` alone (AUC 0.94 against 0.81).

| Name | Type | Asks |
|---|---|---|
| `promise` | probability | whether `reply` says the agent itself is starting or continuing work right now, so stopping leaves that work undone |
| `needs` | probability | whether `full_reply` asks the person for a question, decision or action |
| `needs_tail` | probability | the same question asked of `reply` |
| `state` | choice | `done`, `waiting_person`, `waiting_work` or `continuing` |

`needs` is asked both ways because the two disagree on recorded turns, and the records decide which one the needs layer uses.

## What the daemon knows

The facts are read when the turn ends, not when the answer arrives, because the work a turn waits on may have finished by then:

- `backgroundRunning`, `backgroundSource`, `backgroundTasks`: the session's background tasks as the observer saw them. `backgroundSource` is empty when no task was listed. For a hosted Claude session the source is `host`: the Claude host keeps the set of live tasks from the SDK's `background_tasks_changed` messages. Codex tasks come from the app server's list of background terminals, or from the transcript when that list can't be read.
- `openAsks`, `askedOf`: the session's open asks, of any origin, and where each is routed: a session id, or `person`.
- `childrenWorking`, `children`: sessions it started that are busy or whose task is running, by id.
- `awaitedRelays`, `awaiting`: active one-time relays into the session from a live session, that is, replies it is waiting for, by the sending session's id.
- `typed`: the person typed the prompt that opened the turn into the session itself. The prompt hook reports each prompt to the daemon, which records it on the turn's `turn_ends` row as typed unless it is a message the person sent through Agenthail (the daemon holds each of those for the hook to claim), an agent's message, a task notification or a command. Codex reports the prompt under its `turn_id` when the hook gives no `prompt_id`. A session without the prompt hook never has a typed turn.

A turn with any of these will be woken again by that work, so it cannot leave a promise unkept. The judgment also keeps the turn's `end_turn` headline, if the agent gave one.

## The row

Each session row in the session view, the snapshot and the catalog stream carries `turnState {state, reason, source, turnId, pushed}` for its last completed turn, and the row's label says it. The state comes from the first of these that applies:

1. An open ask to the person: `waiting_person` from `facts`.
2. The judgment's facts, from `facts`: a worker awaiting its project's lead waits on `@lead`; any other awaited session, or a running child, is named by alias, else "a session", with "and N others" for more; running background tasks read "background work". All are `waiting_work`.
3. An `end_turn` whose `waitingOn` says what the person has to do ([work record](work-record.md#agenthail_end_turn-mcp-always-loaded)): `waiting_person`, from `endTurn`, with `reason` the `waitingOn` text, `turnId` the completed turn and `pushed` whether its push went out. A worker in a project with a lead reads `waiting_work` on `@lead`. A judgment of an older turn does not apply.
4. The judge's state, from `judge`. A worker in a project with a lead never waits on the person: the lead is who it answers to, so `waiting_person` reads `waiting_work` on `@lead`. A judgment with an error gives no state.
5. An `end_turn` for the turn: `done`, from `endTurn`.

Otherwise the row has no `turnState` and keeps its usual label. The labels are "Done", "Needs you", "Said it would continue", and "Waiting on <reason>". Only the label changes: the row stays `idle`, and a judged "Needs you" adds no attention, Inbox card or count. A wait from `waitingOn` is listed in Needs you as kind `turn`.

A judgment that lands after the turn completes republishes the row, so a paired viewer shows the host's state. The viewer never judges a mirrored session itself.

## The push

A turn whose row reads "Needs you" from the judge pushes "<session> needs you: <headline>" to the person's devices once, whether the person, an agent or the session itself started it. It does not push when:

- the person typed the turn's prompt into the session (`typed`), since they are at it;
- a newer turn has started or completed by the time the judgment lands;
- the session is delegated, or a worker whose lead the state names instead;
- the state comes from an open ask, which was announced when it reached the person;
- the judge reads any other state.

A turn whose `end_turn` gave `waitingOn` pushes "Needs you: <waitingOn>" in place of its finished push, under the same rules, keys and event type, as soon as it completes. It is claimed on the turn's `turn_ends` row (`waiting_pushed_at_ms`) the same way, and the judge never pushes it, since the row's state comes from `endTurn` before the judge. A typed turn does not push but is still listed in Needs you.

Only the prompt hook marks a turn typed. A terminal Claude without `agenthail hooks install`, or any Codex thread, Agenthail's included, without `agenthail hooks install codex`, gets no mark, so a turn the person types there pushes too; `agenthail doctor` says so for missing or outdated hooks. Claude sessions Agenthail hosts run the hook themselves.

The host claims the push on the judgment (`pushed_at_ms`) in one write that also requires the judged turn to still be the session's last, with none running, and the row's ask to be the one the state was read with. The project and its lead are not in that write, so the state is read again once claimed: the claim is confirmed and pushed only if the row still needs the person, and released otherwise. Until it is confirmed the claim reads as not pushed, so no row, and no paired viewer, sees a push that may not happen. So a turn judged again never pushes twice, a new turn, ask or lead that arrives while the state is read stops the push, and the row's `turnState` carries the judged `turnId` and `pushed`. A paired viewer pushes a mirrored turn only once its host's row says `pushed`, under the host's dedupe key `sha256(host, session, turn, "needs_you")`, so the relay delivers it to a phone once. The push and the turn's finished push share the collapse id `turn-<sha256(host, session, turn)>`, so a phone shows "needs you" in place of "finished". The event type is `turn.needs_you`.

## One push per burst

A session pushes once for a burst of turns, such as a turn and the follow-up a queued peer message starts right after it. A finished turn's push is held in the daemon (`turn_push_settle.go`) and settles once:

- verdict: the judge's verdict for the turn lands. A needs-you verdict sends only the needs-you push; any other verdict sends the finished push.
- idle: no verdict is coming, no message waits in the session's queue, and no message sent to the session is still expected to start a turn.
- deadline: `turnPushSettle` (20 s, which covers the judge's 15 s deadline) passes after the push was last held for one of those reasons.
- shutdown: a stopping daemon sends what it still holds.

A turn seen starting holds the push until that turn ends; its own completion replaces the held push, so the burst pushes once, for its last turn. A message the daemon sends to the session while a push is held is expected to start a turn, and the next turn that ends meets it. A needs-you verdict that lands after the deadline push still goes out under the same collapse id, so it replaces the entry; a later done verdict sends nothing. Each settled push logs `push settled session=... turns=... reason=... event=... injected=N`, where `injected` counts peer messages that reached the session mid-turn. Those are not tied to a later turn, so a burst they start can still push twice.

A viewer mirrors a host's finished push from the row's `turnCompletion`, which the host records only for the push it sends.

## Load

Recording a judgment does not delay the turn's completion, its notification or anything else. A judgment is queued, and two workers ask the model. Both calls of one judgment share a 15 second limit. The queue holds 64 turns. A turn that doesn't fit is not recorded and counts in `judge.dropped`, so a full queue never adds a write to the turn's completion. A failed call is recorded with its error, counts in `judge.failures` until the next answer, and is not retried. The daemon logs each change between up and down once. Judgments older than 30 days are deleted as new ones are written.

Measured on a Linux build host against the endpoint: one call takes about 0.5 seconds, so a judgment takes about 1 second. Twenty calls at once took a median of 4.1 seconds, and 77 of 100 took more than 2. That is why the queue limits concurrency, and why no layer may wait on the model for every turn.

## Reading the records

`GET /api/v1/turn-judgments` (read scope) returns `judge`, the endpoint's health (`model`, `promiseModel`, `since`, `error`, `failures`, `dropped`; null when the judge is off), and `judgments`, newest first. `sessionId` filters to one session, and `limit` takes 1 to 1000 (default 50). Each judgment has the turn, `endedAt`, `judgedAt`, `model`, the `promiseModel` that answered `promise`, `ms`, the four answers with the state's probabilities, the `ending` that was asked about, the `headline`, the `facts` and any `error`.

## Later layers

1. Judgments are recorded. This exists.
2. State and headline drive the row, the status dot and the push. The state on the row and the needs-you push exist; the rest does not yet. They arrive asynchronously: the completion notification waits for the judgment, up to the deadline in [One push per burst](#one-push-per-burst), and the row fills in when the judgment lands. `end_turn` is optional: a turn that ends without it is never held or prompted again. An explicit headline still wins, but the state, the unkept promise and the needs answer always come from the judge. The lead's panel lists the project's ready backlog items, which the coordinator is already told of in its prompt context (see [the ledger](../ledger.md)).
3. The unkept hold. A turn is held only when nothing is pending, no ask is open, and the promise score is high. It needs a Stop hook again, which Agenthail no longer installs: the judge would be called synchronously from it only when the facts already say nothing is pending, which is a small minority of turns. The hook would pass its `last_assistant_message`: the transcript is written only after a tool returns, so a check made during the turn cannot read the reply from it. A hosted Claude session's host already knows whether the turn wrote a reply from the SDK's assistant messages, and could report it next to the tasks. Codex would hold through its Stop hook; a turn with no hook would get a follow-up message instead.
4. The needs item: a judged question to the person raises an Inbox item with its own ask origin, added to every origin filter.
