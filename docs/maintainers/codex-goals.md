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

## Parent integration contract

The generic daemon action handler should route these actions for a writable
Codex session:

| Agenthail action | Typed adapter request |
| --- | --- |
| `goal-set` | `GoalUpdate{Objective: message, Status: active}` |
| `goal-edit` | `GoalUpdate{Objective: message}`; preserves the current status |
| `goal-pause` | `GoalUpdate{Status: paused}` |
| `goal-resume` | `GoalUpdate{Status: active}` |
| `goal-budget` | parse non-negative integer message, then `TokenBudget`; `clear` sends JSON null |
| `goal-clear` | existing `thread/goal/clear` |

The session metadata endpoint should return the adapter's `GoalState` unchanged.
The catalog/event consumer should map `thread/goal/updated` and
`thread/goal/cleared` to a target-specific session goal refresh event carrying
the exact `threadId` and, for updates, the complete `GoalState`. The native
consumer should refresh the session-metadata endpoint on that event, including
when the event represents a clear. The integration belongs in the generic
daemon/API handlers and is intentionally not included in this slice.
