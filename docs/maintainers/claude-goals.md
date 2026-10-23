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

## Setting and clearing a goal

`agenthail goal <target> set|edit <condition>`, `agenthail goal <target>
clear`, the dashboard goal form and `/goal` in the composer, and the iPhone and
Mac goal controls all run Claude's own `/goal` command as the session's turn.
Edit replaces the goal, because a new `/goal` supersedes the one in force.
Setting a goal starts a turn toward it, as `/goal` does in Claude.

The command is held in the session's queue, like compact, and runs once the
session is idle; it never steers into a running turn. Agenthail then waits for
the `local_command` record Claude writes for that `/goal`: `Goal set: …`,
`Goal cleared: …` and `No goal set` are success, and any other output is the
refusal, reported as the queue item's error. Claude refuses a goal when hooks
are disabled by policy or held, or when the workspace is not trusted. Before
queueing, Agenthail refuses what Claude 2.1.304 would: a condition longer than
4000 characters, and a condition that is one of the clear words (`clear`,
`stop`, `off`, `reset`, `none`, `cancel`), which Claude would read as a clear.

`/goal` runs as a command only where the session takes a person's turn: the
Agenthail host, Remote Control, or a session with no live process, which
resumes in the host. A session reached only through its peer socket cannot set
a goal: the socket hands `/goal …` to the model as a cross-session message, so
Claude writes no `goal_status` or `local_command` record for it. Agenthail
refuses such a request with that reason.

In the session timeline, the command shows as a `Command` event and its output
as a `Local command` event. The Stop hook instruction Claude adds for the model
shows as one `Claude Code instructions` line; it starts the model's turn
toward the goal. None of them are shown as something the user said. A slash
command typed in Claude itself, which Claude records with a human origin,
stays the person's message and shows as typed, such as `/goal tests pass`.

Claude records a built-in such as `/goal` or `/context` with no origin and
runs it without a model turn.
Between turns, such a command and its output make an exchange of their own,
so the latest exchange (what `agenthail last` shows) is the command and what
it printed. A command that prints nothing, or one inside a running turn,
makes no exchange. Such an exchange is marked `command`, and the latest reply
(what `agenthail reply` returns) stays the model's last reply.

Claude has no settable pause or token budget for a goal. The command takes only
`[<condition> | clear]`; Claude pauses a goal itself, and any message
continues it.

## Capabilities and clients

`goalStatus` means the session metadata endpoint and `agenthail goal <target>`
return the goal. `goalSet` means the goal is set, replaced and cleared here.
`goal` means it is also paused, resumed and budgeted here
([Codex goals](codex-goals.md)), and gates goal-completed relays. Codex
sessions report all three. Claude sessions report `goalStatus`, and `goalSet`
wherever `/goal` runs as a command; never `goal`. Clients show set, edit and
clear for `goalSet`, and pause, resume and budget only for `goal`. The Claude
transcript stream sends a `goal` event each time the goal it reads changes,
including when the transcript is replaced and a new file describes a different
goal, so an open session updates without a reload. Each event's version is
higher than the last one sent for the session while the daemon runs.

Goal-completed relays still require `goal`, so they do not route Claude
sessions.
