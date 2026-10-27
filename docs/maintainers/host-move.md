# Moving a session to another host

`agenthail move <session> --to <host>` hands an idle Claude conversation to a
paired host. The conversation keeps its id and its history. After the move,
the other host's Claude writes to it, and this host has no process, transcript
or alias for it. The row stays listed here as the other host's session, so
every read, send and action on it routes there, like any
[paired host's](hosts.md) session.

Only the source daemon drives a move. The target answers four requests, all
of them idempotent by move id. Both sides keep the move in `session_moves`.

## Protocol

1. **Hold.** The source refuses a session that another host owns, that
   another move holds, that has a delivery in flight, an active relay, a
   channel or the coordinator role. The Claude adapter then takes the
   conversation lock and stops the process that holds it the way
   [adopt](session-operations.md) does: it closes a host session only once
   idle, stops a background job, and stops a terminal process only with
   `--stop-terminal`. The lock is held until the move ends, so no Agenthail
   process here resumes the conversation in the meantime.
2. **Declare.** The source reads where the conversation's working directory
   sits in its repository: the remote's canonical key, the folder inside the
   repository, and a commit that some remote branch contains. Only a clean
   tree can move. It also records the transcript's size and SHA-256, the
   alias, the title, and the model and permission mode the conversation last
   ran with in this machine's Claude host. The move is recorded `preparing`.
3. **Prepare** (`POST /api/v1/session-move/prepare`). The target finds its own
   checkout of the repository through its added projects and the projects of
   its sessions. When the checkout lacks the commit, it fetches. It refuses
   the move if it has no checkout or more than one, if the commit is missing
   after the fetch, if the working directory's project folder name would be
   longer than Claude writes verbatim (200 characters), or if it already
   holds a transcript of the conversation that the incoming one does not
   extend. Otherwise it records the move `prepared` and keeps the
   declaration. The files that arrive later are held to that declaration.
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

   Only then does it rename the files into
   `<root>/projects/<folder for the working directory>/` and `<root>/tasks/`.
   It resumes the conversation in its Claude host with the declared model,
   permission mode and title. A copy of the conversation already here, which
   the arriving one extends, is kept in `agenthail-moved/<id>/<move id>/previous/`.
   If Claude refuses to start, the target sets the placed files aside in its
   own `agenthail-moved/<id>/<move id>/` and records the move `aborted` before
   it answers, so nothing there finds a copy to run beside the source's and a
   repeated transfer is refused. Last,
   in one transaction, it registers the session as local, takes over the row
   the source mirrored there, binds the alias if it is free, and records the
   move `resumed`.
6. **Flip.** The source, in one transaction:
   - gives the row to the target, clearing its process, transcript and local
     flags;
   - drops the alias;
   - turns each queued message into a send held for the target, under its
     delivery key, and fails a queued message with attachments and any
     queued operation the target cannot take as a send;
   - writes a mirrored row that says the session moved and is waiting for the
     target to report it;
   - records the move `done`.

   Held sends then drain to the target, and the target's catalog replaces the
   placeholder row. If the target reported the session while it was still
   local here, its row was hidden; the flip shows it at once.

On the target, every request must come from a paired host's token, which has
the `control` and `host` scopes. The token names the source; the request body
does not. `GET /api/v1/session-move?id=` answers `none` for a move this host
has no record of from that host.

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
  there and signed in directly, not through an account proxy. The target
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
is refused as a target, because the proxy chooses the account itself.

A move to another host carries no project config; the target's root starts
without the source's trust, allowed tools or local MCP servers.

## Not in this slice

A working tree with uncommitted changes is refused. Moving one, move
controls in the Mac and iPhone apps, and Codex sessions come later.
