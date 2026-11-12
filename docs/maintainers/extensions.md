# Extensions

An extension adds a page, background jobs and event handlers to Agenthail
without changing the daemon. Each one is a folder in
`~/.agenthail/extensions/<id>/`. The daemon serves its page and checks who may
use it, and a supervised Node process, the extension host, runs its server
code.

## Layout

```
~/.agenthail/extensions/<id>/
  extension.json   {"label": "Forge", "description": "...", "capabilities": ["read.all"]}
  server.ts        export default function (ah) { ... }
  ui.html          the page, served at /x/<id>/
  assets/          files the page loads, served at /x/<id>/assets/...
```

An id is 1 to 40 characters, a lowercase letter followed by lowercase
letters, digits or dashes. A folder without `extension.json` is ignored. A
folder whose manifest has unknown fields, no label or an unknown capability,
or that lacks `server.ts`, is listed as `invalid` with the reason.

Only `ui.html` and regular files under `assets/` are served; `server.ts`,
`extension.json` and anything else in the folder are not. A symlink under
`assets/` that leaves `assets/` is refused.

## The server file

The host imports `server.ts` with Node's built-in type stripping, so it may use
type annotations but no syntax that needs compiling (enums, namespaces,
parameter properties). Imports of the extension's own files are loaded fresh
on every reload. The default export receives
the API and may return a cleanup function, called before the next reload.

| Member | What it does |
|---|---|
| `ah.id`, `ah.dir` | The extension's id and folder |
| `ah.action(name, handler)` | Registers an action the page calls. `handler(input, {caller})` returns JSON. `caller.kind` is `person`, `device` or `host`; `caller.actor` names who it was. |
| `ah.job({name, everySeconds, run})` | Registers a background job. The daemon runs it once the extension loads and then every `everySeconds` (at least 60) after the previous run ends, never two at once, for at most 10 minutes. |
| `ah.on(type, handler)` | Receives daemon events of `type`, or every event for `'*'`: `{id, type, timestamp, entityId, data}`, the same as `/api/v1/events`. Only an extension that holds `read.all` receives events. |
| `ah.call(tool, args)` | Calls an Agenthail tool as the extension (below) and returns its result, or throws an error with `code` and `message`. A write gets a fresh `clientRequestId` unless one is given. |
| `ah.db` | The shared SQLite store (`node:sqlite` `DatabaseSync`, WAL), `~/.agenthail/extensions-data/store.db` |
| `ah.table(name)` | `<id>_<name>` with dashes as underscores: the name an extension's own tables use |
| `ah.log(...)` | A line in the extension host's log |

Register actions, jobs and handlers while the default export runs; later calls
are refused.

Every extension shares one store, so one extension can read another's tables.
Keeping to `ah.table` names is what lets Agenthail find and remove an
extension's data.

## The page

The page is plain HTML and ES modules, with no build step. It reaches its
server through `/x/_ah.js`:

```html
<script type="module">
  import { action } from '/x/_ah.js';
  const health = await action('health', { verbose: true });
</script>
```

`action(name, input)` posts to `/x/<id>/a/<name>` and returns the handler's
result, or throws an error with `code`. Inline scripts and styles are allowed;
the page may fetch only its own origin and may be framed only by it. The
dashboard's own pages still refuse every frame.

## Reaching a page from another device

Pages are served by the daemon's dashboard listener (127.0.0.1:7412 by
default). With remote access on, Tailscale Serve already publishes that
listener at `https://<machine>.<tailnet>.ts.net:7412`, so a page is at
`https://<machine>.<tailnet>.ts.net:7412/x/<id>/` with no other listener. Every
`/x/` request needs the dashboard cookie, which a browser gets by opening a
one-time login link, or a paired device's token. Loading a page or asset needs
the `read` scope; an action is a POST and needs `control`, and with the cookie
alone it must come from the same origin.

## Capabilities and the extension sender

`ah.call` reaches the tools that need no session of the caller's own:
`agenthail_sessions`, `agenthail_read`, `agenthail_history`,
`agenthail_projects`, `agenthail_requests`, `agenthail_answer`,
`agenthail_send` and `agenthail_channel_send`. Any other tool answers
`not_for_extensions`.

An extension holds only the capabilities its manifest declares and the person
approved, with `agenthail extensions approve <id>` or the dashboard's Approve
button. Approval grants what the manifest declares at that moment; declaring
more later waits for a new approval. Until then the tools answer as for a
caller without those capabilities, usually `out_of_scope`.

| Capability | Grants |
|---|---|
| `read.all` | read every session on this host, and receive every daemon event |
| `control.all` | send to any session on this host |
| `requests.answer` | answer the approvals and questions it can read |

Messages and history name the extension as `extension:<id>`, labelled
`<label> (extension)`. It is never taken for the person. It reads no messages,
so a send to it is refused with a reason.

## Process and protocol

The daemon runs `runtime/node/bin/node extension-host/host.mjs --socket
~/.agenthail/extension-host.sock --version <daemon version> --store
~/.agenthail/extensions-data/store.db`, supervised like the
[browser host](browser-host.md): a fresh secret on stdin, a 0600 socket, the
`hello` check, restarts with backoff from 1 to 30 seconds, and a log at
`~/.agenthail/extensions-data/host/host.log`. `AGENTHAIL_EXTENSION_HOST`
overrides the script path. A Unix socket path holds at most 103 bytes on macOS
and 107 on Linux; with a home directory long enough to pass that, the host does
not start and its error says so.

| Method | Params | Result |
|---|---|---|
| `hello` | | `{protocol: 1, hostVersion, pid}` |
| `status` | | `{extensions: [{id, state, error, actions, jobs, events, lastEventError}]}` |
| `load` | `{extensions: [{id, dir, token, mcpURL}], writeTools}` | as `status` |
| `action` | `{id, name, input, caller}` | `{result}` |
| `job` | `{id, name}` | `{}` |
| `events` | `{events, to}` | `{}`, at once; handlers run after |

`load` replaces everything loaded: it calls each cleanup function, imports
every `server.ts` again and returns each extension's state, `loaded` or
`failed` with the error. One extension that fails does not stop the others.
The daemon runs one `load` at a time and delivers no events while one is in
flight. A `load` that fails or times out leaves the host's state unknown, so
the daemon restarts the host, and the new host's own `load` ends the wait.

Each load gives every extension a new random token. The host calls tools by
posting MCP `tools/call` to the daemon's loopback `/mcp` with that token as the
bearer, and the daemon maps the token to the extension and the capabilities it
holds now. A reload or host restart makes the previous tokens worthless.

The daemon keeps one cursor on the event hub for all extensions. When the host
is down it holds what it has not delivered (up to 1024 events) and retries
every second, so events published while the host restarts are delivered once
it is back.

## Commands

```
agenthail extensions [list] [--json]   each extension's state, page, capabilities, jobs and last errors
agenthail extensions approve <id>      grant the capabilities extension.json declares
agenthail extensions reload            read the folder again and reload every extension
```

`GET /api/v1/extensions` returns the same list (`read` scope). `POST
/api/v1/extensions` with `{"action": "reload"}` or `{"action": "approve",
"id": "<id>"}` changes it (`settings` scope).
