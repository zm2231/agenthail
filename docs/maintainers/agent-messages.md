# Agent messages in the transcript

Messages between agents render compactly in every client: a message another agent sent becomes one clamped row, a send this session made becomes a one-line sub-row, and runs of them fold. The daemon decides what counts as an agent message and its text; clients decide only layout.

## The `agent` field

Every timeline item the daemon serves passes `surface.PresentTimelineItem` (journal pages, `/stream` rows and provider history pages). It is computed when the item is served, never stored, so items journaled earlier get it too. An item with no `agent` field renders as before.

```json
"agent": {"direction": "in", "kind": "peer", "peer": "@builder", "channel": "ops", "summary": "Tests pass on main", "body": ""}
```

| Field | Meaning |
| --- | --- |
| `direction` | `in`: received by this session. `out`: sent by this session. |
| `kind` | `peer` (another agent), `task` (task notification), `continuation` (automatic continuation), `notice` (Agenthail notice), `send` (an outgoing send). |
| `peer` | Who: the sender without the `agenthail/<surface>: ` prefix, the target of a send, or the notice's title. |
| `channel` | The channel a post came through, without `#`; empty otherwise. |
| `summary` | Whitespace collapsed, at most 400 characters. A send without its own summary uses the message's first line. |
| `body` | Outgoing sends only: the message. Incoming items keep the body in `text`. |

Who sets it:

- `in`/`peer`: `kind=message role=peer`. A `role=user` item whose text starts with one of the envelopes Agenthail writes becomes `role=peer` with `sender` set and the envelope removed. These envelopes are `[from <label>] ` (`agenthail send --from`), `[from <label> via #<channel>] ` (a channel post) and `[from Agenthail CLI, no reply route] ` (a send from outside any session). `surface.ParsePeerEnvelope` is the only parser.
- A Claude peer message from the person who runs Agenthail (its envelope's `from-session` is the person's peer id) is their own message: `role=user origin=person`, preamble removed, no `agent` field.
- `in`/`task|continuation|notice`: `kind=event role=system` with origin `task-notification`, `auto-continuation` or `agenthail-notice`.
- `out`/`send`: a `SendMessage` tool call, or a shell call (`Bash`, `exec_command`, `shell`) whose script is one `agenthail send <target> <message>`, optionally after `cd` and joined by `&&`. `surface.ParseOutgoingSend` reads the script with `mvdan.cc/sh/v3/syntax`. A quoted argument, a here-document or here-string, or `- < file` supplies the message. Flags follow the CLI's own grammar: `surface.SendValueFlags` and `surface.SendBoolFlags` are the table `agenthail send` validates against, and words after `--` are message. A script with an expansion in the target or message, a pipe, loop, `||`, a second send, or a flag the CLI would reject gets no field, so it stays a plain tool call.

## Rendering

- Incoming: the sender, then `summary` in italics clamped to two lines. A client shows "Show more" only when the text does not fit in those two lines. Expanding shows the full `text` with its files.
- Outgoing: the tool run is split at the call, and the send shows as `"summary" → peer` under the assistant text, with "failed" when its result failed. Expanding shows `body`.
- Expanded state is remembered per item id for the session. Expand all (Mac View menu, dashboard toolbar, iPhone transcript menu) is a saved preference. While it is on, every row is expanded and nothing folds.

## Fold rule

Two or more consecutive items with `agent.direction == "in"` fold into one row, unless they are the last items of the transcript. The trailing run stays as separate rows, so what the session is reacting to stays visible. The label counts by kind:

- All `peer`: "N messages from agents", or "N messages from @x" when one agent sent them all.
- Otherwise: "N updates", followed by the counts by kind in order of first appearance, for example "3 updates · 2 agent messages, 1 task notice". A task notification is never called an agent message.

`scripts/fixtures/agent-fold-vectors.json` holds the cases; the Swift helper test and `scripts/test-dashboard-agent-messages.mjs` both check them.
