# The intent ledger

The intent ledger keeps what the person asked for next to the work agent sessions do about it. It is per project, keyed by the repository's remote, so every session working on the same repository shares one ledger.

A folder needs no repository to have a ledger. A project added with `agenthail project add` whose folder is not a Git repository, or has only a remote on this machine (a path or a `file:` URL), keeps its ledger under its project id, `project:<id>`. Once anything is kept in that ledger (a captured word, a session's grant or an owner), it stays under the id when the folder later gains a remote, and when the folder is removed and added again it takes the same id back, with its ledger; meanwhile `agenthail ledger` in the removed folder names the kept ledger, and sessions there use whatever ledger the folder resolves to without the project, such as its repository's. Linking a project away is refused while it holds a ledger, as it is for a repository's; a project linked into another uses the survivor's ledger. Another host that owns or replicates the ledger holds it under the same key.

Every change is an event in an append-only log; the board you read is rebuilt from that log. Nothing is edited in place: a correction is a new event that supersedes an old one.

## Capture and scope

Every project with a lead captures the person's words to that lead, from the moment it became the lead. Its forks are captured the same way from their creation. Each message is recorded verbatim and stays *provisional* until a session attaches it. Nothing waits on a provisional message: no turn is held for it, and no session is told to attach it before replying. A Claude session in scope sees one line after each prompt saying how many of the person's messages are provisional; in a project that attaches at checkpoints, it sees that line only at those checkpoints. The bare `agenthail ledger` command lists the newest five provisional messages with a count; `--all` shows the whole board with every one, and `--provisional` lists only them.

```
agenthail ledger enable              # capture the person's words to every session in this project
agenthail ledger enable --attach checkpoints   # the same, but show the provisional count at checkpoints instead of with each message
agenthail ledger enable @session     # the same for one session, and the forks made from it afterwards
agenthail ledger disable             # capture nothing in this project, not even the lead's words
agenthail ledger disable @session    # take back a session's grant
```

A project's enable or disable is an event in its ledger, so it holds on every host that keeps or replicates the ledger. A session's grant stays on the host where the session runs, since that is where its words are captured. Both are made by the local daemon, so these commands fail while it is stopped; start it with `agenthail daemon start`. Other programs make the same change with `POST /api/v1/ledger/scope`, sending `projectKey`, `enabled`, and either `attach` (`prompt` or `checkpoints`) or a `sessionId` to grant or revoke.

Words are captured from three sources, and each message is recorded once even when more than one source sees it:

- **The prompt hook.** Terminal prompts are captured as they are submitted. Run `agenthail hooks install` once per Claude config root.
- **Delivery.** Messages the person sends through Agenthail are captured when they are delivered.
- **The session journal.** Prompts the hook missed are picked up from the session's transcript later.

A captured message is an *utterance*, identified by a ref such as `claude-prompt:<id>`. If a message is larger than one ledger event can hold (4 MiB), the ledger keeps its beginning with a visible marker, its original size and its sha256, under the same ref.

## Attaching words: goals, asks and clauses

Attaching a provisional message records what it means and confirms it. Any session in scope can attach the person's words; `agenthail ledger --provisional` lists the ones waiting, with their utterance refs. The session replies with the readback that `attach` prints, so the person sees how their words were recorded.

```
agenthail ledger goal "Search"                                   # create a goal, or show it
agenthail ledger attach <utterance> --as new-ask --to <goal> --clause "build search"
```

A message that carries several intents is attached in one call, one fragment per intent, with `--and` between them. Each fragment is recorded on its own: a refused fragment prints why and leaves the others attached, and the command fails so the session sees it.

```
agenthail ledger attach <utterance> --as extends --to <ask> --clause "and the docs" --and <utterance> --as decides --to project --clause "use sqlite"
```

`--to` takes any goal, ask, clause or work item, or `project`, and `attach` reads it as the anchor the relation needs. The readback names each step it took:

- A work item stands for the clause it serves. Found work, which serves no clause, stands for the project.
- A clause stands for its ask, or for its goal if it has no ask.
- `new-ask` with `--to project`, or with no `--to`, goes on a goal named after the project. That goal is made the first time.
- `extends` or `delegates` with `--to <goal>` uses the goal's only ask. If the goal has no ask, or more than one, `extends` starts a new ask on it, and `delegates` asks you to name one.
- `decides` with an ask or a clause decides on its goal.
- `refines` or `reacts` with `--to <ask>` uses the ask's only clause.
- `conversation` binds nothing. A `--clause` is kept as a note on the attach; a `--to` is dropped, and the readback says so.
- `reacts` with none of `--adopt`, `--reject` or `--ack` records a reaction that changes nothing.

`attach` refuses only an id that names nothing, and a request it cannot carry out as written, such as adopting a clause that is not a proposal.

`--as` can be left out. `attach` then reads it from the other flags, and the readback says which relation it chose and why:

| Given | Read as |
| --- | --- |
| `--session` | `delegates` |
| `--ack` | `reacts --ack` |
| `--adopt` or `--reject` | `reacts` |
| `--to <question>` | `answers`; with a `--clause` only for a question on an ask, where the answer adds a clause |
| no `--to` and no `--clause` | `conversation` |
| `--to` but no `--clause` | `reacts`, which changes nothing |
| `--to <clause>` or `--to <ask>` with a `--clause` | `extends`: the words become a new clause on the ask |
| `--to <goal>`, `--to project`, found work, or no `--to`, with a `--clause` | `new-ask` |

A work item counts as the clause it serves. Nothing that rewrites or drops the person's words is guessed: rewording a clause needs `--as refines`, acking a cut needs `--ack`, an answer that rewords a clause or records a decision needs `--as answers`, and constraints and decisions need their `--as`.

```
agenthail ledger attach <utterance> --to <ask> --clause "and the docs"   # intent ledger: +1 clause on <ask> (...) (no --as, read as extends: --to is an ask)
```

### When a session hears about provisional words

`agenthail ledger enable` shows each Claude session the provisional count after every prompt. `agenthail ledger enable --attach checkpoints` shows nothing per message. Instead, the count appears at points a session reaches anyway:

- adding, claiming or submitting a work item (`ledger add`, `start`, `review`, and the same writes over MCP, whose result carries it as `ledgerCheckpoint`);
- the start of a session, a resume and a compaction;
- `agenthail ledger`, which lists the project's provisional words.

Either way the count is a note, not a request: attach the words with one `--and` call when you know what they mean. Running `enable` again with the other `--attach` value switches the mode without changing when capture began.

### Filing messages automatically

A local model can file a provisional message under one of the project's goals. It is off until `~/.agenthail/config.json` names it:

```json
{"ledgerSorter": {"url": "http://192.0.2.10:8100", "model": "d1-3b"}}
```

The daemon then sends each new message to `<url>/v1/systemone`. The question names every goal as the board stood when the message was recorded, with up to twelve of its newest clauses, and gives the agent's previous reply as context. Words captured on this host are sorted by this host.

- **Filing.** Only an existing goal of the message's own project, chosen with confidence 0.9 or more, files the message. The board then lists it under that goal as `filed automatically (0.93)`, and it no longer counts as provisional. "A new topic", "thanks only" and anything less sure leave it provisional, and the answer is kept in the ledger as `utterance.unfiled` with the choice and confidence, so the model is never asked about that message again.
- **Attaching wins.** A session can still attach a filed message. The attach replaces the filing; if it names another goal, the board counts it as overridden (`auto-filed N, overridden M`), so the 0.9 cutoff can be checked against real use.
- **No waiting.** Capture only queues the message. One worker asks the model, one message at a time, and waits at most 10 seconds for each answer. The queue holds 64 messages; one that doesn't fit stays provisional.
- **When the model is down.** A failed or unreadable answer leaves the message provisional and is not retried at once. The board read reports `sorter` with the model, `unavailableSince`, the last `error`, the count of `failures`, and how many messages were `dropped` from a full queue. After the next answer, the daemon sorts again, oldest first, exactly the messages whose answer failed or that didn't fit the queue, going back no further than 24 hours. A message the model did answer, even with no goal, is not asked again. While messages are owed a sort, it also tries once every 5 minutes, so recovery doesn't wait for a new message. The daemon logs each change between up and down once.
- **After a restart.** The daemon reads what is owed from the ledger, not from memory: when it starts, it sorts every provisional message this host captured in the last 24 hours that the model has not answered, oldest first.

`--as` says how the message relates to what is already on the board:

| `--as` | Meaning |
| --- | --- |
| `new-ask` | A new request; its `--clause` texts become its clauses |
| `extends` | Adds clauses to an existing ask, goal or the project |
| `constrains` | Adds a constraint |
| `refines` | Replaces a clause's text with a new version |
| `reacts` | Reacts to something on the board: `--adopt` or `--reject` a proposed clause, `--ack` a cut |
| `delegates` | Hands an ask to another session (`--session`) |
| `decides` | Records a decision |
| `answers` | Answers an open question |
| `conversation` | Not a request; nothing is added beyond an optional `--clause` note |

Use the person's own words and their broadest reading. Anything narrowed or left out becomes a *cut*, which the person has to acknowledge.

Each clause records where it came from:

- `stated`: the person said it;
- `inferred`: read from what they said;
- `proposed`: a session suggested it (`agenthail ledger propose`); it binds only once the person adopts it;
- `adopted`: a proposal the person accepted;
- `delegated`: assigned through delegation.

The ledger refuses a stated or inferred clause that does not point at a captured utterance of the person's.

A clause moves through states as the work goes on: `captured`, `read-back`, `owned`, `briefed`, `built`, `connected`, `merged`, `verified-live`, `reported`, or `rejected`.

```
agenthail ledger own <clause> [--session <id>]
agenthail ledger progress <clause> --ref <commit|file|PR|evidence>
agenthail ledger wait <clause> --condition "..." --deadline 4h
```

The bare `agenthail ledger` command shows goals, open asks, decisions and provisional words. `ledger goals`, `ledger asks` and `ledger ask <id>` show more detail.

### The turn gate

At the end of a turn, a session that owns clauses in a project that is enabled or granted to it is held until it does one of these:

- reports progress on its clauses;
- records a wait;
- depends on someone else's work.

Provisional words never hold a turn. The gate does not hold a session while the person has spoken to it in the last 10 minutes. After 3 holds on the same clause, the clause is waived so a session is never stuck.

### Narrowing checks and cuts

`agenthail ledger check <goal>` queues a narrowing check. A sandboxed session compares the goal's clauses with the person's words and raises what is missing as cuts. A cut stays on the board until the person acknowledges it (`attach --as reacts --ack`). It is withdrawn when a clause taken from the words it was raised from covers it (`attach --covers <cut>`).

## Questions

A session that needs the person to decide something records a question instead of guessing:

```
agenthail ledger question --to <goal|ask|clause|project> "Index titles only, or bodies too?" \
  --recommendation "titles only" --default "titles only" --deadline 4h --kind scope [--keeps-breadth]
```

The question is an ask for the person on the asking session's row, with the recommendation and the default as its options and room for their own words. They are notified when it is asked. Their answer is recorded as their utterance and attached to the question as its answer, and the asking session gets one message with it. An answer recorded another way (`attach --as answers` with words the person typed) closes the ask. `agenthail ledger questions [--all]` lists open questions, and with `--all` the answered ones too.

If nobody answers by the deadline:

- A `scope` or `framing` question with `--keeps-breadth` applies its default and records the decision. The ask expires, and the asking session gets one message naming the default.
- Any other `scope` or `framing` question records its default as a cut the person must acknowledge. Its ask stays open.
- An `outward` question never acts on its own. Its ask stays open.

## Work items

Work items are the project's tasks. *Asked* work hangs off a clause, so it traces to the person's words. *Found* work is work a session came across, such as a bug it hit: it has no clause, and it stays out of the person's intent (goals, asks, the turn gate and the checker) until it is linked to one.

```
agenthail ledger work add "add the index" --clause <clause>       # asked work
agenthail ledger add "index build leaks a handle" --type bug --priority P1 --found-in <item>   # found work
agenthail ledger work link <item> <clause>  # found work the person wants becomes asked work
agenthail ledger work edit <item> --priority P0 --label perf --unlabel db --text "..."
agenthail ledger work claim <item>          # waits for the item's dependencies
agenthail ledger work block <item> --reason "..."
agenthail ledger work submit <item>         # in_review
agenthail ledger work approve <item>        # or reject; done after approval
agenthail ledger work dep <item> --on <other item>
agenthail ledger work handoff <item> --done "..." --remaining "..."
```

Every `ledger work <command>` can also be written without `work`, as in td: `ledger list`, `ledger show <item>`, `ledger add` (or `create`, `new`), `ledger edit` (or `update`), `ledger start` (claim), `ledger review` (submit), `ledger close`, `ledger delete`, `ledger reopen` and the rest. `agenthail ledger help` lists them all, and so does an unknown command. Every item has a type (`task`, the default, `bug` or `feature`) and a priority from `P0`, the most urgent, to `P3` (default `P2`), and can carry a description, acceptance criteria and labels. `--found-in` names the item or clause the work was found from.

An item moves through `open`, `in_progress`, `blocked`, `parked`, `in_review` and `done`. Work that will not be done is closed instead, with a reason: `agenthail ledger work close <item> --reason "duplicate of <item>"` (the reason starts with `duplicate`, `wontfix` or `obsolete`). Work nobody holds that was finished elsewhere, such as an imported issue that already shipped, is closed as done with what finished it: `agenthail ledger close <item> --reason "done: PR #12"` takes an open or blocked item with no assignee straight to `done`. A reason that starts with none of these words, such as `--reason "finished in PR #12"`, is read as done, and the readback says so. Claimed work is finished through `submit` and review. `delete` hides an item from every list and `restore` brings it back; `reopen` takes a done or closed item back to open. Closing or deleting an item frees the items that wait on it, and closing work never closes the clause above it.

Two kinds of notes attach to an item:

- `agenthail ledger work comment <item> "..."`: anyone can comment.
- `agenthail ledger work log <item> "..." [--type progress|blocker|decision|hypothesis|tried|result]`: the running log of the session holding the item.

`agenthail ledger work <item>` shows everything a session picking the item up needs in one read: its fields, the clause it serves with its ask, goal and the person's own words, where it was found, what it waits on and what waits on it, any handoff, and its full timeline of state changes, edits, comments and log entries. `agenthail ledger list` lists unfinished items, and `--all` adds done and closed ones. `agenthail ledger search <words>...` finds items in any state but deleted that have every word somewhere in their id, td id, text, description, acceptance criteria, labels, comments or logs. Both take the same filters:

- `--state`, `--type` and `--priority` take one value or a comma list (`--priority P0,P1`).
- `--label` can be repeated, and an item must have every label given.
- `--assignee <session>` or `--mine`, `--clause <id>`, and `--found` for found work only.
- `--sort id|priority|created|updated` (newest first for the dates) and `--limit <n>`.

Every command that takes an item also takes an imported item's td id, with or without the `td:` prefix: `ledger show td-1a2b3c`, `ledger close td-1a2b3c --reason "done: PR #12"`, `ledger dep td-1a2b3c --on td-4d5e6f`. Only the whole id matches.

Comments and logs stay on the item when the clause above it is cut, and they survive a rebuild. When the person's words behind a clause are retracted, asked work added under that clause goes with them, notes included, and the board lists it among the retractions; found work that was linked to the clause goes back to found.

### Parking work

Work that should wait for another day is *parked*, with why. A parked item has no assignee, is not offered by `ready` or `next`, and cannot be claimed until it is unparked. Items that depend on it keep waiting. A clause whose unfinished work is all parked does not hold its owner at the end of a turn.

```
agenthail ledger park <item>... --usage claude[:<root>]|codex   # waits for that provider's usage headroom
agenthail ledger park <item>... --later "after the release" [--until 2026-12-01|7d|2w]
agenthail ledger park <item>... --person "Titles only, or bodies too?" [--ask <ask id>]   # waits on the person's decision
agenthail ledger park <item> --idea --utterance <ref> [--goal "..."] [--plan "..."] [--first-test "..."]   # the person's idea, in their words
agenthail ledger attach <utterance> --as idea --clause "<title>" [--goal "..."] [--plan "..."] [--first-test "..."]   # a new idea from the person's words
agenthail ledger unpark <item>...
```

Each item is parked on its own: a refused one is named and the others still park, and the command fails so the session sees it. Every kind takes `--why` and `--until`. An idea keeps the ref of the person's captured words, and the ledger refuses one that names no captured utterance; the goal, plan and first test are the session's free-form shape of it. `attach --as idea` turns the person's words into a new work item of type `idea`, already parked, and attaches the words to it like any other attach. It is found work under no goal, so it stays out of goals, asks and the end-of-turn check until it is linked to a clause with `work link` or the person's words are attached as a new ask. A work item can't be added or edited into an idea any other way.

Claimed work is parked by its assignee, or for it with `--for-session <session>`, as when a session has run out of usage and cannot make the call itself. Parking claimed work takes the same `--done`, `--remaining`, `--decisions` and `--uncertain` as a handoff, so whoever picks it up reads where it stopped in `ledger work <item>`. `unpark` returns the item to open with that handoff, for the next session to claim. `close`, `delete` and `edit` work on parked items too.

A park is a `work_item.state` event carrying `park` (`kind`, `why`, `revisit`, the parking `session`, and `usage`, `idea` or `person`), so it replicates like any other state change.

`agenthail ledger backlog` lists parked work, with the items that can come back first, and says why each one waits. Unpark is still a choice: the backlog never unparks anything itself.

- Usage work can come back when its provider's reading shows headroom again on the host it named, or when its `resetsAt` has passed. Headroom means under 90% of both the session and weekly windows, the same test as the offer to move a limited Claude session to another account. When work is parked for usage, the host that takes the write records itself, the Claude root (when none is named, the one the parked session hit its limit on, otherwise the primary one) and, if the provider is at its limit, the reset time from its own reading. A write that goes on to another host's ledger carries that reading with it. A reading older than an hour counts as no reading. With no live reading, the item comes back after that recorded reset, and with neither, it waits.
- Later work can come back on its `--until` date. Without one, it waits until someone unparks it.
- An idea waits until someone takes it up.
- Work waiting on the person comes back once the ask it names (`--ask`) is answered. Until then it is listed in the person's Needs you, as a row of its own under the session that parked it. While that ask is still open, the ask's own row stands for it. An ask that expired unanswered leaves the item in Needs you, and work parked with no ask stays there until someone unparks it. Only the host that keeps the ledger lists it, so it shows once when several computers' pages are merged. The host that takes the park records itself as the ask's host when it holds that ask, and each sweep it tells the ledger's owner the state of its asks, since the owner may not be able to reach it.
- A parked item with unfinished dependencies still waits on them.

`--kind usage,later` keeps those kinds, and `--ready` keeps the items that can come back. The read goes through the daemon, because it holds the usage readings. The same list is `view=backlog` on `GET /api/v1/ledger` (`kind`, `ready`) and `view: backlog` in the MCP ledger read. The ETag covers readiness.

The board's `backlog` counts parked items and how many can come back, in total and by kind. The daemon checks readiness every 30 seconds, from the usage readings it took last, and sends `ledger.changed` when an item turns ready or stops being ready.

When a project's coordinator ends a turn with `agenthail_end_turn`, the reply carries `backlog`, one line naming each item of the project it coordinates (not of the checkout it is working in) that has become ready since it was last told, for example `Backlog: widget-W12 ready (later: its revisit date came), parked by <session>.` Each item is named once each time it turns ready. The mark is kept on this host only, in its registry, and is dropped when the item stops being ready. Only the coordinator is told, and nothing unparks or starts a session.

`agenthail ledger backlog --triage [--days N]` lists open or blocked items that nobody holds and that have had no activity for N days (14 by default), to park them in a batch with `ledger park <id>...`. It is `view=triage` (`days`) on the same endpoint.

To pick up work:

- `agenthail ledger work ready` lists open items whose dependencies are settled, the most urgent first and then the ones that unblock the most.
- `agenthail ledger work next` shows the first of those.
- `agenthail ledger work critical-path` shows the longest chain of unfinished items.

### Moving over from td

`agenthail ledger import td [<path>]` reads a td database, read-only, and brings its open, in-progress, blocked and in-review issues over as found work. Each item keeps its title, type, priority, description, acceptance criteria and labels, plus its comments, logs, handoffs and reviews, at their original times and with the td session that wrote them. `--all` also brings closed issues, as done. The default path is `.todos/issues.db` in the current directory or the nearest parent that has one, and `--dry-run` shows what would come over without writing anything, leaving out what this host's copy of the ledger already holds.

The import maps td onto the ledger as follows:

- An epic becomes a `feature` labelled `epic` that waits on each of its child issues.
- td dependencies become dependencies. One that would make a cycle is skipped and listed.
- `P4` becomes `P3`.
- In-progress and in-review issues come over open: their claims were td sessions, which cannot submit or hand off in the ledger.
- A log type the ledger does not have becomes a progress log whose text starts with that type.
- An open or blocked issue deferred to a future date arrives parked for later (`deferred in td`), coming back on that date. A defer date that has passed is dropped, and the issue arrives open.
- Deleted issues, points, sprints, minor flags, due dates, and td's notes, boards, file links and work sessions are not carried over. The import lists how many of each it left behind.

Each item records the td issue it came from (`imported from td:td-1a2b3c` in `ledger work <item>`), and running the import again skips issues already imported, so an interrupted import can simply be rerun. Imported items are found work until they are linked to a clause of the person's.

## Keeping a ledger on another host

By default each host keeps its own ledger. To share one project's ledger between two paired hosts, say a laptop and a server, make one of them the **owner**:

```
agenthail ledger owner server     # run once on the laptop, per project
agenthail ledger owner            # show the owner, sync cursor and replay backlog
```

Nothing changes on upgrade: a project you do not assign stays local. You assign each project separately, and different projects can have different owners. If the laptop already holds history for the project, that history moves to the owner. This works only while the owner has no history of its own for that project.

Once a project has an owner:

- **Writes that create an ID** (goals, asks, clauses, cuts, decisions, work items, questions) go through the owner, so IDs never change.
  - If the owner cannot be reached, the write is refused. The message names the owner and says nothing was recorded; retry once it is back.
- **Everything else** (captured words, comments, logs, gate results) is recorded on the laptop first and replayed to the owner when it is reachable.
  - A laptop that travels keeps capturing offline and catches up on reconnect.
- **The laptop pulls the owner's log** and keeps its board in the owner's order. Both hosts end up with the same log, event for event, and every write lands once.

Assigning an owner runs in two phases, so a failure partway never leaves only one host thinking it owns the project:

1. The owner holds a pending claim, which is not ownership.
2. The laptop commits locally.
3. The laptop confirms, and the owner commits.

If the confirmation is lost, `ledger owner` says the assignment is not confirmed yet, and sync confirms it once the owner answers. If the owner refuses, the laptop keeps its ledger alone again. Moving a ledger to a different owner later is not supported.

### Checking that two hosts agree

On every pull, the owner sends a digest of its log through the laptop's new cursor. The laptop compares it with a digest of its own log over the same range. If the two differ, the laptop finds the first owner position where they diverge, and doctor reports it.

To check by hand, run on each host:

```
agenthail ledger digest [--project <key>] [--through <owner position>]
```

It prints the number of events and a sha256 through an owner position. The default is the sync cursor on a replica, and the end of the log on the owner. Run it with the same `--through` on both hosts; equal digests mean equal logs.

The digest covers, for each event in owner order:

- its position;
- its origin host and sequence;
- its type, ref and actor;
- its creation time;
- its payload bytes.

## Reading the ledger from another program

Sessions read the ledger with the `agenthail_ledger_read` MCP tool. Other clients use the daemon's HTTP API, which answers from the same code, so the two always agree.

```
GET /api/v1/ledger?projectKey=<key>                         # the board
GET /api/v1/ledger?projectKey=<key>&view=work&state=open&q=index
GET /api/v1/ledger?projectKey=<key>&view=item&id=<item or ask>
GET /api/v1/ledger/events                                   # ledger.changed, one per project when it changes
```

- **Views.** `view` is one of:
  - `board`, the default: capture state, sync state, counts, parked work by kind with how many can come back (`backlog`), the first page of goals, and the newest five provisional words with their total (`provisional`), how many messages were filed automatically and overridden (`autoFiled`), and, when a sorter is configured, its health (`sorter`);
  - one list, paged: `goals`, `asks`, `decisions`, `questions` or `work`;
  - `ready`, the ranked next work;
  - `backlog`, parked work with whether each item can come back and why (`kind`, `ready`), and `triage`, unowned open or blocked work idle `days` days (14 by default), as `agenthail ledger backlog` prints them;
  - `item`: one work item with its context, or one ask, as `agenthail ledger show --json` prints it.
- **Work filters.** With no filter, the `work` view lists every work item, settled and deleted ones included. With any filter, it selects as `agenthail ledger list` does: `q`, `state`, `type`, `priority`, `label`, `assignee`, `clause`, `found` and `all`.
- **Paging.** Lists are ordered by id and paged with `limit` (1 to 100) and the `nextCursor` of the previous page. A page starts after that id, so items added meanwhile don't shift it.
- **Caching.** Every answer carries an `ETag`, a hash of its exact bytes. Send it back as `If-None-Match` and an unchanged answer is `304 Not Modified`. Two different answers never share a tag, including after the daemon rebuilds a board from the log.
- **Sync.** On a host that replicates the ledger, the board's `sync` shows the owner, the pulled cursor, writes not yet replayed, the owner's head as last seen, and how far behind that is. A write from this host goes to the owner, and the daemon pulls right after, so a read that follows shows it. If that pull fails, the write still stands, since the owner recorded it. `sync.error` says why the pull failed until a later pull succeeds.
- **The change stream.** `GET /api/v1/ledger/events` is server-sent events, and keeps no history:
  - On connect, it sends a `ledger.changed` event with the current `version` for every project with a ledger. The version is the board's ETag.
  - After that, it sends one event per changed project at most once a second, however many events were written meanwhile. A client that reads slowly gets the newest version of each project it missed, and is never dropped.
  - A client that reconnects should refetch what it shows.

## Doctor

`agenthail doctor` reports on the ledger:

- **Prompt hooks:** one line per Claude config root whose prompt hook is missing or outdated, meaning terminal words are not captured live. It names the project whose ledger is enabled there.
- **Ledger sync:** one line per project kept on another host:
  - the owner and whether it is connected;
  - when it last synced, and how many owner events this host is behind;
  - how many local events are waiting to replay;
  - the last sync error;
  - the log check: whether the log matches the owner's through the cursor, or the first owner position where they differ.

`agenthail doctor --json` returns the same under `ledgerSync`. Each entry has these fields:

| Field | Meaning |
| --- | --- |
| `owner`, `ownerName`, `ownerState` | The owner host and whether it is connected |
| `cursor`, `ownerHead`, `lag` | How far this host has pulled, the owner's latest position, and the gap |
| `unreplayed` | Local events not yet replayed to the owner |
| `syncedAt`, `lastError` | The last successful sync and the last error |
| `log`, `logThrough` | `match`, `mismatch` or `unchecked`, and the owner position checked through |
| `firstDifference`, `logError` | The first owner position where the logs differ, or why the check failed |

A mismatch, a failed check, an unreachable owner or a sync error makes doctor exit non-zero.
