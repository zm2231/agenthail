# Codex goals

Agenthail reads Codex `ThreadGoal` values through `thread/goal/get` and exposes
the protocol's objective, six statuses, usage counters, nullable token budget,
and creation/update timestamps. Goal mutations use the typed
`thread/goal/set` payload; no slash command is sent as turn text.

The CLI supports `set`, `edit`, `pause`, `resume`, `budget`, and `clear`:

```text
agenthail goal <target> set <objective>
agenthail goal <target> edit <objective>
agenthail goal <target> pause|resume
agenthail goal <target> budget <tokens|clear>
agenthail goal <target> clear
```

Codex emits `thread/goal/updated` with the complete goal and
`thread/goal/cleared` with the thread ID. The adapter preserves those typed
events for the daemon catalog/API integration.

## API and clients

The generic daemon action handler routes these actions for a writable
Codex session:

| Agenthail action | Typed adapter request |
| --- | --- |
| `goal-set` | `GoalUpdate{Objective: message, Status: active}` |
| `goal-edit` | `GoalUpdate{Objective: message}`; preserves the current status |
| `goal-pause` | `GoalUpdate{Status: paused}` |
| `goal-resume` | `GoalUpdate{Status: active}` |
| `goal-budget` | parse non-negative integer message, then `TokenBudget`; `clear` sends JSON null |
| `goal-clear` | existing `thread/goal/clear` |

The session metadata endpoint returns the adapter's `GoalState`. The session
journal carries goal updates and explicit clears; a clear has a null goal.
Clients apply these changes to the selected session without reloading its
transcript. Human CLI output includes elapsed seconds, tokens, a configured
budget and available timestamps; `--json` preserves the complete typed goal.
Blocked, usage-limited and budget-limited goals require user attention.
