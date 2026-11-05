# The intent ledger

The intent ledger keeps what the person asked for next to the work agent sessions do about it. It is per project, keyed by the repository's remote, so every session working on the same repository shares one ledger.

Every change is an event in an append-only log; the board you read is rebuilt from that log. Nothing is edited in place: a correction is a new event that supersedes an old one.

## Capture and scope

Every project with a lead captures the person's words to that lead, from the moment it became the lead, and asks nothing of it: the words are recorded, and the lead attaches them only if it chooses to. Its forks are captured the same way from their creation. The bare `agenthail ledger` command lists the newest five of those words with a count; `--all` lists every one.

```
agenthail ledger enable              # capture the person's words to every session in this project, and ask each to attach them
agenthail ledger enable @session     # the same for one session, and the forks made from it afterwards
agenthail ledger disable             # capture nothing in this project, not even the lead's words
agenthail ledger disable @session    # take back a session's grant
```

A project's enable or disable is an event in its ledger, so it holds on every host that keeps or replicates the ledger. A session's grant stays on the host where the session runs, since that is where its words are captured.

Words are captured from three sources, and each message is recorded once even when more than one source sees it:

- **The prompt hook.** Terminal prompts are captured as they are submitted. Run `agenthail hooks install` once per Claude config root.
- **Delivery.** Messages the person sends through Agenthail are captured when they are delivered.
- **The session journal.** Prompts the hook missed are picked up from the session's transcript later.

A captured message is an *utterance*, identified by a ref such as `claude-prompt:<id>`. If a message is larger than one ledger event can hold (4 MiB), the ledger keeps its beginning with a visible marker, its original size and its sha256, under the same ref.

## Attaching words: goals, asks and clauses

In an enabled project, or for a granted session, the session the person spoke to attaches each message, so the ledger records what it means. After capture, the session is told the utterance ref. It replies with the readback that `attach` prints, so the person sees how their words were recorded.

```
agenthail ledger goal "Search"                                   # create a goal, or show it
agenthail ledger attach <utterance> --as new-ask --to <goal> --clause "build search"
```

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
| `conversation` | Not a request; nothing is added |

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

The bare `agenthail ledger` command shows goals, open asks, decisions and words not attached yet. `ledger goals`, `ledger asks` and `ledger ask <id>` show more detail.

### The turn gate

At the end of a turn, a session that owns clauses or has unattached words in a project that asks it to attach (enabled, or granted to the session) is held until it does one of these:

- attaches the words;
- reports progress on its clauses;
- records a wait;
- depends on someone else's work.

The gate does not hold a session while the person has spoken to it in the last 10 minutes. After 3 holds on the same item, the item is waived so a session is never stuck.

### Narrowing checks and cuts

`agenthail ledger check <goal>` queues a narrowing check. A sandboxed session compares the goal's clauses with the person's words and raises what is missing as cuts. A cut stays on the board until the person acknowledges it (`attach --as reacts --ack`). It is withdrawn when a clause taken from the words it was raised from covers it (`attach --covers <cut>`).

## Questions

A session that needs the person to decide something records a question instead of guessing:

```
agenthail ledger question --to <goal|ask|clause|project> "Index titles only, or bodies too?" \
  --recommendation "titles only" --default "titles only" --deadline 4h --kind scope [--keeps-breadth]
```

The person answers with `attach --as answers`. `agenthail ledger questions [--all]` lists open questions, and with `--all` the answered ones too.

If nobody answers by the deadline:

- A `scope` or `framing` question with `--keeps-breadth` applies its default and records the decision.
- Any other `scope` or `framing` question records its default as a cut the person must acknowledge, and escalates.
- An `outward` question never acts on its own. It only escalates: a notification on this machine and on paired devices, and a notice to the session that asked.

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

An item moves through `open`, `in_progress`, `blocked`, `in_review` and `done`. Work that will not be done is closed instead, with a reason: `agenthail ledger work close <item> --reason "duplicate of <item>"` (the reason starts with `duplicate`, `wontfix` or `obsolete`). `delete` hides an item from every list and `restore` brings it back; `reopen` takes a done or closed item back to open. Closing or deleting an item frees the items that wait on it, and closing work never closes the clause above it.

Two kinds of notes attach to an item:

- `agenthail ledger work comment <item> "..."`: anyone can comment.
- `agenthail ledger work log <item> "..." [--type progress|blocker|decision|hypothesis|tried|result]`: the running log of the session holding the item.

`agenthail ledger work <item>` shows everything a session picking the item up needs in one read: its fields, the clause it serves with its ask, goal and the person's own words, where it was found, what it waits on and what waits on it, any handoff, and its full timeline of state changes, edits, comments and log entries. `agenthail ledger work` lists unfinished items; filter with `--state`, `--type`, `--priority`, `--label`, `--found` or `--mine`, and add done and closed ones with `--all`. Comments and logs stay on the item when the clause above it is cut, and they survive a rebuild. When the person's words behind a clause are retracted, asked work added under that clause goes with them, notes included, and the board lists it among the retractions; found work that was linked to the clause goes back to found.

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
- Deleted issues, points, sprints, minor flags, due and defer dates, and td's notes, boards, file links and work sessions are not carried over. The import lists how many of each it left behind.

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
