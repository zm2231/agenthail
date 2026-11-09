# Moving a session to another host

`agenthail move <session> --to <host>` hands an idle Claude conversation or
Codex thread to a paired host. The conversation keeps its id and its history. After the move,
the other host's Claude writes to it, and this host has no process, transcript
or alias for it. The row stays listed here as the other host's session, so
every read, send and action on it routes there, like any
[paired host's](hosts.md) session.

Only the source daemon drives a move. The target answers four requests, all
of them idempotent by move id. Both sides keep the move in `session_moves`.

## Protocol

1. **Hold.** The source refuses a session that another host owns, that
   another move holds, that has a delivery in flight, or the coordinator
   role. It also refuses a relay from the session that is tied to
   a channel, or that has a captured completion it has not forwarded yet,
   naming the relay. The Claude adapter then takes the
   conversation lock and stops the process that holds it the way
   [adopt](session-operations.md) does: it closes a host session only once
   idle, stops a background job, and stops a terminal process only with
   `--stop-terminal`. The lock is held until the move ends, so no Agenthail
   process here resumes the conversation in the meantime. The hold also marks
   the conversation as moving, so a send that finds the lock taken does not
   wait out the move: it is queued as held and leaves with the queue at the
   flip. A send that gets the lock only after the conversation left is held
   the same way. With the session
   held, the source reads its completions once more and its relays forward
   what they have not; a completion they still cannot forward refuses the
   move when it is declared.
2. **Declare.** The source reads where the conversation's working directory
   sits in its repository: the remote's canonical key, the folder inside the
   repository, and a commit that some remote branch contains. Only a clean
   tree can move. It also records the transcript's size and SHA-256, the
   alias, the title, and the model and permission mode the conversation last
   ran with in this machine's Claude host. It lists the active relays from the
   session: each relay's target, pattern, handoff, whether it fires once, and
   the last goal it forwarded. The target of each relay is named with the host
   that runs it. It reads the project's Claude settings: the entry for the
   working directory in the `.claude.json` of the root the conversation runs
   under. It keeps only folder trust, `allowedTools`, the MCP server keys
   (`mcpServers`, `enabledMcpjsonServers`, `disabledMcpjsonServers`,
   `mcpContextUris`), the CLAUDE.md include approvals, `ignorePatterns` and
   the onboarding flags. Per-machine state such as costs and the last session
   stays. Each MCP server goes without its `env` and `headers`, which may
   hold credentials. The move result names those servers so the person adds
   their credentials again on the target. It lists the channels the session
   belongs to, each named with the host that keeps it: the source for its own
   channels, or the host a channel came from when the session moved here
   before. A declaration larger than a host reads for a prepare (140 KiB)
   refuses the move. The move is recorded `preparing`, in a transaction that
   refuses it if the session's relays or channels are no longer the ones it
   listed, or its relays have a completion they have not forwarded. Until the
   move ends, the relays and channels of the session stay as declared: adding
   or removing a relay, joining or leaving a channel, a coordinator handover
   that moves relays to or from the session, and forwarding a completion are
   all refused.
3. **Prepare** (`POST /api/v1/session-move/prepare`). The target finds its own
   checkout of the repository through its added projects and the projects of
   its sessions. When the checkout lacks the commit, it fetches. It refuses
   the move if it has no checkout or more than one, if the commit is missing
   after the fetch, if the working directory's project folder name would be
   longer than Claude writes verbatim (200 characters), or if it already
   holds a transcript of the conversation that the incoming one does not
   extend, if a relay's target is not a session it sees on the host the
   source named, or if a channel is kept on a host it has not paired. When
   project settings come with the move, it also refuses if the landing root's
   `.claude.json` is not a JSON object or its `projects` is not an object.
   Otherwise it records the move `prepared` and keeps the declaration, and
   answers with what it can carry (`relays`, `channels`, `claude-project`). A
   target that does not name one the move needs (an Agenthail too old to take
   it) fails the move before any file leaves, naming what it cannot take. The
   files that arrive later are held to that declaration.
   They cannot redefine it.
4. **Archive.** Still holding the lock, the source moves the conversation's
   files out of `projects/` and `tasks/` into
   `<root>/agenthail-moved/<id>/<move id>/`: `<id>.jsonl`, the `<id>/`
   folder of subagent transcripts and tool results, and `tasks/<id>/`. Only
   then does it record the move `sent`. A sent move's conversation is
   therefore out of reach even after a crash ends the hold: Claude and
   Agenthail read conversations only from `projects/` and `tasks/`, and
   Agenthail refuses to resume a conversation that has no transcript, as
   Claude does. Nothing in Agenthail reads `agenthail-moved/` outside the
   move that wrote the folder. Once the move is done, the folder is only a
   copy to recover from by hand. Each move has its own folder, so a later
   move of the same conversation never mistakes an earlier move's copy for
   its own.
5. **Transfer** (`POST /api/v1/session-move/transfer`). The source streams the
   archive as a tar over the pairing. The body is never held in memory on
   either side, and a transfer sent again resends everything. The target
   records the move `received` and makes the working directory, which is the
   main checkout when it sits clean at the commit, and otherwise a worktree
   at `<checkout>/.worktrees/move-<first 8 characters of the move id>`. It
   then reads the tar as untrusted input:
   - It accepts only regular files and folders.
   - It refuses absolute paths, `..`, symlinks, hard links, and any name
     outside `<id>.jsonl`, `<id>/` and `tasks/<id>/`.
   - It writes into `<root>/.agenthail-move-<move id>/` inside its primary
     config root.
   - It checks the transcript against the declared size before writing it
     and against the declared digest after.
   - It reads the tar only while it holds the conversation and the move is
     still `received`, so a move that the source has already aborted places
     nothing.

   Before it reads the tar, it merges the project's Claude settings into the
   landing root's `.claude.json`, under its own checkout's folder. Both sides
   key the entry by the folder's physical path, as Claude does (on macOS,
   `/private/tmp/...` for a folder under `/tmp`). It merges the
   way a move between accounts on one machine does: it adds what is missing,
   object members and array items included, and never overwrites a value
   already there. Everything up to recording the move undoes the merge if it
   fails, removing only what it added. A merge repeated by a retried transfer
   adds nothing.

   Only then does it rename the files into
   `<root>/projects/<folder for the working directory>/` and `<root>/tasks/`.
   It resumes the conversation in its Claude host with the declared model,
   permission mode and title. A copy of the conversation already here, which
   the arriving one extends, is kept in `agenthail-moved/<id>/<move id>/previous/`.
   If Claude refuses to start, the target sets the placed files aside in its
   own `agenthail-moved/<id>/<move id>/` and records the move `aborted` before
   it answers, so nothing there finds a copy to run beside the source's and a
   repeated transfer is refused. When relays come with the session, it then
   reads the conversation's completions so far, so its relays forward only
   what comes after the move. Last, in one transaction, it registers the
   session as local, takes over the row the source mirrored there, binds the
   alias if it is free, creates the relays, records which host keeps each
   channel that is not its own, and records the move `resumed`.
6. **Flip.** The source, in one transaction:
   - gives the row to the target, clearing its process, transcript and local
     flags;
   - drops the alias;
   - deletes the relays from the session, which the target now runs, and
     the completions captured for them. Relays to the session stay: they
     deliver to the target through held sends, like any relay to a paired
     host's session;
   - deletes its records of channels other hosts keep for the session. The
     session stays a member of this host's own channels, which now reach it
     on the target through held sends;
   - turns each queued message into a send held for the target, under its
     delivery key, and fails a queued message with attachments and any
     queued operation the target cannot take as a send;
   - writes a mirrored row that says the session moved and is waiting for the
     target to report it;
   - records the move `done`.

   Held sends then drain to the target, and the target's catalog replaces the
   placeholder row. If the target reported the session while it was still
   local here, its row was hidden; the flip shows it at once.

   A message queued for the session after the flip becomes a send held for
   the target as well. A paired host that has not yet heard of the move may
   still send to the source; the source does not pass that on, but refuses it
   with code `moved` and `movedToHost`, and the sending host sends it once
   more, straight to the target.

On the target, every request must come from a paired host's token, which has
the `control` and `host` scopes. The token names the source; the request body
does not. `GET /api/v1/session-move?id=` answers `none` for a move this host
has no record of from that host.

## Channels after a move

A channel stays on the host that keeps it; only the session moves.

- A post to the channel on that host reaches the moved session on its new
  host through a held send, like a post to any member on a paired host.
- A post from the moved session to a channel it took with it goes to the
  host that keeps the channel. While that host is unreachable, the post is
  held and delivered once it reconnects, under the post's idempotency key.
- A session can be in channels of the same name on several hosts, and a move
  carries each of them. A bare name that names channels on more than one
  host is refused with their qualified names, such as `home::#builders`; a
  name qualified with a host always picks that host's channel.
- The keeping host leaves the poster out of its own post, and accepts a
  post from a paired host's session only while that session is a member.
  When the channel is gone, or the session is no longer a member, the new
  host stops sending its posts there.
- A relay tied to a channel still refuses the move.

## Failure model

The source resumes the conversation locally only once it can prove the
target never will. It proves that by having the target record the move as
`aborted`, after which any late or repeated request for it is refused. Until
then the move stays `sent`. Nothing here delivers to the conversation, since
local queue claims skip it, and nothing resumes it, since its files are
archived before the move is `sent`. Each time the target reconnects, the
source asks it where the move stands. The target answers an abort only while
it holds the conversation: it refuses with `move_running` when Claude already
runs it there, and otherwise sets aside any copy it placed before it records
`aborted`.

| Where it stopped | What recovery reads | What it does |
|---|---|---|
| Before the move was recorded | nothing | Nothing. The process the hold stopped is gone, and the next send resumes the conversation here as usual. |
| `preparing` (target refused, or the daemon stopped) | the source's own record | Puts back any files it had archived and marks the move `failed`, at once or when the daemon starts. No file had been sent. A target that had recorded it `prepared` drops that record when the next move of the conversation from this host is prepared. |
| `sent`, before the transfer | target: `prepared` | Transfers. |
| `sent`, transfer unanswered | target: `received` | Transfers again. A target that already runs the conversation only checks that what arrived begins what runs there. |
| `sent`, target resumed it but the source stopped before the flip | target: `resumed` | Flips. |
| `sent`, target refused the transfer before starting Claude | answer `409 move_refused`, or `move_aborted` to a repeated transfer | Asks the target to abort, which it answers as `aborted` again if it already recorded that. Then puts the files back and marks the move `failed`. |
| `sent`, target could not set aside a copy it placed | answer `502 move_unknown`, then `500 move_unsettled` to the abort | Leaves the move unresolved and asks again on the next reconnect. |
| Putting the files back fails | the source's record | Leaves the move as it was and tries again on the next reconnect, or the next start for a `preparing` move. |
| `sent`, target has no record | target: `none` | Same as a refusal. |
| `sent`, target unreachable | nothing | Leaves the move unresolved. `agenthail move list --unfinished` shows it. |
| `done` | the source's record | Nothing. |

A target refuses to abort a move whose conversation already runs there
(`move_running`), so a move whose resume outcome was unknown is finished, not
undone.

## What the target machine needs

- **A checkout of the repository**, added with `agenthail projects add` or
  holding one of its sessions, that can fetch the commit from its git remote.
  It does not need to push.
- **Its Claude host.** The target resumes the conversation in the Claude host
  its daemon runs.
- **A Claude config root to place it in.** Without `--root` that is the
  primary root, the one its daemon's `CLAUDE_CONFIG_DIR` names, `~/.claude`
  when unset, and Claude is launched without naming a root. With
  `--root <name>` it is that root of the target, which must be configured
  there. A root behind an account proxy is accepted. The target
  answers the prepare with the root it will use; a source whose target
  answers without it (an Agenthail too old to choose) fails the move before
  any file leaves, so a move never lands in another root than the one named.
- **Its own Claude sign-in.** The move carries no account. The moved session
  uses whatever login or API configuration the target's root has, like any
  session started there.

The conversation's tasks (`tasks/<id>/`) arrive with it, and a turn on the
target lists them with TaskList, because the Claude host turns on Claude's
task tools for every session it runs. A target without Claude Remote Control
runs the moved session without a claude.ai listing, as it runs all its
sessions.

## Codex threads

A Codex thread moves by the same protocol, with these differences.

- **Hold.** The Codex adapter takes a per-thread lock that every Agenthail
  write to the thread checks while it holds the Codex write lock. The lock
  file, `~/.agenthail/codex-threads/<thread>.lock`, exists only while a move
  holds it; a write removes one that no move holds. A send,
  steer or setting change during the move is refused as unavailable, so
  queued deliveries retry and, after the flip, go to the target. The thread
  must be idle: no running turn, nothing asked of the person, and no
  message in Codex's own queue, since Codex starts a queued message as soon
  as an idle thread resumes. A thread that has spawned subagent threads, a
  subagent thread, and a forked thread whose history continues another
  thread's rollout are refused; the refusal names the subagent threads.
- **Declare.** The current rollout's size and SHA-256 are the transcript;
  the thread's other rollout files, which `thread/revert` leaves, are the
  parts. The thread's name and goal go in the declaration's `state`, since
  Codex keeps them outside the rollout.
- **Archive.** `thread/archive` unloads the thread from the runtime that has
  it loaded, Codex Desktop or the app-server daemon, and moves its rollouts
  to `archived_sessions/`. Codex then refuses to resume it. Archiving ends a
  running turn, so it rechecks that the thread is idle under the Codex write
  lock first. A Codex process outside Agenthail that holds the thread makes
  the archive fail and the move with it. The archived files are listed in
  `$CODEX_HOME/agenthail-moved/<id>/<move id>/archived.json`, and only that
  move's restore runs `thread/unarchive`.
- **Transfer.** The tar holds only the thread's rollout files, flat and
  regular. The target places each in `sessions/<date in its name>/`, keeping
  any shorter copy under `agenthail-moved/<id>/<move id>/previous/`, and
  resumes it in its Codex app-server daemon with `thread/resume` in the
  mapped working directory. It then sets the name and goal again; both are
  safe to repeat when a transfer is retried. A copy the target archived when
  the thread last left it must begin the arriving rollout, and is unarchived
  before it is replaced, so a thread can move back.
- **Abort.** The target sets aside the files it placed, named in
  `placed.json`, into the move's `dropped/` folder, unless its app-server has
  the thread loaded, which answers `move_running`.

A goal's time and tokens used do not carry; the target starts them at zero.
There is no `--root` for Codex threads.

## Moving to another account on the same host

`agenthail move <session> --root <name>` moves an idle conversation to
another Claude config root of the host it runs on, so it continues under that
root's login. There is no transfer. The source holds and stops it as above,
records the move in `session_moves` with this host as its peer, and the
Claude adapter, still holding the conversation lock:

1. merges the project's entry from the source root's `.claude.json` into the
   target root's: folder trust, allowed tools and local MCP servers. Only
   what the target lacks is added; a value it already holds is never
   changed. The file is rewritten only if it is as it was read, since
   running Claude processes write it too;
2. archives the conversation's files into the source root's
   `agenthail-moved/<id>/<move id>/`, as a host move does;
3. renames each part into the target root, the transcript last, refusing
   when the target already holds any of them;
4. resumes the conversation in the Claude host under the target root.

A step that fails puts the files back where they were and removes exactly
what step 1 added. A resume whose outcome is unknown leaves the conversation
in the target root and the move done. A daemon that stopped mid-move settles
it when it starts: a move whose transcript reached the target finishes there,
and any other goes back to the source root. A root behind an account proxy
is a valid target: the session continues under whichever account the proxy
chooses, which is how a person out of quota on a signed-in root reaches the
pool.

A move to another host carries no project config; the target's root starts
without the source's trust, allowed tools or local MCP servers.

A move that succeeds releases the session's usage-limit stop, if it is
still waiting, with the resume notice, so the session continues at once
under its new root. A canceled or lapsed stop stays held.

### Moving at a usage limit

While a limit stop waits and the host has two or more roots, the dashboard
projection's `limitResume.moveTo{root, account, reason}` names the target
`claudepick.MoveTarget` chooses from the cached usage readings: a claude.ai
root other than the stop's, with a reading, under 90% of its session and week
(or the proxy's own ceiling), the least loaded first. Proxy, API-key, signed-out and unread roots are never
offered. The offers are computed for every waiting stop at once and kept
five seconds, or until a stop changes. The `limit-move` action
(`configRoot` optional, defaulting to the offer) runs the move above, and
refuses a session whose stop is not waiting or a `configRoot` behind a proxy,
so a stale or hand-built offer never lands in a proxy root;
`agenthail move --root` is the way there. It claims the waiting stop
first; while the claim holds, `limit-resume` and `limit-resume-cancel` on
that session are refused, so a cancel either lands before the move starts or
after it finishes. Nothing moves a stopped session unless the person asks.

## Not in this slice

A working tree with uncommitted changes is refused. Moving one, Mac and
iPhone controls for a move to another host, and Codex sessions come later.
