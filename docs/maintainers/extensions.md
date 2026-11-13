# Extensions

An extension adds a page, background jobs, event handlers and existing web
apps to Agenthail without changing the daemon. Each one is a folder in
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
| `ah.action(name, handler)` | Registers an action the page calls. `handler(input, {caller})` returns JSON. `caller.kind` is `person`, `device`, `host` or, for `agenthail extensions call` run inside a session, `session`; `caller.actor` names who it was. |
| `ah.job({name, everySeconds, run})` | Registers a background job. The daemon runs it once the extension loads and then every `everySeconds` (at least 60) after the previous run ends, never two at once, for at most 10 minutes. |
| `ah.on(type, handler)` | Receives daemon events of `type`, or every event for `'*'`: `{id, type, timestamp, entityId, data}`, the same as `/api/v1/events`. Only an extension that holds `read.all` receives events. |
| `ah.call(tool, args)` | Calls an Agenthail tool as the extension (below) and returns its result, or throws an error with `code` and `message`. A write gets a fresh `clientRequestId` unless one is given. |
| `ah.fetch(mount, path, init)` | Requests `path` on the upstream of one of the extension's approved mounts, with the mount's key (below). `init` takes `method`, `headers` and `body`; a body that is not a string is sent as JSON. Returns `{status, ok, headers, text(), json(), bytes()}`, or throws an error with `code` and `message`. |
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

## Mounting an existing web app

A mount puts a web app that already runs somewhere, such as a proxy's console
or a service's dashboard, behind Agenthail on a port of its own, so the app
keeps its own origin and paths and needs no change:

```json
{
  "label": "Forge",
  "mounts": [
    {"name": "console", "label": "Proxy console", "upstream": "http://100.124.126.9:8317",
     "entry": "/management.html", "signin": "assets/console-signin.js"}
  ]
}
```

| Field | Meaning |
|---|---|
| `name` | 1 to 40 characters, a lowercase letter followed by lowercase letters, digits or dashes |
| `label` | What the dashboard calls it |
| `upstream` | The app's origin, `http` or `https`, with no path |
| `entry` | The path the dashboard opens, `/` if omitted; it may carry a query or `#fragment` |
| `signin` | Optional: a script under `assets/` that signs the app in (below) |

A mount serves nothing until the person approves it, and approval covers the
upstream it names: pointing the mount at another upstream waits for a new
approval. Each approved mount listens on `127.0.0.1` on a port from 7420 to
7469 that it keeps while the extension declares it, recorded in
`~/.agenthail/extensions-data/mounts.json`. While remote access is on, the
daemon publishes each mount's port in Tailscale Serve at
`https://<machine>.<tailnet>.ts.net:<port>`, and removes it when remote access
goes off or the mount goes away; it never removes a route it did not publish,
and Funnel is refused. `agenthail dashboard remote off` withdraws the mounts'
routes before it returns, itself when the daemon is not running. The dashboard's Extensions tab lists each mount and
opens it in a frame.

Every request to a mount is checked the way `/x/` is: the dashboard cookie or
a paired device's token, `read` for GET and HEAD, `control` for anything else,
and with the cookie alone a write must come from the mount's own origin. A
request whose Host is neither loopback nor this machine's tailnet name gets
421, and a connection upgrade (WebSocket) must come from the mount's own
origin. On the way to the app the daemon removes Agenthail's cookie and token
and the Referer, sets Origin to the app's, and passes only the app's own
cookies. On the way back it renames each cookie the app sets to
`ah_<id>_<mount>_<name>` without its Domain, so apps on other ports of the same
host cannot read each other's cookies. It drops `X-Frame-Options`, the app's
`frame-ancestors` and `Access-Control-*` headers, allows the dashboard to frame
the app, and rewrites a redirect to the app's own origin into a path.

### Keys

An app that needs a key gets it from the daemon, never from the page. The
person names where the key is kept:

```
agenthail extensions key <id> <mount> --key-file <path>
agenthail extensions key <id> <mount> --key-env <name>
agenthail extensions key <id> <mount> --keychain-service <service> --keychain-account <account>
agenthail extensions key <id> <mount> --key-command -- <argv...>
agenthail extensions key <id> <mount> --clear
```

`approvals.json` records only that place, bound to the mount's approved
upstream. Approving a new upstream forgets it, so a key never reaches an app it
was not given for. The daemon reads the key when a request needs it and keeps
it in memory for a minute. It never writes the key to the extension's folder,
the store, a log or a page.

The page sends the placeholder `Authorization: Bearer agenthail-key`. The
daemon swaps in the key only when the browser marks the request
`Sec-Fetch-Site: same-origin`, so another page cannot spend it. The `signin`
script is how the placeholder gets into the app: while a key is named, the
daemon adds `<script src="/_agenthail/signin.js">` to the app's HTML pages when
the dashboard frames them or the person opens them directly, and serves the
extension's script there. A script for an app that keeps its key in local
storage can be one line:

```js
if (!localStorage.getItem('apiKey')) localStorage.setItem('apiKey', 'agenthail-key');
```

The extension's server reaches the app with `ah.fetch`, and the daemon makes
the request. The path is joined to the mount's approved upstream, so an
absolute URL, a `//host` path or a `Host` header is refused, and so are
`Authorization`, `Cookie`, forwarding, `Accept-Encoding` and `Range` headers.
The daemon adds `Authorization: Bearer <key>` when the mount has a key,
follows redirects only within the upstream, stops after 30 seconds and refuses
an answer over 8 MiB. It decodes gzip itself and refuses an answer in any
other content encoding, so the key is replaced with `[redacted]` wherever the
answer repeats it, and no error names it. The daemon logs each request by extension, mount, method and
path, without the query or headers. Requests to services that need no key can
use Node's own `fetch`.

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

## History, Keep and Undo

`~/.agenthail/extensions/` is a git repository the daemon owns, so whatever an
agent changes there can be shown and undone. Git runs with no user or system
configuration and no hooks. `node_modules/`, `.DS_Store` and `.lock` are
ignored.

- When a turn completes, everything changed in the folder since the last
  commit is committed under the session's name, with the trailers
  `Agenthail-Session` and `Agenthail-Turn`, plus one `Agenthail-Also-Running`
  per other session that was mid-turn then (`alsoRunning` in the history),
  since the change may hold their edits too. A turn that changed nothing makes
  no commit. Turn starts and ends are handled one at a time in the order they
  were observed.
- When a turn starts and no other session is mid-turn, anything changed since
  the last commit is committed as `person`, `Edited by hand`. While another
  turn runs those edits may be its own, so they wait for the next turn's
  commit and its ask instead.
- The first commit holds what the folder held when the daemon first saw it; a
  folder a turn created starts empty, so its files are that turn's.
- After a turn's commit the daemon assigns mount ports, reloads the host and
  publishes `extension.changed` with `{"rev": "<rev>"}`.
- A session's first change raises an ask of origin `extension-change` to the
  person: `<session> changed extensions`, with `Keep` and `Undo`. Later turns
  of that session reword the same ask instead of raising another, and push
  nothing new: `<session> changed extensions in 3 turns`, `Keep all` and
  `Undo last`. Keep closes the ask. Undo last undoes the newest pending turn
  and rewords the ask, or closes it when no turn is left. Only the person
  answers; a session is refused with `403 forbidden`. What is pending is kept
  in `~/.agenthail/extensions-data/changes.json`.
- Undo reverts one commit as a new commit by `person`. A revert that meets a
  later change to the same lines is abandoned with `409 conflict` and the
  folder is left as it was. Edits not yet committed may belong to a turn still
  running, so undo never commits or discards them: undoing a commit whose
  files have such edits is refused with `409 conflict` until a turn records
  them. Undoing the commit that created an extension is
  refused the same way while a later commit touched that extension. Otherwise
  the extension's folder is removed, its approval and key references are
  forgotten, its mounts give up their ports, and the tables it made in the
  store (those named with its `ah.table` prefix) are dropped. A table whose
  name another remaining extension's prefix also matches is kept.

## Building with an agent

`agenthail extensions build "<what it should do>" [--agent claude|codex]`, or
the Build box on the dashboard's Extensions tab, starts an ordinary session as
the person: Claude unless Codex is named, with `~/.agenthail/extensions` as its
working directory and the surface's own permission mode, so it asks only what
the person's other sessions ask. Its first message is "Build or change an
Agenthail extension in this folder. Read AGENTS.md first." followed by the
person's text. The person keeps talking to it to iterate, and each of its turns
is a Keep/Undo change. Run from inside a session, `build` is refused.

The daemon writes `AGENTS.md`, `CLAUDE.md` (`@AGENTS.md`) and `recipes/*.md`
into the folder from copies built into the binary, when the daemon starts with
the folder present and before a build starts. A file is written when it is
missing or when the built-in copy differs from the one last written (recorded
in `~/.agenthail/extensions-data/seeded.json`), so the person's own edit lasts
until a newer Agenthail changes that file. Each write is committed as
`agenthail`. `recipes` is not a valid extension id.

### Verified from what ran

`agenthail extensions call <id> <action> [--input <json>]` runs an action and
`agenthail extensions run <id> <job>` runs a job once, never while the same job
is running. Both first reload the extensions if the folder's files differ from
what the host loaded, so they run what is on disk. Inside a session they run as
that session.

Each run is recorded in `~/.agenthail/extensions-data/calls.json`, the newest
20 per extension: action or job, ok or the error, time, who ran it and the git
tree hash of the extension's folder that was loaded (hashed through a scratch
index, so nothing is staged). The Keep/Undo ask says `<id> verified: <name> ran
ok at 23:01 against this version` only when a successful run's hash equals the
folder's tree in the change's commit, and `<id> not verified` otherwise. A
successful run rewords open asks at once. The ask also says `<id> needs your
approval: ...` while capabilities or mounts the folder declares are not
approved.

### Approval stays the person's

`approve` and `key` run inside a session are refused with `403
ask_the_person`, and raise an ask of origin `extension-grant` to the person,
pushed to their devices: `Approve` or `Decline`, with what would be granted, or
which key place and upstream. The same request while its ask is open raises no
second ask. Approving applies exactly what the ask showed: if
`extension.json` or the mount's upstream changed since, the answer is refused
with `409` and nothing is granted. Only the person answers. A key place too
long to show in an ask is refused outright; the person names it from their own
shell. Pending requests are kept in `~/.agenthail/extensions-data/grants.json`.
This keeps an agent from approving by habit; it is not a wall, since an agent
in bypass mode on the same account can reach what the CLI uses.

## Commands

```
agenthail extensions [list] [--json]   each extension's state, page, capabilities, jobs and last errors
agenthail extensions build "<text>" [--agent claude|codex]  start a session that builds or changes extensions
agenthail extensions call <id> <action> [--input <json>]  run an action against the folder as it is now
agenthail extensions run <id> <job>    run a job once now
agenthail extensions approve <id>      grant the capabilities extension.json declares
agenthail extensions reload            read the folder again and reload every extension
agenthail extensions key <id> <mount> ...  name where a mount's key is kept, or --clear it
agenthail extensions history [--json]  the folder's changes, newest first, with who made each
agenthail extensions undo <rev>        undo one change
```

`GET /api/v1/extensions` returns the same list, each mount with its port,
state, whether it is on the tailnet and where its key is kept (`read` scope).
`POST /api/v1/extensions` with `{"action": "reload"}`, `{"action": "approve",
"id": "<id>"}` or `{"action": "key", "id": "<id>", "mount": "<mount>", "key":
{"file": "<path>"}}` (or `env`, `keychain`, `command`, or `null` to forget it)
changes it (`settings` scope). `{"action": "undo", "rev": "<rev>"}` undoes a
change. `POST /api/v1/extensions/build` with `{"message": "<text>", "surface":
"claude"}` starts the builder (`control` scope) and answers as `session-create`.
`POST /api/v1/extensions/<id>/call` with `{"action": "<name>", "input": {...}}`
or `{"job": "<name>"}` runs it (`control` scope) and answers `{result, ran,
tree, reloaded}`; `"sessionId"` runs it as that session, accepted only from
this machine. `approve` and `key` with `"sessionId"` raise the person's ask
instead. `GET /api/v1/extensions/history?limit=50` lists the changes (`read`
scope): each with `rev`, `author`, `time`, `message`, `sessionId`, `turnId`
and, per extension, `created`, `removed` and the files `added`, `updated` and
`deleted`.
