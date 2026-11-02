# The intent ledger

The intent ledger keeps what the person asked for next to the work agent sessions do about it. It is per project, keyed by the repository's remote, so every session working on the same repository shares one ledger.

Every change is an event in an append-only log; the board you read is rebuilt from that log. Nothing is edited in place: a correction is a new event that supersedes an old one.

## Capture and scope

Capture is off until you turn it on.

```
agenthail ledger enable              # capture the person's words in this project
agenthail ledger enable @session     # only for one session, and the forks made from it afterwards
agenthail ledger disable [@session]  # stop capturing
```

Words are captured from three sources, and each message is recorded once even when more than one source sees it:

- **The prompt hook.** Terminal prompts are captured as they are submitted. Run `agenthail hooks install` once per Claude config root.
- **Delivery.** Messages the person sends through Agenthail are captured when they are delivered.
- **The session journal.** Prompts the hook missed are picked up from the session's transcript later.

A captured message is an *utterance*, identified by a ref such as `claude-prompt:<id>`. If a message is larger than one ledger event can hold (4 MiB), the ledger keeps its beginning with a visible marker, its original size and its sha256, under the same ref.

## Attaching words: goals, asks and clauses

The session the person spoke to attaches each message, so the ledger records what it means. After capture, the session is told the utterance ref. It replies with the readback that `attach` prints, so the person sees how their words were recorded.

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

At the end of a turn, a session that owns clauses or has unattached words is held until it does one of these:

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

Work items break a clause into tracked pieces of work.

```
agenthail ledger work add <clause> "add the index"
agenthail ledger work claim <item>          # waits for the item's dependencies
agenthail ledger work block <item> --reason "..."
agenthail ledger work submit <item>         # in_review
agenthail ledger work approve <item>        # or reject; done after approval
agenthail ledger work dep <item> --on <other item>
agenthail ledger work handoff <item> --done "..." --remaining "..."
```

An item moves through `open`, `in_progress`, `blocked`, `in_review` and `done`.

Two kinds of notes attach to an item:

- `agenthail ledger work comment <item> "..."`: anyone can comment.
- `agenthail ledger work log <item> "..." [--type progress|blocker|decision|hypothesis|tried|result]`: the running log of the session holding the item.

`agenthail ledger work <item>` shows the item with its full timeline of state changes, comments and log entries. Comments and logs stay on the item when the clause above it is later retracted or cut, and they survive a rebuild.

To pick up work:

- `agenthail ledger work ready` lists open items whose dependencies are done, the ones that unblock the most first.
- `agenthail ledger work next` shows the first of those.
- `agenthail ledger work critical-path` shows the longest chain of unfinished items.

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
