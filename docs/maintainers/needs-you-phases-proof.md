# Needs you, work phases and proof steps

Status: design. Nothing here is built yet.

This design covers three changes to the core that the work-first project view is built on:

1. **One Needs-you list.** An ask from a session that holds work is recorded on that work item, and a turn can end saying it waits on the person. Both appear in the one list the apps already read, together with work waiting for the person's review.
2. **Phases on work items.** Every work item has a phase (Scope, Build, Review, Verify, Check, Ship), computed from what already happened to it. The keys are fixed. Each project can rename them.
3. **Proof steps on work items.** The agent that holds an item records what it checked: expected, observed, pass or fail, with links. The person reads them when the item reaches Check.

The core stays generic. An extension can later set its own phase names, add its own steps to the proof, or read phases to drive its process, all through the APIs below, with no extension-specific code in the core.

The three parts ship in this order, each working end to end before the next starts: phases first (read-only, no new writes except Verify), then proof steps, then the Needs-you changes, which show both.

## What exists today

| Piece | Where | What it does |
|---|---|---|
| Agent asks | `internal/daemon/asks.go`, `mcp_ask.go`, `internal/registry/asks.go` | `agenthail_ask` raises an ask (title, 1 to 4 questions, options, recommended option, links). A worker's ask goes to its lead first and moves up to the person after a window, when passed on, or when the lead stops. Stored in the `asks` table. |
| Ledger questions | `internal/ledger/questions.go`, `board_questions.go`, `internal/registry/ledger_asks.go` | `agenthail ledger question --to <goal\|ask\|clause\|project>` writes a `question` event with a recommendation, a default, a deadline and a kind (scope, framing, outward). A reconciler mirrors each open question into an ask of origin `ledger`, routed only to the person. Answers are recorded as the person's words. |
| Parked on the person | `internal/ledger/work_park.go`, `internal/daemon/ledger_backlog_watch.go` | `ledger park --person "<question>"` releases an item until the person decides; it shows in the decisions list as kind `parked`. |
| Decisions list | `internal/daemon/decisions.go`, `decisions_http.go`, `decisions_revision.go` | `GET /api/v1/decisions` returns the person's open asks and provider requests, stopped sessions, and parked work, grouped `stopped`, `holding`, `waiting`, plus a "Handled by a lead" section. A `decisions.changed` catalog event carries a revision so apps refetch only on change; paired hosts mirror each other's revision. The Mac and iPhone apps and the dashboard render it and count it. |
| End of turn | `internal/daemon/mcp_end_turn.go`, `turn_ends` table | `agenthail_end_turn {headline, next}`. A turn with an end-turn call reads `done` unless something else applies. |
| Turn judge | `internal/daemon/turn_state.go`, `turn_needs_you.go`, [turn-judge.md](turn-judge.md) | A local model reads the reply and may judge it `waiting_person`. That changes the row label to "Needs you" and pushes once, but adds no entry to the decisions list. |
| Work items | `internal/ledger/work_items.go` | States `open`, `in_progress`, `blocked`, `in_review`, `done`, `closed`, `parked`, `deleted`, changed by `work_item.state` events. Every item has a timeline of state changes, logs and comments. |
| Takeover | `internal/ledger/work_takeover.go` | Another session can release, submit or finish a claimed item whose holder is gone. `--then done` needs a "PR merged" line from the PR watcher. |
| PR watcher lines | `internal/daemon/prwatch/link.go`, `pr_host.go` | When a pull request is linked to a session, the daemon logs `PR opened: <url>` to the item it is for, and later `PR merged: <url>` or `PR closed unmerged: <url>`. These logs carry source `pr-item`, which only the daemon can set. |
| Project board | `internal/daemon/ledger_board.go`, `ledger_page.go` | The board page puts each held item in a lane: `you` (its holder has an open ask to the person), `ciFailing`, `ciPending`, `review`, `blocked`, `running`, `idle`. The Mac and iPhone apps show this page in a web view. |

Gaps this design closes:

- An ask is tied to a session, not to the work. A board marks every item a session holds as `you` when the session asks anything, and the item records nothing about the question or its answer.
- A turn that ends waiting on the person shows in the list only if the agent raised an ask. Otherwise the judge's guess sets a label and a push, never a list entry.
- Work waiting for the person's review is not in the list at all.
- There is no phase. The board lanes say what needs attention, not how far along the work is.
- There is nowhere to record what the agent checked, so review means reading the transcript.

## 1. One Needs-you list

### 1a. Asks on held work

**What changes.** An ask from a session that holds work is linked to one work item. The ask itself is unchanged: same tool, same limits, same routing to the lead first, same answering and escalation. In addition, the ledger records a question on that item, so the item shows what it waits on and keeps the answer in its history.

Which item:

- `agenthail_ask` and `agenthail ask` take an optional `item`. It must be an item the session holds (`in_progress` or `blocked` with this session as assignee); any other is refused as `invalid`.
- Without `item`, the ask is linked to the session's only item in progress, the same rule the PR watcher uses to log a pull request. A session holding no item, or several with none named, raises a plain ask. With several, the reply says so: `"note": "you hold 2 items; name one with item to show this ask on it"`.

Defaults:

- An ask may carry a default for a single-question ask: `default`, an option index, and `defaultAfterMinutes` (15 to 10,080). Both or neither.
- No default is the normal case: the ask waits for an answer, however long. An agent leaves the default out whenever no option is safe to take without the person.
- A default whose option label or description reads as an outward act (sending, merging, deploying, paying, deleting: the same word list ledger questions use) is refused: `"an outward default; leave default out so the person answers"`.
- At the deadline an open ask with a default expires (`state` `expired`, as ledger questions do today), and the session gets one message: `[agenthail] No answer by <time>; the default applies: <option label>.` The item's question is resolved as defaulted.

The ledger question:

- The ledger gains a question kind `decision` and a work item target. A `decision` question is created only by the daemon, for a linked ask. Its payload adds:

  | Field | Meaning |
  |---|---|
  | `itemId` | the work item it is on |
  | `askId`, `askHostId` | the ask that carries it, and the host that holds that ask |
  | `prompts` | the ask's questions: text, options (label, does), recommended option, links |
  | `defaultOption`, `deadlineMs` | the default option index and when it applies, or absent |

  `text` is the ask's title. `recommendation` is the recommended option's label of the first question, or empty. A `decision` question needs no recommendation and no default.
- `ledger question --to <item>` also accepts a work item for the person's own scope, framing and outward questions. Those keep today's rules: the person answers, through the ledger ask.
- The ledger's ask reconciler skips questions that carry `askId`: they already have their ask, so the person never sees one question twice.
- `question.resolved` gains `by` (`person`, or the session id of the lead that answered) and `withdrawn` (the agent withdrew the ask). The answer text is the message the session received. Only `scope`, `framing` and `outward` questions require the person's words; a `decision` question records whoever the ask was answered by, since a lead may answer its worker.

Keeping the ask and the question in step:

- The asks table holds the truth about routing and answering. The ledger question follows it. Each ask row gains `work_item`, `ledger_key` and `ledger_question`.
- Raising writes the ask row first, then the question. When the project's ledger is kept by a paired host that cannot be reached, the ask still goes out: the session is not held up by the ledger. A reconciler, run whenever an ask changes and every 30 seconds, writes the missing question for any linked ask with no `ledger_question`, and the resolution for any closed ask whose question is still open. Both writes are idempotent: the question id is stored on the ask row in the same transaction that confirms the ledger write, and a resolution for a question already resolved is a no-op.
- When the item stops being the session's (handed off, parked, taken over, submitted, done) while its ask is open, the ask stays open and stays linked. The item shows the open question; whoever holds the item next sees it.

What the person and other sessions see on the item: an open `decision` question puts `person` in the item's `waitsOn`, puts the item in the board lane `you`, and shows on the item page as the question with its options and recommendation. The board lane `you` is set from the item's own open questions and no longer from every open ask of its holder, so a session holding two items marks only the one it asked about. A plain ask from a holder with no linked item still marks all of its items, as today.

Answering the ask (by the person, from any surface, or by the lead) unblocks the session as today and resolves the item's question in the same write path.

### 1b. A turn that waits on the person

**What changes.** `agenthail_end_turn` takes an optional `waitingOn`:

```json
{"headline": "The import is ready to run on the real data", "waitingOn": "Upload the March export to the shared folder"}
```

- `waitingOn` is what the person has to do or decide before the work goes on, in their words, at most 120 characters, one line. The tool description says to leave it out when nothing waits on the person, and to use `agenthail_ask` instead when the decision has options.
- It is stored on the turn's `turn_ends` row, new column `waiting_person TEXT NOT NULL DEFAULT ''`.
- It is linked to a work item by the same rule as an ask: optional `item`, else the session's only item in progress.

How the row reads it (`turnStateOf`), in order:

1. An open ask to the person: `waiting_person` from `facts` (unchanged).
2. The daemon's facts about running work (unchanged).
3. **New:** an end-turn call with `waitingOn`: `waiting_person`, source `endTurn`, reason the `waitingOn` text. A worker in a project with a lead reads `waiting_work` on `@lead`, as a judged wait does today, and appears under "Handled by a lead".
4. The judge's state (unchanged).
5. An end-turn call without `waitingOn`: `done` (unchanged).

The judge still runs and still pushes for turns that end without an end-turn call (terminal sessions, sessions taught the CLI card). A turn whose end-turn call gave `waitingOn` pushes once through the same claim (`ClaimTurnPush`), with body `Needs you: <waitingOn>`, and the judge's push for that turn does not fire. A turn the person typed into the session does not push, as today, but still appears in the list.

The wait ends when the session's next turn starts, whoever starts it, or when the person dismisses it from the list.

### 1c. The list

**What exists.** `GET /api/v1/decisions` already is the person's Needs-you list: the apps count it and show it, it has a revision event, and paired hosts mirror it. It stays the one list, under its current path and event name. A parallel endpoint would split the count the apps show, and a rename would change every client for no gain.

**What changes.** Every item gains:

| Field | Meaning |
|---|---|
| `key` | unique within the page and stable while the item stays: `ask:<askId>`, `request:<sessionId>`, `turn:<sessionId>:<turnId>`, `parked:<workItem>`, `review:<workItem>`, `stopped:<sessionId>` |
| `workItem`, `workTitle` | the work item it concerns, when there is one (today only on `parked`) |
| `phase` | that item's phase key (part 2), when there is one |

Clients key items by `key`, replacing the `sessionId` + `workItem` pair.

Two new kinds:

| Kind | Group | When | Answer |
|---|---|---|---|
| `turn` | `waiting` | a session's last completed turn ended with `waitingOn`, no turn is running, and it is not routed to a lead (1b) | open the session and reply, or dismiss. `answerable` is false; `reason` is the `waitingOn` text; `since` is when the turn ended |
| `review` | `review`, a new group after `waiting` | a work item is `in_review` in a ledger this host keeps | approve or send back (below). `answerable` is false. It carries `proof` (`{pass, fail, pending}` counts, part 3) and `prs` (the item's pull requests, as the board has them) |

An ask linked to an item (1a) stays kind `ask` in its usual group and gains `workItem`, `workTitle` and `phase`.

Review from the list:

- A new person-only action `work-review`, `{"workItem", "decision": "approve" | "reject", "note"}`, posted to `/api/v1/actions` with an `Idempotency-Key`. Any session, one acting for the person included, is refused `403 forbidden`.
- The ledger takes a review only with the person's words. The daemon records the person's decision (`Approved.` or `Sent back.`, then the note) as an utterance captured by `ask`, said to the item's assignee, exactly as an answer to a ledger question is recorded today, then writes the approve or reject with that utterance. A reject needs a note.
- Approving records the review; it does not merge anything. Merging stays a decision about the pull request.
- An item the ledger has already moved out of review is `409 stale`.

Dismissals: `turn` and `stopped` items are dismissed through the existing `dismiss-decision` action and key. A `review` item cannot be dismissed; it leaves the list when the item leaves review.

Revision: `ownDecisionsRevision` hashes the page as it does now, so the new kinds change the revision when they appear or go. The ledger change that moves an item into or out of review pokes the revision, as parking does today.

Push: a `review` item pushes once when the item enters review, `"<title> is ready for your review"`, event `work.review`, collapse key per item. A `turn` item pushes as in 1b. Asks push as today.

## 2. Phases

### Keys and labels

| Key | Default label | The item is here when |
|---|---|---|
| `scope` | Scope | `open` or `parked`: nobody is building it |
| `build` | Build | claimed (`in_progress` or `blocked`) and no open pull request |
| `review` | Review | claimed, with a pull request the PR watcher logged as opened and not since closed unmerged |
| `verify` | Verify | claimed, and the holder marked Verify after the newest pull request was opened and after the item was last claimed or sent back |
| `check` | Check | `in_review`: waiting on a reviewer, usually the person |
| `ship` | Ship | `done`, or a pull request on it was logged as merged |

`closed` and `deleted` items have no phase. Phase is not a gate: nothing refuses an action because of the phase, and no phase has to be passed through.

The rules, in this order, first match wins:

1. `closed` or `deleted`: no phase.
2. `done`: `ship`.
3. A `PR merged` line on the item: `ship`. This covers an item whose holder ended after the merge, before anyone marked it done.
4. `in_review`: `check`.
5. `open` or `parked`: `scope`.
6. A Verify mark newer than both the newest `PR opened` line and the item's last move into `in_progress`: `verify`.
7. A pull request opened and not closed unmerged: `review`.
8. Otherwise: `build`.

What this gives for the cases the work-first view depends on:

- Claimed, nothing else: `build`. PR opened: `review`. Holder marks Verify: `verify`. Submitted: `check`. Approved: `ship`.
- Sent back from review (`reject` moves it to `in_progress`): the old Verify mark predates the move, so `review` if its PR is still open, else `build`.
- A second PR opened after Verify: back to `review` until Verify is marked again.
- Taken over with `--then release`: `scope`; `--then submit`: `check`; `--then done`: `ship`.
- An item with a merged PR whose holder is gone: `ship`, while its state still says `in_progress` until someone marks it done.
- All its PRs closed unmerged: `build`.

### Where it is computed

Phase is derived, never stored. The board fold in `internal/ledger` already replays every event in order on every host. It gains per-item marks, kept the way the review state is kept (`work_review.go`): the sequence of the last move into `in_progress`, of the newest `PR opened` line, of the newest Verify mark, and whether a `PR merged` line exists. `WorkItem` gains `phase` and `phaseSince` (when the item entered its current phase), filled in when the board is read. Because the fold is deterministic, every host that holds the ledger computes the same phase.

The PR lines stay the text the watcher writes today: they are already the contract `mergedPR` in takeover and `itemPRs` in the board read. Only lines with source `pr-item` count, which the daemon alone writes. The prefixes move into one parser in `internal/ledger` that takeover, the board page and the phase all use, replacing the two copies of the prefixes in `internal/ledger/work_takeover.go` and `internal/daemon/ledger_board.go`. Existing ledgers keep their meaning and need no migration.

### The Verify mark

The only new signal. The holder says it has finished building and is now checking the work:

- A work log of the new type `verify`: `agenthail ledger verify <item> "<what is being checked>"`, which is `ledger log <item> --type verify "..."`; MCP `agenthail_ledger_write {"op": "work", "request": {"action": "log", "itemId", "logType": "verify", "text"}}`.
- Only the holder writes it, as with any log. The text is required.
- A host that does not know the type shows it as an ordinary log.

### Project labels

- A new ledger event `phase_labels` sets the labels for the project: `{"labels": {"verify": "QA", "check": "Sign-off"}}`. Keys must be the six above; a label is 1 to 24 characters, one line. An empty label returns that key to its default. The newest event wins per key.
- Keeping the labels in the ledger gives a project one set of names on every host, through the sync that already keeps ledgers equal.
- CLI `agenthail ledger phases` prints the keys and current labels; `agenthail ledger phases --label verify=QA --label check=Sign-off` sets them. MCP `agenthail_ledger_write {"op": "phases", "request": {"labels": {...}}}`. Any session in the project or the person may set them, so an extension can name the phases for its own process.
- Every read that returns a phase also returns the project's `phaseLabels`, all six keys, so a client never holds a default of its own.

## 3. Proof steps

### Shape

A proof step is one check the holder ran:

| Field | Meaning | Limit |
|---|---|---|
| `id` | issued by the ledger, `p1`, `p2`… within the item | |
| `title` | what was checked | 120 characters |
| `expected` | what should happen | 1,000 characters |
| `observed` | what happened | 1,000 characters, empty until checked |
| `outcome` | `pass`, `fail` or `pending` (written before it was run) | |
| `revision` | the commit or build it was observed at, optional | 64 characters |
| `attachments` | `{title, url}`: http(s) links to a screenshot, a log, a CI run, a recording | 5 per step, title 80 |
| `by`, `at` | the writing session and time | |

An item holds at most 30 steps. Files are referenced by link, never stored in the ledger: the ledger is synced text and is not a file store. An extension that keeps its own screenshots links to them.

### Writing

- A new ledger event `work_item.proof`, `{itemId, step}`. Without `step.id` it adds a step; with one it replaces that step's fields. `{itemId, remove: "<id>"}` removes one.
- Only the item's holder writes proof, while the item is `in_progress`, `blocked` or `in_review` (the holder can still correct a step after submitting). A reviewer comments instead.
- CLI: `agenthail ledger proof <item> --title "..." --expected "..." [--observed "..."] [--outcome pass|fail|pending] [--revision <sha>] [--link <title>=<url>]... [--step <id>]`, and `agenthail ledger proof <item> --remove <id>`.
- MCP: `agenthail_ledger_write {"op": "work", "request": {"action": "proof", "itemId", "proof": {...}}}`.

### Reading

- The item read (`ledger show`, `agenthail_ledger_read {"view": "item"}`, `/api/v1/ledger?view=item`) returns `proof`: every step in order, each with `stale: true` when it was written before the item's last move into `in_progress` (it was sent back since).
- Work lists and board rows return the counts `{pass, fail, pending}` of steps that are not stale.
- The `review` entry in the Needs-you list returns the same counts (1c).

## Storage and migration

| Change | Store | Migration |
|---|---|---|
| `asks.work_item`, `asks.ledger_key`, `asks.ledger_question` | registry | one dated named migration adding the three columns, empty by default. Existing asks stay unlinked; they close as they do today. |
| `turn_ends.waiting_person` | registry | the same migration adds the column, empty by default. No past turn waits. |
| question kind `decision`, work item target, payload fields `itemId`, `askId`, `askHostId`, `prompts`, `defaultOption`; `question.resolved` fields `by`, `withdrawn` | ledger events | none: new fields on new events. Existing questions read as before. |
| log type `verify` | ledger events | none |
| `phase_labels` event | ledger events | none; a project without one uses the defaults |
| `work_item.proof` event | ledger events | none |
| phase | derived | none: existing ledgers get a phase from their own history the first time a board is read, including items done long ago and items taken over |

Paired hosts update together. A host that does not know a new event type skips it when building its board, as the board fold already skips unknown types, so it shows no phase labels or proof until it updates. A write of a new kind sent to a project ledger kept by an older host is refused by that host with its error; there is no fallback path.

## API, MCP and CLI summary

| Surface | Addition |
|---|---|
| `agenthail_ask` / `agenthail ask` | `item`, `default`, `defaultAfterMinutes`; the reply may carry `note` and `workItem` |
| `agenthail_end_turn` | `waitingOn`, `item` |
| `agenthail_ledger_write` | work actions `log` with `logType: "verify"` and `proof`; op `phases` |
| `agenthail_ledger_read` | `phase`, `phaseSince`, `proof`, `phaseLabels`, open `questions` on item and work views |
| CLI | `ledger verify`, `ledger proof`, `ledger phases`; `ledger question --to <item>` |
| `GET /api/v1/decisions` | item `key`, `workItem`, `workTitle`, `phase`; kinds `turn` and `review`; group `review` |
| `POST /api/v1/actions` | `work-review` (person only) |
| `GET /api/v1/ledger`, board page data | `phase`, `phaseSince`, proof counts, open questions per item, `phaseLabels` |
| Pushes | `work.review`; end-turn waits use the existing `turn.needs_you` event |

The tool budget test pins the always-loaded tool JSON. The two end-turn fields and three ask fields grow it; the pinned numbers are raised in the change that adds them, with the new byte counts in its description.

## How the apps and dashboard read it

- **Needs you (Mac, iPhone, dashboard).** They already fetch `/api/v1/decisions` on `decisions.changed` and count its items. They switch to keying by `key`, render the work title and phase label on any item that has one, render `turn` items as the session's name and the `waitingOn` line with Open and Dismiss, and render the `review` group with the proof counts, the pull requests, and Approve and Send back (which posts `work-review`; Send back asks for a note). The count is the number of items outside "Handled by a lead", as today.
- **Project board.** The board page the apps show in a web view gains a phase chip on each item, drawn with the project's labels, and the proof counts. The lanes stay: they say what needs attention, the phase says how far the work has come. A view that groups by phase reads `phase` from the same data.
- **Item page.** The board's item view shows the open questions with their options and recommendation, the phase with when it began, and the proof as expected against observed, stale steps marked.
- Phase labels always come from the response. No client keeps its own names.

## Tests for the build

Through public boundaries only:

- Ledger: a phase for each rule above from event sequences, including sent back after Verify, a second PR after Verify, PRs closed unmerged, takeover with each `--then`, and an item with a merged PR whose holder ended; a log line naming a PR without source `pr-item` changes nothing; labels rename without changing keys; proof add, replace, remove, holder-only, limits, and stale after a send-back.
- Daemon: an ask from a session holding one item shows on that item with its options and recommendation, appears once in `/api/v1/decisions` with the item, and answering it delivers to the session and resolves the item's question; the same with the ledger kept by an unreachable paired host, resolved once it is back; a lead's answer is recorded with the lead as `by`; a default applies at its deadline and an outward default is refused; end-turn `waitingOn` shows as a `turn` item, pushes once, and leaves when the next turn starts; a worker's wait goes under "Handled by a lead"; an `in_review` item appears as `review` and `work-review` approves it with the person's words and refuses a session.
- The existing decisions, ledger question and parked tests keep passing unchanged except where an item's key changes.

## Not in this design

- Phase gates, required proof before submit, or per-project phase sets beyond renaming: an extension can enforce those on top by reading phases and proof.
- Storing files in the ledger.
- Changing how the judge decides; its push stays for turns that end without an end-turn call.
- The work-first project view itself, which reads these fields.
