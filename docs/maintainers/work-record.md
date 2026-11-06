# Work record: end of turn, worktrees, pull requests

Agenthail keeps a record of the work in each session it launches, beside the session catalog, and gives agents tools built on it. Three capabilities, in order: a structured end of turn, a worktree per task, and pull requests linked to sessions and watched.

## What agents actually use

Measured on forge-agents over every Claude transcript in the config root: about 1,400 `agenthail` CLI calls (1,062 of them `send`, 115 `help`) against 9 MCP calls. Two causes, both observed:

- The start card teaches seven CLI lines and no MCP tool (`internal/agentcard/render.go:21-37`).
- Claude defers every `mcp__agenthail__*` tool behind ToolSearch, so an MCP call costs a load step; Bash is always loaded.

The design follows from that:

1. **Mechanics before instructions.** Most of the three capabilities need no agent call at all: the Stop hook asks for the end of turn, `gh pr create` links its PR, a merged PR settles the session. What an agent must know fits in two card lines.
2. **One service, three faces.** The work record lives in a new `internal/workrecord` package over the registry. Writes go through `performAction`, the path the CLI (`internal/cli/daemon_api.go:110`) and MCP writes (`internal/daemon/mcp_write.go:29-46`) already share. One spec table per tool (name, one-line use, input struct) registers the MCP tool, builds the CLI help, and supplies the card line, so the three cannot drift.
3. **Teach the CLI for everything but the end of turn.** The CLI is what agents reach for, works in terminal and hosted sessions alike, and costs no schema bytes per turn. `agenthail_end_turn` is MCP only, because the Stop hook and the clients need its structure and validation; it is marked `_meta["anthropic/alwaysLoad"]: true` (supported by Claude Code 2.1.316) so Claude never has to load it, and it is named in `CLAUDE_CODE_TERMINAL_MCP_TOOLS` so its result ends the turn without a filler reply.
4. **A budget test.** A Go test pins the bytes of the rendered card, the always-loaded tool JSON and the whole tool list. Growth fails the test until the pinned numbers are raised in the same change.

### Text injected

| Piece | Today | After |
|---|---|---|
| Start card (once per session) | about 1.9 KB | +300 bytes at most (end-of-turn line, worktree and PR line) |
| Always-loaded tool schema, Claude | 0 | `agenthail_end_turn`, at most 2 KB |
| MCP tool list JSON (deferred on Claude) | 18,849 bytes, 26 tools | +2 tools (`agenthail_end_turn`, `agenthail_pr`), at most +4 KB |
| Nudge, only when a turn skips the call | 0 | at most 300 bytes, once per turn |
| PR wake, only on a change that needs the agent | 0 | at most 600 bytes each |

Whether Codex defers MCP tools is not yet verified; if it does not, the two new tools cost Codex their full schema each turn, which is why the PR tool is one action tool rather than five.

## Data model

Three tables beside the catalog, keyed by session id, one per slice, starting at schema 56 (no open PR claims it).

**`turn_ends`**, primary key `(session_id, turn_id)`

| Column | Meaning |
|---|---|
| `needs` | `you` or `nothing` |
| `headline` | one line, at most 80 characters |
| `asks_json` | `[{question, options:[{label, does}], suggested, links:[{title, url}]}]` |
| `next_json` | follow-ups, at most 3 |
| `source` | `tool`, `nudged` (tool after the Stop hook), `text` (parsed from the reply), `none` |
| `answered_at`, `answer_message_id` | set when the person's one reply is queued |
| `created_at` | |

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
| `state`, `head_sha`, `mergeable` | last read |
| `failed_checks`, `passed_head`, `conflict_head`, `remarks_through`, `remark_ids` | what has already woken the agent, so a change wakes it once |
| `wakes`, `read_failures`, `last_read_at`, `linked_at` | |

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

**A session that reports to an agent** (spawned task, worker under @lead) ends its turn the same way, but `needs: "you"` goes to its report-to session as a message, not to the person, matching the card's existing rule that questions go to the spawner (`render.go` `askLine`).

**Skipped call.** Claude host: a Stop hook in `claude-host/host.mjs` blocks once (`stop_hook_active` guards the second stop; not while background tasks run) with the nudge. Codex: the per-thread hook config gains a Stop hook, `agenthail hook stop --surface codex`, that asks the daemon whether the turn has a `turn_ends` row; the Codex 0.163 binary carries `stop_hook_active`, and a spike with its own `CODEX_HOME` and app-server confirms the block semantics before that code lands. If the turn still ends without the call, the observer parses a closing "What I need from you" list or "Nothing needed from you" line from the reply (`source: text`); otherwise `source: none` and today's "finished" push stands.

**Effects.** `rowAttention` (`internal/daemon/session_attention.go:5-25`) gains kind `ask`, after `request`, for an unanswered `needs: "you"` with no newer person message. The turn-completed push (`observer.go:223-230`) says "needs you: headline" or "done: headline". The open ask is listed by `/api/v1/requests` as a request of kind `ask`, so the phone's notification buttons (`surface/request_actions.go:35-61`) and the existing question pager work for it unchanged.

**One reply.** New action `turn-answer {sessionId, turnId, picks:[{ask, option | text}], note}`. The daemon composes one numbered message, "1. Deploy the fix? Deploy it" per ask plus the note, queues it through the normal send path, and marks the row answered. Composing in the daemon keeps Mac, iOS, web and the notification buttons identical.

### `isolation: "worktree"` on `agenthail_create` and `agenthail thread create --worktree`

Optional `branch` (default `ah/<alias or 8 hex>`) and `base` (default the spawner's `HEAD` commit, so a child sees committed parent work). The daemon finds the main checkout through `git rev-parse --git-common-dir`, adds `<main>/.worktrees/<branch slug>` (the convention host moves already use, `session_move_git.go:191`), starts the session in the same relative folder inside it, and writes `work_records`. Refused when the folder or branch exists, or the cwd is not in a repository.

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

- **Composer card** (Mac, iOS, web): the headline, then the asks through the existing `QuestionPager` and `question-pager.js` with the suggested option marked, then one Send that posts `turn-answer`. A single single-select ask on the phone sends on one tap.
- **Rows**: the headline as the row subtitle; `ask` joins the Needs you band; a `needs: "nothing"` idle session shows Done with Archive.
- **Work panel** (session details): branch, worktree path, kept-cleanup reason, each PR with its state and check summary.
- **Push**: needs you and done text from the headline; ask buttons through the request path above.

## Slices

Each slice is one PR, builds on a working product, and lands with the budget test updated.

| # | Slice | Acceptance tests |
|---|---|---|
| 0 | Tool spec table and budget test | `tools/list` is generated from the spec; the test pins card, always-loaded and list bytes and fails on growth |
| 1 | `agenthail_end_turn`, `turn_ends` (schema 56), `ask` attention, push text | MCP call stores the row; each refusal case; a needs-you turn puts the row in the Needs you band and the push says "needs you: headline"; a worker's needs-you reaches its spawner, not the person |
| 2 | Nudge and fallback | claude-host test with a fake query: a turn without the call is blocked once, not twice, not while background tasks run; Codex Stop hook tested against a scratch app-server; parser table tests built from real reply endings |
| 3 | One reply and the card | `turn-answer` queues exactly one numbered message, idempotent on retry, and clears the attention; notification buttons answer an ask; card in web, Mac and iOS |
| 4 | Worktree isolation, `work_records` (schema 57), cleanup | temporary repository: create makes branch and worktree and starts there; settle removes a clean merged worktree; dirty or unpushed is kept with its reason; Claude passthrough removed |
| 5 | PR link, `session_prs` (schema 58) | explicit link and unlink through CLI and MCP; `gh pr create` cases link, guarded cases do not; branch link |
| 6 | PR watch and settle | fake GitHub reads: a failing check wakes once per head; a new head re-arms; conflict wakes once; merged settles the session and removes its worktree without a wake |
| 7 | Work panel | panel shows branch, worktree, PRs and kept reason in web, Mac and iOS |

## Decisions wanted

1. Keep the CLI as the taught surface for existing tools, with only `agenthail_end_turn` always loaded (recommended), or move the card to MCP names for hosted sessions.
2. Worktree base: the spawner's `HEAD` (recommended) or the fetched default branch.
3. GitHub through the person's `gh` sign-in (recommended) or a token in Agenthail's config.
4. A worker's needs-you goes to its spawner, not the person (recommended).
