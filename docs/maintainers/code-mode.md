# Code mode: tool calls written as scripts

Some agents run their tools through one tool whose input is a script that calls the others, for example a Codex `exec` call holding JavaScript:

```js
const r = await tools.exec_command({ cmd: "go test ./...", workdir: "/work/demo", yield_time_ms: 30000 });
text(r.output);
```

The daemon serves such a call as the tool it runs, in the same item shape it serves for any other tool call, so iPhone, Mac and the dashboard present it with their existing tool rows. Clients need no code-mode handling.

## Detection

Code mode is detected by the shape of the call, not by configuration. A tool call is code mode when its input parses as a script (or, failing to parse, still has the `tools.NAME(` text) and calls at least one `tools.NAME(...)`. The tool's own name does not matter. A session's configuration (Codex records the code-mode tool in an `additional_tools` record) is not consulted: a call with the shape takes this path and one without it does not, so sessions that switch modes mid-way read correctly row by row.

## Presentation

`internal/surface/codemode` is surface-agnostic. `Read` parses the script with goja's parser (wrapped in an async function so top-level `await` parses) and returns each `tools.NAME(...)` call with the string properties of its first object argument. `Present` turns the calls into one row: the first call names it (namespaces such as `multi_agent_v1__` are dropped) and its string fields are the row's input, so `exec_command` reads as "Run `go test ./...`". A first call with no literal fields reads as "Calls …" with the tools the script uses. A surface plugs in by reading its own record shape and calling these two functions.

The Codex mapper (`internal/surface/surfaces`) adds what is specific to Codex:

- The script's output header (`Script completed`, `Script failed`, `Script running with cell ID …`, wall time, `Output:`) is removed; a failed script marks its result failed.
- Codex records each action a script took as an `item_completed` event (`CommandExecution`, `McpToolCall`, `CollabAgentToolCall`, `FileChange`, `WebSearch`, `Extension`, `ImageView`, `FunctionCallOutput`). The first action recorded between a call and its output is authoritative: it replaces the call row in place (same item ID) with its structured input and becomes the call's result, with failure taken from its status and exit code. The script's own output is then not served. Further actions in the same call get rows of their own.
- Several calls can be open at once. An action belongs to the open call running its command, else to the one calling its tool, else to the only call open; an action no open call claims gets a row of its own.
- A call whose `exec_command` recorded no `CommandExecution` before its output left the command running: its result reads "Running in the background." with status `background`. When that command's `CommandExecution` arrives later, outside any call, it is served as a task notice (`origin: task-notification`, title "Background command", status `completed` or `failed`) linked to the starting call by `callId`. Its summary is the command, exit code and duration, and its body adds the output's last lines. Clients render it like Claude's background task notices.
- Records that repeat what already renders (`UserMessage`, `AgentMessage`, `Reasoning`, `ContextCompaction` items), model input (`additional_tools`, `thread_settings_applied`) and `item_started` are not served. `thread_goal_updated` is a one-line goal notice. Any other record type is a compact `Codex · <type>` row with no body; raw records never reach a client.

## Read order

Joining a call, its actions and its output needs the records in order. `codexRecordItems` keeps a `CodexToolWindow` (the open calls, their tools and commands, and commands left running) on the session while the stream reads forward, and the page reader computes it in a forward pass over the page before building items, so a page loaded later serves the same rows a client watched stream in. The window is known once a turn start has been read; a `CommandExecution` before that, with no call in view and no command left running under its name, is served as a plain command row rather than a background notice.

## Background work

A command left running is also the session's background work. `Codex.codexBackground` reads the same window over the rollout (the tail on first sight, then what was appended) and lists each open background command as a `shell` task in `BackgroundTasks` (source `transcript`, `since` the starting call's time). Both the catalog's status read and the observer set it, so clients show "Waiting on background work" as they do for Claude.

Those processes live in the app-server that has the thread loaded. A thread that neither Codex Desktop nor Codex's app-server daemon lists as loaded runs none, so its tasks are dropped. A runtime that cannot be reached has nothing loaded; one whose loaded threads could not be listed keeps its last listing. A thread run in a standalone Codex process Agenthail cannot reach shows no background work. A completion appends to the rollout and clears its task at once. An unload changes no file, so the catalog clears it at the next discovery listing.

A turn that ends with background work running does not notify or relay yet. Claude starts a turn when that work reports; Codex does not. So the observer stores the turn in `session_runtime.held_completion_turn_id` (schema 40), keeps the session watched, and completes the turn once when the work stops: it finishes, the thread unloads, or the app-server goes away. A later turn takes over a held one. When no runtime can be reached, the observation fails. The observer then reads the session's background work from the surface (`surface.BackgroundSource`), which finds none because nothing reachable has the thread loaded, and completes the held turn. A `turn_finished` relay stays pending with "waiting for background work to finish" and delivers on the same release.

Journals written by an older mapping are read again from the transcript once (`codexJournalDerivation`), so sessions opened before an upgrade show the same rows.
