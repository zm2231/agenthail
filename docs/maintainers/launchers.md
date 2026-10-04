# Launcher backends

The launcher seam separates session creation from the place where an agent
process is hosted. The daemon/API/storage layers own persistence and may copy
the returned runtime metadata into a `surface.Session`.

## Public contract

`internal/surface` exposes `Location`, `Runtime`, `LaunchRequest`,
`LaunchResult`, `Launcher`, and the optional `Focuser` interface. A session's
`Runtime` is a pointer and is omitted from JSON when no launcher was selected;
this preserves the direct `StartSession` path.

The stable launcher IDs are `cmux`, `tmux`, `claude-bg`,
`codex-app-server`, `claude-sdk`, and `external`. `NewLaunchers` accepts the
configured surfaces so the existing `SessionStarter` implementations are
injected rather than recreated.

## Process safety

The cmux backend uses the documented `new-workspace --command` operation only
with a trusted, shell-quoted executable path and opaque intent-file path. The
command value is initial shell input, so the message, model, and other request
data are never interpolated into it. The hidden `agenthail launcher-exec`
helper reads the 0600 intent JSON, removes it before execution, and invokes
the stored argv directly.

The cmux command names and JSON envelope follow the cmux CLI contract at
`https://github.com/manaflow-ai/cmux/blob/main/docs/cli-contract.md`: use
`new-workspace`, `sessions list --json`, `select-workspace`, and
`focus-panel`. `new-workspace --json` returns a workspace reference/id;
`sessions` records use the actual snake_case `session_id`, `workspace_id`,
`surface_id`, and `pid` fields in a top-level `sessions` array. Availability
probes each required `--help`
command and does not infer support from English text such as the word
“run”.

tmux receives the target program and each argument as separate
`exec.Command` arguments through `new-session -d -P -F`; no command string is
assembled from the message. Model, name, cwd, and message are validated
before they enter an argv list.

The cmux installation is optional. Availability reports the executable as
missing instead of trying to start a session. The local development machine
used to implement this seam did not have cmux installed, so only help/docs
inspection and fake-executable tests are used here; no live terminal is
created or focused.

## Correlation and focus

Terminal launches can return an empty `SessionID`. Callers should persist the
returned location and run `Locate` after the provider catalog observes the
new session. A tmux `Location.Session` is a generated tmux server session
name, not an Agenthail/provider session ID. tmux correlation requires a live
registered session PID, matching cwd, a live pane PID, and proof that the
registered PID descends from that pane PID. cmux correlation requires a live
recorded PID from `cmux sessions list --json`; its location contains only the
workspace and surface handles.

`Focuser` is implemented by cmux and tmux. Focus is an explicit caller action,
not a side effect of `Launch`.

The `claude-sdk` implementation is deliberately a seam only: it reports
unavailable and performs no SDK/runtime work. `external` is the generic
unavailable seam until a daemon-owned external command policy is supplied.
