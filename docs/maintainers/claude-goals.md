# Claude goals

Claude Code's `/goal <condition>` installs a session-scoped Stop hook. After
every turn a separate small model judges the condition from the conversation
and returns *not yet met*, *met*, or *impossible*; on *not yet met* Claude keeps
working. Claude records each step in the session transcript as an attachment of
type `goal_status`, and Agenthail reads the goal from those records. The
transcript is the one place every Claude runtime writes, so the same goal shows
for host, terminal, cmux and Remote Control sessions.

| Transcript record | Agenthail goal |
| --- | --- |
| `{met:false, sentinel:true}`: goal set | `active` |
| `{met:false, reason}`: judged not yet met | `active`, with the reason |
| `{met:true, reason, durationMs, tokens}`: judged met | `complete` |
| `{met:false, failed:true, reason, …}`: judged impossible | `blocked` |
| `{met:true, sentinel:true}`: `/goal clear`, or cleared after an unrecoverable error | no goal |

`objective` is the condition and `reason` is the evaluator's latest reason.
`createdAt` is when the goal was set; Claude writes the set record again for a
goal still in force, which keeps that time. A complete or blocked goal stays
reported until Claude records another goal or a clear. Elapsed time and tokens
come from the final verdict; while a goal is active, elapsed time runs to the
latest verdict.

Claude does not record a paused goal. When it pauses one (a rate or usage
limit, a hook ending the turn, or the stop-hook block cap after repeated unmet
verdicts) it writes only a `system` notice, `Goal paused · <cause> · send a
message to continue`, in interactive sessions, and a generic hook block-cap
warning in `-p` and SDK sessions. The goal stays set, so Agenthail reports it as
active.

## Capabilities and clients

Claude sessions report `goalStatus` but not `goal`. `goalStatus` means the
session metadata endpoint and `agenthail goal <target>` return the goal;
`goal` means the session also takes the typed set, edit, pause, resume and
budget controls ([Codex goals](codex-goals.md)), which Claude has no equivalent
for. Clients show a Claude goal with its status and reason and offer no goal
controls. The Claude transcript stream sends a `goal` event each time the goal
it reads changes, including when the transcript is replaced and a new file
describes a different goal, so an open session updates without a reload. Each
event's version is higher than the last one sent for the session while the
daemon runs.

Goal-completed relays still require `goal`, so they do not route Claude
sessions.
