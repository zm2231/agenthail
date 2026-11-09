# Completion triggers (design)

A completion trigger runs a command, or touches a file, on this host when one of its sessions finishes a turn. It lets a local operator react to a finished turn without polling. Examples: start a build, ring a bell, or update a status file another tool watches.

The CLI names `hook` and `hooks` already belong to the Claude hook installer, so this feature is called *triggers*.

## What fires a trigger

A trigger fires on the same terminal completion that sends the desktop notification (`internal/daemon/observer.go:233`, and `releaseUnobservedHold` for a held Codex turn):

- the turn's reply is done;
- no turn is active;
- no background work is still running.

The outcome is `finished`, or `failed` when the reply carries an error. A turn held while background work runs fires once, when that work clears, just as the notification does.

Only sessions this host owns fire. A paired host's completions arrive here through the mirror (`notifyMirroredCompletion`), and they fire triggers only on their own host. So a completion runs each trigger once, on the machine where the work happened. If two hosts were both configured, a mirrored completion would otherwise fire twice.

By default a session another agent started (`session.Delegated()`) fires no trigger, which matches notifications. A trigger opts in with `"delegated": true`.

## Configuration

Triggers are opt-in. They live in `~/.agenthail/triggers.json`, next to `notifications.json`. No file means no triggers.

```json
{
  "version": 1,
  "triggers": [
    {
      "name": "build-on-finish",
      "run": ["/usr/local/bin/make", "-C", "/home/me/site", "deploy"],
      "when": { "outcome": ["finished"], "project": ["repo:github.com/me/site"], "surface": ["codex", "claude"] },
      "timeoutSeconds": 120
    },
    { "name": "done-flag", "touch": "/home/me/.cache/agent-done" }
  ]
}
```

Each trigger has these fields:

- **`name`**: unique, 1 to 64 characters, `[a-z0-9-]`.
- **`run` or `touch`, exactly one:**
  - `run` is an argv array, executed directly with no shell, so nothing is interpolated or expanded. `run[0]` must be an absolute path to a regular, executable file that is not group- or world-writable.
  - `touch` is an absolute path. Its parent must exist, and Agenthail creates the file or updates its modification time.
- **`when`** (optional): each listed field narrows the match. Unknown keys are a config error.
  - `outcome`: `finished`, `failed`.
  - `project`: owner-qualified project ids.
  - `surface`: `codex`, `claude`, `notion`.
  - `session`: session ids or aliases, resolved when the trigger fires.
- **`delegated`** (optional, default false).
- **`timeoutSeconds`**: default 60, from 1 to 600.
- **`env`** (optional): extra environment variables, as literal strings.

### Validation

The file must belong to the daemon's user and be mode 0600 or stricter. A file that fails validation for any reason disables every trigger: wrong owner or permissions, invalid JSON, an unknown field, a duplicate name, a relative path, or an unsafe executable. The daemon logs the error and reports it in `agenthail triggers status` and the daemon health. It never runs part of a broken file.

### Loading

The daemon reads the file at start, and again on `agenthail triggers reload`, which goes through the local daemon API.

There is no API, MCP tool or dashboard control that writes triggers. A paired device, a remote dashboard or an agent holding an MCP token can't make the daemon run a command. Only someone who can write a 0600 file in the daemon user's home can.

## What a trigger receives

A `run` trigger gets one JSON document on stdin, at most 16 KiB, and then EOF:

```json
{
  "version": 1,
  "event": "turn.completed",
  "dedupeKey": "<host id>:<session id>:<turn id>",
  "trigger": "build-on-finish",
  "host": { "id": "...", "name": "..." },
  "session": { "id": "...", "alias": "builder", "surface": "codex", "name": "...", "cwd": "..." },
  "project": { "id": "repo:github.com/me/site", "name": "site" },
  "turnId": "...",
  "outcome": "finished",
  "completedAt": "2026-11-09T01:30:00Z"
}
```

- Strings are capped (name 200 bytes, cwd 4096). A field that would push the document past the cap is cut and marked `"truncated": true`.
- The reply text is not included. It can be long, can hold secrets, and is text an agent chose, so it's the wrong thing to feed a local program by default. A trigger that needs it can read it with `agenthail read <session id>`.

### Environment

The command runs as the daemon's user. Its environment holds only:

- `PATH`, `HOME`, `LANG`, `TZ`;
- `AGENTHAIL_TRIGGER`, `AGENTHAIL_EVENT`, `AGENTHAIL_DEDUPE_KEY`, `AGENTHAIL_SESSION_ID`, `AGENTHAIL_TURN_ID`;
- the trigger's `env`.

None of the daemon's tokens or environment are passed on.

The working directory is the daemon's state directory, not the session's folder. A session's folder can hold anything an agent wrote there.

## Execution

- Each `run` starts in its own process group.
- On timeout the daemon sends SIGTERM to the group, waits 5 seconds, then sends SIGKILL.
- Stdout and stderr are kept, the last 8 KiB of each, in the run record.
- At most 2 triggers run at once on a host. One trigger runs its completions one at a time, in completion order.
- A trigger with more than 100 runs waiting records further completions as `skipped` (`backlog full`) instead of queueing them.
- There is no automatic retry. `agenthail triggers retry <run id>` queues a run again.

## Delivery, deduplication and restarts

A completion that matches a trigger is written to a new `trigger_runs` table at the terminal-completion point.

- For a completion that also notifies, the write happens in the same SQLite transaction as the turn notice (`RecordTurnNotice`), so a completion recorded for notifications is also recorded for triggers.
- A delegated session records no notice, so its row is written on its own at the same point.

Like the notification, a completion counts only if this daemon saw the turn start or had it armed (`completionNotificationEligible`). A turn that finished while the daemon was down is still caught on the next scan, because the saved runtime state differs from what that scan observes.

Rows are keyed `(trigger, dedupe_key)` and inserted with `INSERT OR IGNORE`. A completion seen twice, for example observed again after a restart or released from a hold, runs a trigger once.

A row's state is one of:

- `pending`;
- `running`;
- `succeeded` (exit 0);
- `failed` (non-zero exit, or the executable could not start);
- `timed_out`;
- `interrupted`;
- `skipped`.

Every row also records attempts, exit code, signal, the output tails and its timestamps.

On daemon start:

- `pending` rows run.
- `running` rows become `interrupted` and do not run again. A command that may already have acted isn't repeated without the operator asking (`retry`). Triggers are therefore at-most-once per completion, unless retried. `dedupeKey` lets a command make itself idempotent if it is retried.

Rows keep the last 1000 finished runs per trigger.

This needs one migration, to the next schema number.

## Audit and visibility

- Each finished run adds a history entry (`kind: trigger`, with trigger name, session, turn, state and exit code). It shows in Audit with the other delivery history.
- `agenthail triggers status [--json]`: config path, whether it loaded, its error, and each trigger with its last run.
- `agenthail triggers runs [--trigger <name>] [--session <target>] [--json]`.
- `agenthail triggers test <name>` runs the trigger once on a synthetic completion, marked `"test": true`, and prints the run record.
- A read-only `GET /api/v1/triggers` exposes status and recent runs to the dashboard. No write endpoint exists.

## Tests

These follow the acceptance list, through the daemon and CLI boundaries:

- A finished turn runs a matching trigger once. Its stdin carries the documented fields, and its run record has exit 0 and the output tail. A non-matching trigger does not run.
- No shell: an argument such as `$(touch x); rm -rf` arrives verbatim as one argv entry.
- Timeout kills the process group, including a child that ignores SIGTERM. The run is `timed_out`.
- A failing command is `failed` with its exit code. A missing or unsafe executable is refused at load.
- Duplicates: the same completion observed twice, and a held completion released after a restart, each run once.
- Restart: a `pending` run executes after restart, and a `running` one becomes `interrupted` and does not rerun.
- Concurrency: with three triggers on one completion, at most two run at once, and one trigger's runs execute in order.
- Malformed config disables every trigger and is reported:
  - bad JSON, unknown field, duplicate name;
  - relative path;
  - group-writable file, wrong owner;
  - `run` and `touch` both set.
- A paired host's mirrored completion does not fire this host's triggers.
- A delegated session fires only triggers with `"delegated": true`.

## Layers

1. Config, `trigger_runs`, the runner, the history entry, and `agenthail triggers status|runs|test|retry|reload`.
2. The dashboard's read-only view (`/api/v1/triggers`, an Audit filter).
3. Later events, each its own decision:
   - a goal completed;
   - a session that needs you;
   - an opt-in, size-capped reply excerpt.
