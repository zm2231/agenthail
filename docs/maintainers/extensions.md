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
  ui.html          the page, served at / on the extension's own port
  assets/          files the page loads, served at /assets/... there
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
| `ah.source({name, everySeconds, timeoutSeconds, collect})` | Registers a source: data a page shows (below). |
| `ah.sources()` | Every source's stored state, `{name: {name, state, value, at, error, errorAt, tookMs}}` |
| `ah.job({name, everySeconds, run})` | Registers a background job. The daemon runs it once the extension loads and then every `everySeconds` (at least 60) after the previous run ends, never two at once, for at most 10 minutes. |
| `ah.on(type, handler)` | Receives daemon events of `type`, or every event for `'*'`: `{id, type, timestamp, entityId, data}`, the same as `/api/v1/events`. Only an extension that holds `read.all` receives events. |
| `ah.call(tool, args)` | Calls an Agenthail tool as the extension (below) and returns its result, or throws an error with `code` and `message`. A write gets a fresh `clientRequestId` unless one is given. |
| `ah.tool({name, description, inputSchema, run})` | Offers a tool to agent sessions (below). |
| `ah.fetch(mount, path, init)` | Requests `path` on the upstream of one of the extension's approved mounts, with the mount's key (below). `init` takes `method`, `headers` and `body`; a body that is not a string is sent as JSON. Returns `{status, ok, headers, text(), json(), bytes()}`, or throws an error with `code` and `message`. |
| `ah.badge(value, {mount})` | Sets the badge the apps show on the extension's entry, or with `mount` on that mount's entry. `value` is a whole number (0 clears it, above 9999 shows `9999+`), text of up to 8 characters, or `null` to clear it. Returns the badge as shown, or throws an error with `code` and `message`. The daemon keeps the latest value at once and publishes `extension.badge` `{id}` at most once every 2 seconds per extension, the last change always included. |
| `ah.db` | The shared SQLite store (`node:sqlite` `DatabaseSync`, WAL), `~/.agenthail/extensions-data/store.db` |
| `ah.table(name)` | `<id>_<name>` with dashes as underscores: the name an extension's own tables use |
| `ah.log(...)` | A line in the extension host's log |

Register actions, sources, jobs and handlers while the default export runs;
later calls are refused. Sources and jobs share one set of names.

## Sources

A source is how a page shows data without waiting for it. The daemon runs
`collect({signal, now})` like a job: once the extension loads, again after
every reload or undo (so new code is collected at once, not after an
interval), and every `everySeconds` (at least 10, 60 if omitted) after the
previous run ends, never two at once. A run is stopped after `timeoutSeconds`,
a whole number below `everySeconds` and at most 30 (by default the smaller of
30 and one less than the interval); `signal` aborts then. A collector that
ignores `signal` still holds its source, through reloads too: no other run of
it starts until that one settles, and the daemon tries again every second, so
the next run starts as soon as it does. The host stores the
JSON result, at most 512 KiB, in the extension's table `<id>__sources` with
when it was collected and how long it took. A run that throws or times out
records its error beside the last good value instead of replacing it. When a
run ends the daemon publishes `extension.source` with `{extension, source,
ok}`.

Pages read sources from their own port, with the `read` scope like the
page's own files:

| Request | Answer |
|---|---|
| `GET /s` | `{sources: [{name, state, at, error, errorAt, tookMs, everySeconds}]}`, without values |
| `GET /s?name=<name>` | `{source: {name, state, value, at, error, errorAt, tookMs, everySeconds}}` |

`state` is `collecting` before the first run ends, `ok`, or `failed` (with the
last good `value`, if any).

Every extension shares one store, so one extension can read another's tables.
Keeping to `ah.table` names is what lets Agenthail find and remove an
extension's data.

## Tools for agent sessions

An extension that holds `tools` offers each tool it registers to every agent
session on this host as `ext_<id>_<name>`, at most 64 characters. Extensions
are never offered each other's tools. `inputSchema` is a JSON Schema object
with `type: "object"` (that alone if omitted); the daemon advertises it and
checks only that the arguments are a JSON object of at most 64 KiB.

Each tool's listing (name, description and schema) may encode to 2048 bytes
and all extensions' tools together to 8192, beside the built-in tools' pinned
size. Extensions are counted in id order, and one that would pass either limit
offers none of its tools; `agenthail extensions list` and the dashboard say
why. The MCP endpoint is stateless and sends no `list_changed`, so a session
sees tools added or removed when it next starts or resumes, and a call to a
tool no longer offered is refused. While a reload runs, sessions are offered
no extension tools.

`run(input, {caller, ah})` gets `caller` as `{kind: "session", sessionId,
actor}`. `ctx.ah.call` uses a token that lives for the call (at most 60
seconds) and acts as the calling session, with the same tool list as
`ah.call` and only the session's capabilities the extension's own allow:
`read.*` if it holds `read.all`, `control.*` if it holds `control.all`, and
`requests.answer` if both hold it. Writes through it are the session's. The
result is limited to 16 KiB like any tool's; an error thrown without a `code`
answers `tool_failed`.

## The page

The page is plain HTML and ES modules, with no build step. It reaches its
server through `/_ah.js`:

```html
<script type="module">
  import { action } from '/_ah.js';
  const health = await action('health', { verbose: true });
</script>
```

`action(name, input)` posts to `/a/<name>` and returns the handler's
result, or throws an error with `code`.

`source(name, render)` calls `render` at once with `{state: 'collecting'}` or
what is already stored, fetches the source on its own, and calls `render`
again whenever `extension.source` names it, and every 30 seconds so ages stay
current. Each source is fetched separately, so none waits on another.
`sources({name: render, ...})` watches several, and `age(state)` words a
state as "updated 2 min ago", "collecting" or "failed 1 min ago: ...,
showing 5 min old". The seeded recipes build every page this way. Inline scripts and styles are allowed;
the page may fetch only its own origin and may be framed only by itself and
the dashboard. The dashboard's own pages still refuse every frame.

## The page's own origin

Each extension with a page gets a port of its own on `127.0.0.1`, from the
pool mounts use (7420 to 7619), kept in
`~/.agenthail/extensions-data/mounts.json` under the extension's id while it
has a page. Ports are origins, so the browser keeps a page from reading
another extension's routes, the dashboard's API, or a mount. The dashboard's
own listener serves no extension pages. While remote access is on the port is
published in Tailscale Serve at `https://<machine>.<tailnet>.ts.net:<port>`,
like a mount's. The extensions view gives each page as `{port, entry,
published}`, the shape of a mount, so the dashboard and the apps build both
URLs the same way and refuse an unpublished one over https.

The port serves only its own extension:

| Request | |
|---|---|
| `GET /`, `GET /assets/...` | the page and its files |
| `GET /_ah.js` | the page SDK, with the event cursor when it was served |
| `POST /a/<name>` | an action (`control` scope) |
| `GET /s`, `GET /s?name=<name>` | sources, as above |
| `GET /events?after=<cursor>` | the event stream, holding only `extension.changed` and `extension.source` for this extension, and `stream.reset` |

Every request is checked as a mount's is: the dashboard cookie or a paired
device's token, `read` for GET and HEAD, `control` for anything else, and
with the cookie alone a write must come from the port's own origin. A request
whose Host is neither loopback nor this machine's tailnet name gets 421.
Ports share a site, so the browser sends the dashboard cookie to each of them;
it is `HttpOnly` and `SameSite=Strict`, so a page can neither read nor replace
it. A page may still send a blind GET to another port, so no GET route on the
dashboard or on an extension port changes anything; a test sends every one
and fails on any change. A page is framed only by itself and the dashboard.

## Signing in an app's web view

The Mac and iPhone apps open pages and mounts in a web view, which cannot send
a bearer token with every request a page makes. An app posts its own token to
`POST /api/v1/web-session` and gets back a cookie to set:
`{"name": "agenthail_web", "value", "path": "/", "expiresAt", "scopes"}`.

- Only a bearer can start one; a cookie cannot. Its scopes are `read` and
  `control`, less any the minting token lacks.
- It lasts 12 hours and lives in the daemon's memory, so a daemon restart
  ends it. Every request authenticates the minting token again, so revoking
  that device ends its web sessions at their next request.
- It counts only on extension ports: on a page's port for the whole page,
  its actions, sources and events, and on a mount's port for the whole mount,
  `read` for GET and HEAD and `control` otherwise. The dashboard's port never
  takes it: an API path answers 401, and the dashboard serves no `/x/` paths.
  The mount never passes it to the app.

## Entries in the apps

The apps list every page and mount in an Extensions hub. An extension asks
for an entry of its own, a sidebar entry on the Mac and a tab on iPhone and
iPad, with `app`, on the manifest for its page or on a mount for that mount:

```json
{"label": "Board", "app": {"label": "Board", "icon": "chart.bar.xaxis", "order": 1}}
```

| Field | Meaning |
|---|---|
| `label` | Up to 40 characters; the extension's or mount's label when omitted |
| `icon` | An SF Symbol name (lowercase words joined by dots); an unknown name shows a generic icon |
| `order` | A whole number from -1000 to 1000; lower comes first |

The person decides what is pinned: a declared entry is offered, any entry can
be pinned, and the rest stay in the hub. `GET /api/v1/extensions` carries
`app` and `badge` on each extension and mount.

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
| `app` | Optional: asks the apps to offer this mount an entry of its own (below) |

A mount serves nothing until the person approves it, and approval covers the
upstream it names: pointing the mount at another upstream waits for a new
approval. Each approved mount listens on `127.0.0.1` on a port from 7420 to
7619 that it keeps while the extension declares it, recorded in
`~/.agenthail/extensions-data/mounts.json`. While remote access is on, the
daemon publishes each mount's port in Tailscale Serve at
`https://<machine>.<tailnet>.ts.net:<port>`, and removes it when remote access
goes off or the mount goes away; it never removes a route it did not publish,
and Funnel is refused. `agenthail dashboard remote off` withdraws the mounts'
routes before it returns, itself when the daemon is not running. The dashboard's Extensions tab lists each page and
mount in its launcher and opens the one you pick beside it.

Every request to a mount is checked the way a page's is: the dashboard cookie or
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
| `tools` | offer tools to agent sessions |

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
| `status` | | `{extensions: [{id, state, error, actions, jobs: [{name, everySeconds, source}], tools, events, lastEventError}]}` |
| `load` | `{extensions: [{id, dir, token, mcpURL}], writeTools}` | as `status` |
| `action` | `{id, name, input, caller}` | `{result}` |
| `job` | `{id, name}` | `{}`; for a source, after storing its value or error |
| `sources` | `{id, name?}` | `{sources}` or `{source}`, as a page's `/s` |
| `tool` | `{id, name, input, caller, token}` | `{result}` |
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
- After a turn's commit, and after an undo, the daemon assigns mount ports,
  reloads the host and publishes `extension.changed` with `{"rev": "<rev>",
  "extensions": ["<id>", ...]}`, the extensions that change touched. The
  dashboard's Extensions tab then reads the list again, and
  reloads the page or mount it frames when its extension is among them and the
  folder's newest change moved. A page opened on its own reloads itself through
  `/_ah.js`, which the daemon serves with its event cursor at that moment, so
  the page hears only changes made after it loaded, and only its own; it
  reloads too when the stream reports it missed events.
- A session's first change raises an ask of origin `extension-change` to the
  person: `<session> changed extensions`, with `Keep` and `Undo`. Later turns
  of that session reword the same ask instead of raising another, and push
  nothing new: `<session> changed extensions in 3 turns`, `Keep all` and
  `Undo last`. Keep closes the ask. Undo last undoes the newest pending turn
  and rewords the ask, or closes it when no turn is left. Each extension's
  History on the Extensions tab lists the latest 30 changes that touched it
  with Show (the patch), Undo, and Keep for each change still waiting, so one
  change can be kept or undone on its own. Only the person
  answers; every session, including one that may act for the person
  elsewhere, is refused with `403 forbidden`. What is pending is kept
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
New extension on the dashboard's Extensions tab, starts an ordinary session as
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
run is recorded without a hash, and so checks nothing, when the folder changed
while the host loaded it or the extensions reloaded while it ran. A
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
with `409` and nothing is granted. Only the person answers, as for Keep and Undo. A key place too
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
agenthail extensions history [<id>]    the folder's changes, or one extension's, newest first, with who made each
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
instead. `{"action": "keep", "rev": "<rev>"}` keeps one change still waiting in a
Keep/Undo ask: the ask drops it, and closes as kept when none is left.
`GET /api/v1/extensions/history/<rev>` returns the change and its `patch`, cut
at 256 KiB with `truncated` set. `GET /api/v1/extensions/history?limit=50` lists the changes (`read`
scope), with `&id=<id>` only those that touched that extension, each still
with every file it changed since undo reverts all of them: each with `rev`, `author`, `time`, `message`, `sessionId`, `turnId`
and, per extension, `created`, `removed` and the files `added`, `updated` and
`deleted`, `pending` while the change waits in a Keep/Undo ask, and `root`
for the first commit, which can't be undone.
