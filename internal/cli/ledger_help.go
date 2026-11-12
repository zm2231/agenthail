package cli

const ledgerHelp = `Intent ledger (agenthail ledger <command>; agenthail ledger help prints this):

 Work items (each is also "ledger work <command>"; an <item_id> can be an imported item's td id):
  ledger list [filters] [--all] List unfinished work items; --all adds done and closed ones
  ledger search <words>... [filters]
                                 Items with every word in their id, td id, text, description, acceptance,
                                 labels, comments or logs, in any state but deleted
                                 Filters: --state <s,...> --type <t,...> --priority <P0,...> --label <l> (repeat:
                                 all must match) --assignee <session> | --mine, --clause <id>, --found,
                                 --sort id|priority|created|updated, --limit <n>
  ledger show <item_id|td_id|ask_id>
                                 Everything about one item in one read: fields, the clause with the person's
                                 words, where it was found, dependencies both ways, handoff and timeline
  ledger words <utterance_id> [--context N] [--json]
                                 One captured message: what the person typed, what they quoted, and the reply
                                 it answered; --context N adds the N turns before and after it, read from the
                                 session's journal on the host that captured it (other hosts refuse)
  ledger add "<text>" [--clause <clause_id> | --found-in <item|clause>] [--type task|bug|feature]
             [--priority P0-P3] [--description "..."] [--acceptance "..."] [--label <l>]...
                                 Add asked work under a clause, or found work with none (create, new)
  ledger edit <item_id> [--text|--type|--priority|--description|--acceptance "..."] [--label|--unlabel <l>]
                                 Change an item's fields (update)
  ledger start <item_id>         Claim an item; waits for its dependencies (claim)
  ledger log <item_id> "<text>" [--type progress|blocker|decision|hypothesis|tried|result]
                                 Add to the running log of an item you hold
  ledger comment <item_id> "<text>"
                                 Comment on an item from any session
  ledger handoff <item_id> --done/--remaining/--decisions/--uncertain "..."
                                 Hand off a claimed item with its state; it reopens for the next session
  ledger park <item_id>... --usage <provider[:root]>|--later "<why>"|--person "<question>"|--idea --utterance <ref>
                                 Set work aside with why it waits; takes the handoff flags and --until
  ledger unpark <item_id>...     Bring parked work back to open
  ledger backlog [--kind usage,idea,later,person] [--ready]
                                 Parked work, the items that can come back first
  ledger backlog --triage [--days N]
                                 Open or blocked work nobody holds, idle N days (14), to park in a batch
  ledger block <item_id> --reason "..." | ledger unblock <item_id>
  ledger review <item_id>        Submit a claimed item for review (submit)
  ledger approve <item_id> | ledger reject <item_id> --reason "..." [--utterance <id>]
                                 Review submitted work: another session, or the person's own words
  ledger close <item_id> --reason "done: <PR, commit or where it landed>"
                                 Mark unclaimed work done that was finished elsewhere; claimed work goes
                                 through review
  ledger close <item_id> --reason "duplicate|wontfix|obsolete ..."
                                 Close work that will not be done, without review
  ledger reopen <item_id>        Take a done or closed item back to open
  ledger delete <item_id> | ledger restore <item_id>
                                 Hide an item from every list, or bring it back
  ledger dep <item_id> --on <item_id>
                                 Record that one item waits on another
  ledger link <item_id> <clause_id>
                                 Hang found work off a clause of the person's
  ledger ready | ledger next     Open items whose dependencies are settled, most urgent first
  ledger critical-path           Longest chain of unfinished items

 The person's intent:
  ledger [--all|--provisional]   Show goals, open asks, decisions and the person's provisional words for the current project;
                                 --provisional lists only the provisional words
  ledger goals                   List goals with the clauses gathered under each
  ledger goal <name>             Create a goal, or show the one with that name
  ledger asks | ledger ask <ask_id>
                                 List all asks, or show one with its clauses
  ledger attach <utterance_id> [--to <id|project>] [--clause "..."] [--as <how>] [--and <utterance_id> ...]...
                                 Attach the person's words, several fragments in one call with --and:
                                 new-ask, extends, constrains, refines,
                                 reacts (--adopt|--reject a clause, --ack a cut), delegates [--session],
                                 decides, idea (--clause "<title>" [--goal|--plan|--first-test "..."],
                                 parked found work in the person's words), conversation;
                                 --covers <cut> withdraws a checker's cut
                                 with a clause from the words it was raised from. Without --as it
                                 is read from the flags: words on a clause or ask extend the ask,
                                 on a goal or the project start a new ask, on a question answer it;
                                 no --clause is a reaction, and neither --to nor --clause conversation.
                                 Rewording (refines), acking (--ack), an answer that rewords or decides
                                 (answers) and constrains/decides need saying
  ledger propose --to <id|project> --clause "..."
                                 Propose clauses; they bind once the person adopts them
  ledger question --to <id|project> "<question>" --recommendation "..." --default "..."
                  --deadline <30m|4h|2d> --kind <scope|framing|outward> [--keeps-breadth]
                                 Ask the person; they answer its ask, or with attach --as answers.
                                 Unanswered, only a --keeps-breadth default applies itself;
                                 any other waits for the person's ack
  ledger questions [--all]       List open questions (--all includes answered ones)
  ledger own <clause_id> [--session <id>]
                                 Take ownership of a clause (or give it to a session)
  ledger progress <clause_id> --ref <commit|file|PR|evidence>
                                 Report self-reported progress on a clause you own
  ledger wait <clause_id> --condition "..." --deadline <30m|4h|2d>
                                 Declare what a clause you own is waiting on
  ledger check <goal_id>         Queue a narrowing check: a sandboxed session compares the goal's
                                 clauses with the person's words and raises what they miss as cuts
  ledger check-report <run> (--none | --missing "<breadth>" --in <utterance_id> ...)
                                 The narrowing check session's own report; refused from any other session

 Setup:
  ledger enable [@session] [--attach prompt|checkpoints]
                                 Capture the person's words to every session in this project, or only for
                                 one session and its later forks; without it, only the lead's words are
                                 captured. Words stay provisional until attached and nothing waits on them;
                                 each message tells the session how many are provisional, or with
                                 --attach checkpoints only at points it already reaches (work add, claim
                                 or submit, a start or compaction, and ledger)
  ledger disable [@session]      Capture nothing in this project, or take back a session's grant
  ledger owner [<host>]          Keep this project's ledger on a paired host, which then plans every write and
                                 syncs its log back; without a host, show the owner, sync cursor and replay backlog
  ledger import td [<path>] [--all] [--dry-run] [--json]
                                 Bring a td database's open issues over as found work, with their comments,
                                 logs, handoffs, reviews and dependencies; --all adds closed ones as done.
                                 The default path is .todos/issues.db here or above. Running it again skips
                                 what is already imported; --dry-run leaves out what this host's copy holds
  ledger digest [--through N]    Hash of this host's ledger log in owner order through owner position N (default:
                                 the sync cursor, or the log's end on the owner); equal on two hosts means equal logs
`
