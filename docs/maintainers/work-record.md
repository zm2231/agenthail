# Work record: end of turn, worktrees, pull requests

Agenthail keeps a record of the work in each session it launches, beside the session catalog, and gives agents tools built on it. Three capabilities, in order: a structured end of turn, a worktree per task, and pull requests linked to sessions and watched.

## What agents actually use

Measured over every Claude transcript in one config root: about 1,400 `agenthail` CLI calls (1,062 of them `send`, 115 `help`) against 9 MCP calls. Two causes, both observed:

- The start card teaches seven CLI lines and no MCP tool (`internal/agentcard/render.go:21-37`).
- Claude defers every `mcp__agenthail__*` tool behind ToolSearch, so an MCP call costs a load step; Bash is always loaded.

The design follows from that:

1. **Mechanics before instructions.** Most of the three capabilities need no agent call at all: the Stop hook asks for the end of turn, `gh pr create` links its PR, a merged PR settles the session. What an agent must know fits in two card lines.
2. **One service, three faces.** The work record lives in a new `internal/workrecord` package over the registry. Writes go through `performAction`, the path the CLI (`internal/cli/daemon_api.go:110`) and MCP writes (`internal/daemon/mcp_write.go:29-46`) already share. One spec table per tool (name, one-line use, input struct) registers the MCP tool, builds the CLI help, and supplies the card line, so the three cannot drift.
3. **Hosted sessions are taught the tools.** A session holding a live Agenthail MCP credential (every Claude host session and every Codex thread Agenthail starts) gets a card naming MCP tools; a terminal session without the server keeps the CLI card. A small core is always loaded through `_meta["anthropic/alwaysLoad"]: true` (supported by Claude Code 2.1.316): `agenthail_send`, `agenthail_create`, `agenthail_wait`, `agenthail_result`, and later `agenthail_end_turn` and `agenthail_pr`. Everything else stays deferred. `agenthail_end_turn` is MCP only, because the Stop hook and the clients need its structure and validation, and it is named in `CLAUDE_CODE_TERMINAL_MCP_TOOLS` so its result ends the turn without a filler reply.
4. **A budget test.** A Go test pins the bytes of the rendered card, the always-loaded tool JSON and the whole tool list. Growth fails the test until the pinned numbers are raised in the same change.

### Text injected

| Piece | Today | After |
|---|---|---|
| Start card (once per session) | 1,666 bytes (test fixture) | slice 0: 1,702 for the tool card, CLI card unchanged; later slices +300 at most |
| Always-loaded tool schemas, Claude | 0 | slice 0: 3,993 bytes for send, create, wait, result; then `agenthail_end_turn` and `agenthail_pr` |
| MCP tool list JSON | 18,849 bytes, 26 tools | slice 0: 19,001 (the `_meta` marks); later +2 tools, at most +4 KB |
| Nudge, only when a turn skips the call | 0 | at most 300 bytes, once per turn |
| PR wake, only on a change that needs the agent | 0 | at most 600 bytes each |

Whether Codex defers MCP tools is not yet verified; if it does not, the two new tools cost Codex their full schema each turn, which is why the PR tool is one action tool rather than five.

## Data model

Tables beside the catalog, keyed by session id, one migration per slice. Each takes the next free schema number when it lands, after rechecking main and every open PR.

**`sessions.pinned_at`, `sessions.stopped_by_person_at`, `keep_alive`** (pinned sessions)

| Column | Meaning |
|---|---|
| `sessions.pinned_at` | unix ms, null when unpinned; set by `session-pin`, cleared by `session-unpin` |
| `sessions.stopped_by_person_at` | the person stopped it; cleared by their next send or resume |
| `keep_alive(session_id, failures, next_at, failed_at)` | backoff state; `failed_at` set after the fifth failure in 30 minutes |

**`turn_ends`**, primary key `(session_id, started_at)`

A row opens when the turn's `UserPromptSubmit` hook runs (Claude host, and Codex with its hooks installed), which also drops any older row that never ended, so a session that never calls the tool holds one row at most. `agenthail_end_turn` fills the open row, or adds one when there is none. The observer binds `completed_turn_id` to the newest row when it sees a turn complete. A call is a repeat, and refused, when the latest row has ended, has not completed, and the runtime names no other active turn than the one recorded with it. An end stands, for the row and its attention, until a newer turn opens or a later turn completes.

| Column | Meaning |
|---|---|
| `ended_at_ms` | when `agenthail_end_turn` was called; 0 while the turn runs |
| `turn_id` | the runtime's active turn at the call, when the observer had seen it |
| `completed_turn_id` | the runtime's turn id, bound at completion |
| `needs` | `you` or `nothing` |
| `headline` | one line, at most 80 characters |
| `asks_json` | `[{question, options:[{label, does}], suggested, links:[{title, url}]}]` |
| `next_json` | follow-ups, at most 3 |
| `source` | `tool`, or `nudged` when the Stop hook asked for the end first; a nudged turn that never ends keeps an unended `nudged` row |
| `routed_to` | the session told of a needs-you instead of the person |

Slice 4 (schema 64) adds `answered_at_ms`, set when the person's reply claims the ask, and `answer_status` and `answer_message_id` from its delivery receipt (`unknown` when delivery failed in a way that may still have reached the session).

**`work_records`**, primary key `session_id`

| Column | Meaning |
|---|---|
| `repo_root`, `worktree_path`, `branch`, `base_ref`, `base_commit` | what was made |
| `state` | `active`, `settled` |
| `settled_reason` | `pr_merged`, `task_reported`, `archived`, `person` |
| `cleanup` | `pending`, `removed`, or `kept` with `cleanup_reason` (dirty, unpushed, running) |
| `created_at`, `settled_at` | |

**`session_prs`**, primary key `(session_id, repo, number)`

| Column | Meaning |
|---|---|
| `url`, `source` | `agent`, `created` (from `gh pr create`), `branch` (found by the work record's branch), `person` |
| `watch` | on by default |
| `title`, `state`, `head_sha`, `mergeable`, `review` | last read; `review` is `approved`, `changes_requested` or `none` |
| `failed_checks`, `passed_head`, `conflict_head`, `remarks_through`, `remark_ids` | what has already woken the agent, so a change wakes it once |
| `wakes`, `read_failures`, `last_read_at`, `linked_at` | |

**`work_files`**, primary key `(session_id, path)`, and **`work_commits`**, primary key `(session_id, sha)`

Derived in the daemon from the session's tool events, with no model: Claude `Edit`, `Write`, `MultiEdit` and `NotebookEdit`, Codex file changes, and shell calls running `git commit`.

| Column | Meaning |
|---|---|
| `work_files`: `added`, `removed`, `last_at` | lines changed, summed per path |
| `work_commits`: `subject`, `at`, `pushed` | `pushed` once a remote branch contains it |

A session with none of these rows looks exactly as it does today; adopted terminal sessions gain only what they opt into (an end-of-turn call, an explicit PR link).

## Tools

### `agenthail_end_turn` (MCP, always loaded, terminal)

```
headline  string, at most 80        sidebar and push line
needs     "you" | "nothing"
asks      at most 4: question (at most 160), options 1 to 6 of {label (at most 40), does (at most 90)},
          suggested (index), links at most 3 of {title, url}
next      at most 3 strings, with needs "nothing"
```

Refused, with a message the agent can act on: `needs: "you"` with no ask; asks with `needs: "nothing"`; `suggested` out of range; a second call in one turn. Description, about 600 bytes: write your reply first, then call this as the last thing in every turn; it decides whether the person is pinged.

**A session that reports to an agent** (origin `agent` or `coordinator`: a spawned task, or a worker a lead started with `agenthail thread create` or `agenthail_create`, which records the lead as its creator) ends its turn the same way, but `needs: "you"` goes as an `[agenthail]` message to the session a permission prompt would reach, its project's lead first, then its spawner; the row records it in `routed_to` and `askedSession`. The person gets no ask attention and keeps today's "finished" push for that turn. They see the ask when the lead escalates it. A session the person or voice started, or one with nobody to tell (a lead itself), asks the person. If the message cannot be queued, the ask falls back to the person.

**Skipped call.** `agenthail hook stop --surface claude|codex` (`internal/cli/turn_stop.go`) reads the Stop hook's input and blocks the stop once, with a nudge of about 200 bytes naming `agenthail_end_turn`, when the session holds a live Agenthail credential and its latest `turn_ends` row has no end for this turn (`Registry.TurnEnded`; Codex sends `turn_id`, which must match the row's; on 0.163.0 it is the app-server's `turn.id`, the id the row records). It marks the row `nudged` (`Registry.MarkTurnNudged`, inserting one when the start hook left none), so an end given after the nudge keeps that source. It lets the stop through on `stop_hook_active` (the stop after the nudge), on `agent_id` (a subagent's stop), on any failure, and after 5s.

- Claude host: every session gets an SDK `Stop` callback in `claude-host/host.mjs` that runs the command only when the session has its MCP credential, the input is neither `stop_hook_active` nor a subagent's, and no background task that is not ambient runs; only the host knows which tasks are ambient.
- Codex: `agenthail hooks install codex` adds a `Stop` command hook running `exec '<agenthail>' hook stop --surface codex` beside the session-context hooks, only when the Codex app-server reports 0.163.0 or later in its `initialize` `userAgent`. 0.163.0 is the first version seen to continue the same turn (same `turn_id`) after a Stop hook prints `{"decision":"block","reason":...}`, then send the next Stop with `stop_hook_active: true`. On an older Codex no Stop hook is installed and `hooks status codex` and `doctor` say the end-of-turn nudge needs 0.163.0; after an update the hooks read `outdated` until install runs again.

If the turn still ends without the call, nothing is parsed from the reply: real transcripts showed no untaught agent closing with a recognisable needs list, so today's "finished" push stands.

**Effects.** Session rows, in the session view and the snapshot, carry `endTurn {headline, needs, asks, next, endedAt, askedSession, requestId, answeredAt}` while the end stands. `rowAttention` (`internal/daemon/session_attention.go`) gains kind `ask`, after `request`, for a standing, unanswered `needs: "you"` that asks the person, which puts the row in Needs you. The turn-completed push says "needs you: headline" or "done: headline", and a needs-you push carries the ask's request id and buttons. Hosted Claude sessions name the tool in `CLAUDE_CODE_TERMINAL_MCP_TOOLS`, so a successful call ends the turn with no reply after it; a refused call is an error result and does not. The open ask is listed by `/api/v1/requests` as a request of kind `ask`, so the phone's notification buttons (`surface/request_actions.go`) and the existing question pager answer it through `respond` like any other request.

**One reply.** The ask is answered with the existing `respond` action, never a separate one: the pagers post `{answers:{question: label or typed text}, message: note}`, a notification button posts `{choice: "o<n>" | "reply", message}`. The daemon (`internal/daemon/end_turn_answer.go`) composes one message, a line "1. Deploy the fix? Deploy it" per answered ask in the order asked, then the note, and delivers it as the person through the normal send path. It claims the row (`answered_at_ms`) before delivery, so of two devices or two keys only one sends and the other gets `409 stale`; it releases the claim only when delivery provably did not start or was rejected, and otherwise keeps it with the receipt's status, so a retry never sends twice. A retry with the same `Idempotency-Key` replays the first reply. An answered ask leaves Needs you at once. Composing in the daemon keeps Mac, iOS, web and the notification buttons identical. The client contract is [end-turn-asks.md](end-turn-asks.md).

### `isolation: "worktree"` on `agenthail_create` and `agenthail thread create --worktree`

Optional `branch` (default `ah/<alias or 8 hex>`) and `base` (default the spawner's `HEAD` commit, so a child sees committed parent work; the task result says when that `HEAD` was behind its upstream). The daemon finds the main checkout through `git rev-parse --git-common-dir`, adds `<main>/.worktrees/<branch slug>` (the convention host moves already use, `session_move_git.go:191`), starts the session in the same relative folder inside it, and writes `work_records`. Refused when the folder or branch exists, or the cwd is not in a repository.

**Settle and clean up.** A session settles when its task reports, its last linked PR merges, or it is archived. Cleanup then runs `git worktree remove` without force and deletes the branch only when it is merged into the base or contained in a remote branch. It keeps the worktree, recording why, when there are tracked or untracked changes, commits not on any remote, or a live turn. The kept reason shows in the work panel; `agenthail work clean <target>` retries. Claude's own `worktree` passthrough (`host.mjs:447`, `dashboard_action_request.go`) is removed, since this replaces it.

### `agenthail_pr` (MCP) and `agenthail pr` (CLI)

```
action   "link" | "unlink" | "watch" | "unwatch"
pr       URL, or number with repo
session  optional; default yourself
```

**Automatic link.** When the daemon ingests a finished shell call whose command starts with `gh pr create` and whose output has a line that is only a PR URL, it links that PR with `source: created`. It ports T3's guards (`PullRequestSyncReactor.ts:58-174`): unwrap `sh -lc`, strip heredocs, quotes and comments, and skip when the script also lists PRs or prints more URLs than it created. A work record's branch also links any PR opened on it, by anyone (`source: branch`).

**Watch.** Every 2 minutes, one `gh api graphql` query per repository reads state, mergeability, head, check rollup and reviews for its watched PRs, through the person's own `gh` sign-in; without one, the panel says so and nothing wakes. A wake is queued into the session as an `[agenthail]` turn when, per head: a check newly fails; all required checks pass; the branch conflicts; or someone else reviews or comments. A new head re-arms all of these. A task that has already reported is not woken; its spawner is. Ten comment-only wakes in a row end the watch. Closed unmerged wakes once and stops. **Merged** wakes no one, settles the session, and starts worktree cleanup.

## Clients

This work owns the data, the API fields and the actions (`session-pin`, `session-unpin`, `respond` for an ask, `pr`), and the end-of-turn card's content. The sidebar rows, the details and work panel layout and the window frame belong to the client redesign, which binds to these fields as they land and reviews the card's look.

- **Composer card** (Mac, iOS, web): the headline, then the asks through the existing `QuestionPager` and `question-pager.js` with the suggested option marked, an optional note, then one Send that posts `respond`. In the app an ask is never sent on one tap, so the note can go with it; a notification button answers a single ask with 4 or fewer options in one tap. It uses the redesign's tokens and row grammar.
- **Row fields**: `endTurn.headline` for the row subtitle, `ask` attention for the Needs you band, a derived done state for a `needs: "nothing"` idle session, and `pinnedAt`. Placement and controls are the redesign's.
- **Work fields**: branch, worktree path, kept-cleanup reason, each PR with its state, review and check summary, `work.files` and `work.commits`. The panel is the redesign's.
- **Push**: needs you and done text from the headline; ask buttons through the request path above.

## Slices

Each slice is one PR, builds on a working product, and lands with the budget test updated.

| # | Slice | Acceptance tests |
|---|---|---|
| 0 | Tool spec table and budget test | `tools/list` is generated from the spec; the test pins card, always-loaded and list bytes and fails on growth |
| 1 | Pinned sessions stay up: `pinned_at`, `session-pin`/`session-unpin` (CLI `agenthail pin`/`unpin`), keeper | a pinned session that exits is resumed once, and after a daemon restart; the resume sends no input (no host turn, no `turn/start`); archived or person-stopped sessions are never resumed; a mirrored row from a paired host is skipped; a failing resume follows 30s, 1m, 2m, 4m, 8m, then `keepAliveFailed` attention exactly once; a project's first lead starts pinned, a replacement lead inherits the old one's pin, and unpin always works |
| 2 | `agenthail_end_turn`, `turn_ends`, `ask` attention, push text | MCP call stores the row; each refusal case; a needs-you turn puts the row in the Needs you band and the push says "needs you: headline"; a worker's needs-you reaches its spawner or lead, not the person |
| 3 | Nudge and fallback | claude-host test with the fake Claude: a turn without the call is blocked once, not twice, not while background tasks run, never without the credential; the stop command lets an ended turn, a subagent and the second stop through; Codex install against a real `codex app-server` in its own `CODEX_HOME` adds the Stop hook on 0.163.0 and none on a Codex reporting an older version |
| 4 | One reply and the card | `respond` to an ask delivers exactly one numbered message, replays on a same-key retry, refuses a second answer and two concurrent answers with one `409`, keeps a possibly delivered answer claimed, and clears the attention, including for a session with no live provider requests; notification buttons answer an ask; the card in web `question-pager.js` (Mac and iOS follow against [end-turn-asks.md](end-turn-asks.md)) |
| 5 | Worktree isolation, `work_records`, cleanup | temporary repository: create makes branch and worktree and starts there; settle removes a clean merged worktree; dirty or unpushed is kept with its reason; Claude passthrough removed |
| 6 | PR link, `session_prs` | explicit link and unlink through CLI and MCP; `gh pr create` cases link, guarded cases do not; branch link; without `gh` or a sign-in, link refuses with the reason |
| 7 | PR watch and settle | fake GitHub reads: a failing check wakes once per head; a new head re-arms; conflict wakes once; merged settles the session and removes its worktree without a wake |
| 8 | Files and commits | recorded transcripts of Claude and Codex edits and commits produce the expected `work_files` and `work_commits` rows; a commit is marked pushed once a remote branch has it |
| 9 | Work data on the API | the session view carries branch, worktree, PRs with checks, files, commits and kept reason; the panel itself is the client redesign's |

## Decisions

1. Hosted sessions are taught MCP tools, with a small always-loaded core; terminal sessions keep the CLI card. One spec table generates both cards and the MCP tool list.
2. The worktree base defaults to the spawner's `HEAD`, with an explicit `base` (for example `origin/main`). The task result says when `HEAD` was behind its upstream.
3. GitHub goes through whatever `gh` is signed into, including `GH_TOKEN` setups. Agenthail stores no token. Without `gh` or a sign-in, PR features are off and the work panel says why.
4. A worker's needs-you goes to its spawner or lead, the same routing as permission prompts. The person sees it when the lead escalates it, or when the session has no lead.
