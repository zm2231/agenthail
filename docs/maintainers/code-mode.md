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

Joining a call, its actions and its output needs the records in order. `codexRecordItems` keeps a `CodexToolWindow` (the open calls, their tools and commands, and commands left running) on the session while the stream reads forward, and the page reader computes it in a forward pass over the page before building items, so a page loaded later serves the same rows a client watched stream in. The window is known once a turn start has been read; a `CommandExecution` before that, with no call in view, is served as a plain command row rather than a background notice.

Journals written by an older mapping are read again from the transcript once (`codexJournalDerivation`), so sessions opened before an upgrade show the same rows.
