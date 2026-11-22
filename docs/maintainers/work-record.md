# Work record: end of turn, worktrees, pull requests

Agenthail keeps a record of the work in each session it launches, beside the session catalog, and gives agents tools built on it. Three capabilities, in order: a structured end of turn, a worktree per task, and pull requests linked to sessions and watched.

## What agents actually use

Measured over every Claude transcript in one config root: about 1,400 `agenthail` CLI calls (1,062 of them `send`, 115 `help`) against 9 MCP calls. Two causes, both observed:

- The start card teaches seven CLI lines and no MCP tool (`internal/agentcard/render.go:21-37`).
- Claude defers every `mcp__agenthail__*` tool behind ToolSearch, so an MCP call costs a load step; Bash is always loaded.

The design follows from that:

1. **Mechanics before instructions.** Most of the three capabilities need no agent call at all: the turn judge reads how a turn ended, `gh pr create` links its PR, a merged PR settles the session. What an agent must know fits in two card lines.
2. **One service, three faces.** The work record lives in a new `internal/workrecord` package over the registry. Writes go through `performAction`, the path the CLI (`internal/cli/daemon_api.go:110`) and MCP writes (`internal/daemon/mcp_write.go:29-46`) already share. One spec table per tool (name, one-line use, input struct) registers the MCP tool, builds the CLI help, and supplies the card line, so the three cannot drift.
3. **Hosted sessions are taught the tools.** A session holding a live Agenthail MCP credential (every Claude host session and every Codex thread Agenthail starts) gets a card naming MCP tools; a terminal Claude or Codex session whose own config starts the stdio relay is taught the tools too, without the line to call `agenthail_end_turn` before each turn's reply, and one without the server keeps the CLI card. A small core is always loaded through `_meta["anthropic/alwaysLoad"]: true` (supported by Claude Code 2.1.316): `agenthail_send`, `agenthail_create`, `agenthail_wait`, `agenthail_result`, and later `agenthail_end_turn` and `agenthail_pr`. Everything else stays deferred. `agenthail_end_turn` is MCP only, because the clients need its structure and validation.
4. **A budget test.** A Go test pins the bytes of the rendered card, the always-loaded tool JSON and the whole tool list. Growth fails the test until the pinned numbers are raised in the same change.

### Text injected

| Piece | Today | After |
|---|---|---|
| Start card (once per session) | 1,666 bytes (test fixture) | slice 0: 1,702 for the tool card, CLI card unchanged; later slices +300 at most |
| Always-loaded tool schemas, Claude | 0 | slice 0: 3,993 bytes for send, create, wait, result; then `agenthail_end_turn` and `agenthail_pr` |
| MCP tool list JSON | 18,849 bytes, 26 tools | slice 0: 19,001 (the `_meta` marks); later +2 tools, at most +4 KB |
| PR wake, only on a change that needs the agent | 0 | at most 600 bytes each |

Whether Codex defers MCP tools is not yet verified; if it does not, the two new tools cost Codex their full schema each turn, which is why the PR tool is one action tool rather than five.

## Data model

Tables beside the catalog, keyed by session id, one migration per slice. Each takes the next free schema number when it lands, after rechecking main and every open PR.

**`sessions.pinned_at`** (pinned sessions)

| Column | Meaning |
|---|---|
| `sessions.pinned_at` | unix ms, null when unpinned; set by `session-pin`, cleared by `session-unpin` |

**`turn_ends`**, primary key `(session_id, started_at)`

A row opens when the turn's `UserPromptSubmit` hook runs (Claude host, and Codex with its hooks installed), which also drops any older row that never ended, so a session that never calls the tool holds one row at most. `agenthail_end_turn` fills the open row, or adds one when there is none. The observer binds `completed_turn_id` to the newest row opened by the time the turn completed, so a completion seen only after the next turn opened stays with its own turn. Codex states that time in whole seconds, so a Codex completion with no row opened by then goes to the oldest unbound row opened within that second. A session whose newest row has no completion is watched for 24 hours (`TurnEndWatch`), even once its process has exited, so a turn the session started itself is seen ending. Seeing such a session busy does not arm a notification: only a turn the daemon was already watching for sends, deliveries or tasks pushes. A call is a repeat, and refused, when the latest row has ended, has not completed, and the runtime names no other active turn than the one recorded with it. An end stands, for the row and its attention, until a newer turn opens or a later turn completes.

| Column | Meaning |
|---|---|
| `ended_at_ms` | when `agenthail_end_turn` was called; 0 while the turn runs |
| `turn_id` | the runtime's active turn at the call, when the observer had seen it |
| `completed_turn_id` | the runtime's turn id, bound at completion |
| `needs` | `you` or `nothing` |
| `headline` | one line, kept up to 1000 characters and cut to 80 where shown |
| `asks_json` | `[{question, options:[{label, does}], suggested, links:[{title, url}]}]` |
| `next_json` | follow-ups, at most 3 |
| `source` | `tool` |
| `routed_to` | the session told of a needs-you instead of the person |

Slice 4 (schema 64) adds `answered_at_ms`, set when the person's reply claims the ask, and `answer_status` and `answer_message_id` from its delivery receipt (`unknown` when delivery failed in a way that may still have reached the session).

**`work_records`**, primary key `worktree_path`, one per session (`session_id`, unique once set)

The row is written, in state `creating` and with no session, before `git worktree add`, so a crash between the two never leaves a worktree nobody tracks.

| Column | Meaning |
|---|---|
| `repo_root`, `worktree_path`, `branch`, `base_ref`, `base_commit` | what was made |
| `state` | `creating`, `active` (its session started), `settled` |
| `settled_reason` | `task_reported`, `archived`, `person`, `merged` (a linked PR from its branch merged), `orphaned` (a create that never recorded its session) |
| `cleanup` | `pending`, `removed`, or `kept` with `cleanup_reason` (`dirty`, `ignored`, `unpushed`, `running`, or `failed: ...`) |
| `kept_paths` | up to five ignored files that kept it, as JSON |
| `created_at`, `settled_at` | |

**`session_prs`**, primary key `(session_id, repo, number)`

| Column | Meaning |
|---|---|
| `repo` | `host/owner/name` |
| `url`, `source` | `agent`, `created` (from `gh pr create`), `branch` (found by the work record's branch), `person` |
| `unlinked` | hidden by an unlink; found links leave it hidden, an explicit link restores it |
| `watch` | on by default |
| `title`, `state`, `head_sha`, `mergeable`, `review`, `checks` | last read; `review` is `approved`, `changes_requested` or `none` |
| `failed_checks`, `passed_head`, `conflict_head`, `remarks_through_ms`, `remark_ids` | what has already woken the agent, so a change wakes it once |
| `wakes`, `comment_wakes`, `read_failures`, `last_read_at_ms`, `linked_at_ms` | `wakes` counts wakes, re-watches and re-links, and a read applies only if it is unchanged since the read began; `comment_wakes` counts comment-only wakes in a row |

**`work_files`**, primary key `(session_id, path)`, and **`work_commits`**, primary key `(session_id, sha)`

Derived in the daemon from the session's tool events, with no model: Claude `Edit`, `Write`, `MultiEdit` and `NotebookEdit`, Codex file changes, and shell calls running `git commit`.

| Column | Meaning |
|---|---|
| `work_files`: `added`, `removed`, `last_at` | lines changed, summed per path |
| `work_commits`: `subject`, `at`, `pushed` | `pushed` once a remote branch contains it |

A session with none of these rows looks exactly as it does today; adopted terminal sessions gain only what they opt into (an end-of-turn call, an explicit PR link).

## Tools

### `agenthail_end_turn` (MCP, always loaded)

```
headline  string, cut to 80 where shown   sidebar and push line
next      up to 3 strings, each cut to 160 where shown
```

**Order.** The agent calls the tool when the turn's work is done and then writes its reply as its final message, with no tool call after it; a successful result says so. Claude Opus 5.5 returns text written between tool calls as a progress-update `thinking` block holding only a summary of that text, so a reply written before the call would reach the transcript as a Thought rather than as the reply. Only the text after a turn's last tool call is kept verbatim.

Only a blank headline is refused. Line breaks fold into spaces, next items past 3 are dropped, and a second call in the same turn replaces the first headline. When the headline runs past 80 characters, or names a PR or issue number, a work item id, a review round or a commit hash, the result's `then` gives the text as the person will see it, names each thing that matched, and asks the agent to call again with the outcome in plain words. The call is never refused for these. A turn that needs a decision raises an ask first ([asks.md](asks.md)); the ask, not the end of turn, puts the session in Needs you.

**Skipped call and spoken questions.** Nothing holds or prompts a turn that ends without `agenthail_end_turn`: the turn judge reads its state from the reply, and the row shows no headline. A reply that asks the person something no ask carries is judged waiting on the person and pushes Needs you ([turn judge](turn-judge.md#the-push)). Agenthail installs no `Stop` hook. A `Stop` hook an earlier install wrote goes away without the person doing anything: the daemon, when it starts (as after an update), removes from every Claude config root's `settings.json` and from Codex any agenthail hook this binary no longer writes, and installs nothing; `agenthail hooks install` and `uninstall`, and their `codex` forms, remove them too. Until then `agenthail hook stop` reads its input and exits 0 with no output, so an old hook neither errors nor holds a turn. The daemon starts Codex for this only when its `hooks.json` or `config.toml` names such a hook, and leaves every other hook where it is, so Codex keeps trusting them.

**Effects.** Session rows, in the session view and the snapshot, carry `endTurn {headline, next, endedAt}` while the end stands, with the headline cut to 80 characters and each next item to 160. The turn-completed notification says "done: headline" with the same cut headline, or nothing when the session has an ask open to the person, which was announced on its own. A phone push names the session by its alias (else the app) and its project, never by the session's title, since pushes pass through the relay and Apple: "@builder in widget done: Deployed".

### `isolation: "worktree"` on `agenthail_create` and `agenthail thread create --worktree`

Optional `branch` (default `ah/<alias or 8 hex>`) and `base` (default the spawner's `HEAD` commit, so a child sees committed parent work; the create result's `worktree.behind` says when that `HEAD` was behind its upstream). The `session-create` action takes the same as `isolation`, `worktree` (the branch name; a non-empty `worktree` alone also asks for one, which is what the apps' worktree name sends) and `base`. `internal/worktree` finds the main checkout (from `git rev-parse --git-common-dir`, or a submodule's `core.worktree`), records the work, adds `<main>/.worktrees/<branch slug>` (the convention host moves already use) and starts the session in the same relative folder inside it, or at the worktree's top when that folder is not in the base commit. When `.worktrees/` is not already ignored, it is added to the repository's `.git/info/exclude`; the repository's `.gitignore` is never written. Refused when the folder or branch exists, the base is not a commit, or the cwd is not in a repository; a session started in a terminal cannot have one. A child's ceiling roots inside the checkout move to the same places in the worktree, so the child works only there. A start that returns no session removes the worktree and its record. The local CLI create runs the same package against the registry directly.

**Settle and clean up.** A session settles when it reports with `agenthail_report`, when it is archived, when the person runs `work-clean` (`agenthail thread clean <target>`), or when a watched PR whose head branch is the worktree's branch merges. Cleanup runs on the host that owns the folder: a `work-clean` for another host's session is forwarded there like any session action. It removes the worktree with `git worktree remove` (no force) and deletes the branch when nothing would be lost: no tracked or untracked changes, no ignored files outside regenerable folders (ignored files under `node_modules`, `.build`, `build`, `dist`, `target`, `bin`, `obj`, `DerivedData`, `__pycache__`, `.venv`, `.gradle`, `.next` or `coverage` never keep it; any other, such as a `.env`, does, and the row lists up to five of them in `kept_paths`), and every commit the branch or the worktree's own `HEAD` reaches merged into the base or contained in a remote branch, so work committed on a detached `HEAD` or after deleting the branch is kept too. Otherwise it keeps both and records why (`dirty`, `ignored`, `unpushed`). A session mid-turn is kept as `running` and retried once a minute until its turn ends. `work-clean` on kept work retries it, and with `forceIgnored` (`--force-ignored`) removes it past ignored files; an agent's session cannot run it.

**Reconcile.** The same once-a-minute sweep finds `creating` rows more than an hour old: a create that crashed between the row and its session (the CLI creates outside the daemon with any `--timeout`, so age is the only safe sign). A registered session whose folder is inside the worktree takes it; otherwise the row settles as `orphaned` and is cleaned like any other, its row dropped once removed. Claude's own `--worktree` passthrough is removed, since this replaces it.

Each session row in the API carries `worktree {path, branch, base, state, settledReason, cleanup, cleanupReason, keptPaths}`. The session view reads it from `work_records` for this host's sessions, and a trigger on those columns moves the presentation version, so any write, the CLI's included, changes the view's cache key. The daemon makes every write through one helper that also republishes the session's catalog row, which is what event streams and paired hosts see. A write made outside the daemon, such as the CLI's local create, reaches them on the daemon's next discovery pass (every 30 seconds), which republishes any row whose worktree changed.

### `agenthail_pr` (MCP) and `agenthail pr` (CLI)

```
action   "link" | "unlink" | "watch" | "unwatch"
pr       URL, or number
repo     optional owner/name for a number; default the session's linked PR with that number, else its folder's origin
session  optional; default yourself (MCP: a session you spawned or lead)
```

The `pr` action (`{action: "pr", sessionId, op, pr, repo}`) backs both, and replies with the session's linked PRs. `link` reads the PR through `gh` (`internal/github`) and refuses with the reason when `gh` is missing, signed out or its token is refused; the other ops need no GitHub read. A link through MCP or the CLI is `source: agent`, one from the apps `person`. `unlink` keeps the row as unlinked so nothing found later brings it back; only an explicit `link` does. Each session row in the API carries `prs [{repo, number, url, source, watch, title, state, headSha, mergeable, review, checks, problem}]`; `problem` says why a watched PR is not being read (no `gh` sign-in, a rate limit, or GitHub not returning it three reads running). As with worktrees, the session view reads them from `session_prs` for this host's sessions under a trigger that moves the presentation version, and every daemon write goes through one helper that also republishes the session's catalog row. The trigger fires only when a value it watches changes, so a watch read that finds nothing new leaves cached views alone, and the watcher republishes a row only when what it shows changed.

**Automatic link.** When a session Agenthail started (not a terminal session or a delegated subagent) completes a turn, the daemon reads that turn's timeline and pairs each shell call (`Bash`, `exec_command`) with its result. A successful call whose command runs `gh pr create` and whose output has a line that is only a PR URL links that PR with `source: created`. It ports T3's guards (`PullRequestSyncReactor.ts:58-174`): unwrap `sh -lc`, strip heredocs, quotes and comments, require the create to start a shell command, and skip when the script also lists PRs, names the number elsewhere, or prints more URLs than it created. Every 2 minutes the daemon also looks up, in one GraphQL request per GitHub host, the worktree branches whose session has no PR in that repository (the branch's remote, else `origin`), and links same-repository PRs opened on them since the worktree was made (`source: branch`). Links are idempotent, so a turn read twice links once.

**Watch.** Every 2 minutes, one `gh api graphql` query per GitHub host reads state, mergeability, head, check rollup (with each check's required flag), reviews and the last 20 comments for every watched PR on it, through the person's own `gh` sign-in; without one, the PR's `problem` says so and nothing wakes. A rate-limited read backs that host off from 4 minutes, doubling to 30; a read that leaves under 100 points waits for the limit's reset. A wake is queued into the session as an `[agenthail]` turn when, per head: a check newly fails; all required checks pass (all checks, when none is required); the branch conflicts; or someone other than the `gh` user reviews or comments. The first read takes existing reviews and comments as seen. A new head re-arms all of these. A task that has already reported is not woken; its spawner is, with the child named. Ten comment-only wakes in a row end the watch; watching it again starts the count over. Closed unmerged wakes once and stops. **Merged** wakes no one and stops the watch; when the PR's head branch is the session's active worktree branch, it settles the work as `merged` and starts worktree cleanup. Unwatching, re-watching or unlinking during a read drops that read, so it neither wakes nor overrides the person's choice.

## Clients

This work owns the data, the API fields and the actions (`session-pin`, `session-unpin`, `respond` for an ask, `pr`), and the end-of-turn card's content. The sidebar rows, the details and work panel layout and the window frame belong to the client redesign, which binds to these fields as they land and reviews the card's look.

- **Composer card** (Mac, iOS, web): the headline, then the asks through the existing `QuestionPager` and `question-pager.js` with the suggested option marked, an optional note, then one Send that posts `respond`. In the app an ask is never sent on one tap, so the note can go with it; a notification button answers a single ask with 4 or fewer options in one tap. It uses the redesign's tokens and row grammar.
- **Row fields**: `endTurn.headline` for the row subtitle, `turnState` for the row's label ([turn-judge.md](turn-judge.md#the-row)), `ask` attention for the Needs you band, and `pinnedAt`. Placement and controls are the redesign's.
- **Work fields**: branch, worktree path, kept-cleanup reason, each PR with its state, review and check summary, `work.files` and `work.commits`. The panel is the redesign's.
- **Push**: needs you and done text from the headline; ask buttons through the request path above.

## Slices

Each slice is one PR, builds on a working product, and lands with the budget test updated.

| # | Slice | Acceptance tests |
|---|---|---|
| 0 | Tool spec table and budget test | `tools/list` is generated from the spec; the test pins card, always-loaded and list bytes and fails on growth |
| 1 | Pinned sessions stay listed: `pinned_at`, `session-pin`/`session-unpin` (CLI `agenthail pin`/`unpin`) | a pinned session stays current and is never retired, process up or not; nothing starts it on its own, and a message resumes it; a project's first lead starts pinned, a replacement lead inherits the old one's pin, and unpin always works |
| 2 | `agenthail_end_turn`, `turn_ends`, `ask` attention, push text | MCP call stores the row; each refusal case; a needs-you turn puts the row in the Needs you band and the push says "needs you: headline"; a worker's needs-you reaches its spawner or lead, not the person |
| 3 | No Stop hook | a turn that ends without a headline is never held; Claude settings and a real `codex app-server` in its own `CODEX_HOME` lose an agenthail `Stop` hook an earlier install wrote, on install and uninstall, and keep the person's own |
| 4 | One reply and the card | `respond` to an ask delivers exactly one numbered message, replays on a same-key retry, refuses a second answer and two concurrent answers with one `409`, keeps a possibly delivered answer claimed, and clears the attention, including for a session with no live provider requests; notification buttons answer an ask; the card in web `question-pager.js` (Mac and iOS follow against [asks.md](asks.md)) |
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
