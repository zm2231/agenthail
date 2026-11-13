# Building Agenthail extensions

This folder is `~/.agenthail/extensions`. Every extension is a folder here, and
Agenthail loads each one into the person's dashboard. Agenthail writes this
file and the recipes; a newer Agenthail may replace them.

You are a normal Agenthail session. Use any tool you need: read the real data
source, install packages, run commands. What you write is shown to the person
after every turn, and they keep it or undo it.

## Layout

```
<id>/
  extension.json   {"label": "Forge", "description": "...", "capabilities": ["read.all"], "mounts": [...]}
  server.ts        export default function (ah) { ... }
  ui.html          the page, served at /x/<id>/ (optional)
  assets/          files the page loads, served at /x/<id>/assets/...
```

- An id is a lowercase letter followed by up to 39 lowercase letters, digits
  or dashes. `recipes` is taken.
- `extension.json` allows only `label` (required), `description`,
  `capabilities` and `mounts`. Unknown fields make the extension invalid.
- `server.ts` runs in Node with type stripping: type annotations are fine,
  enums, namespaces and parameter properties are not. There is no build step.
- `npm install` inside an extension's folder works; `node_modules/` is not
  recorded.

## The server API

The default export of `server.ts` receives `ah` and may return a cleanup
function. Register everything while it runs.

| Member | Use |
|---|---|
| `ah.action(name, handler)` | `handler(input, {caller})` returns JSON for the page or for `agenthail extensions call` |
| `ah.job({name, everySeconds, run})` | Runs once after load, then every `everySeconds` (at least 60) after the last run ends, at most 10 minutes each |
| `ah.on(type, handler)` | Daemon events (`'*'` for all); needs the `read.all` capability |
| `ah.call(tool, args)` | Agenthail tools as the extension: `agenthail_sessions`, `agenthail_read`, `agenthail_history`, `agenthail_projects`, `agenthail_requests`, `agenthail_answer`, `agenthail_send`, `agenthail_channel_send` |
| `ah.tool({name, description, inputSchema, run})` | A tool agent sessions call as `ext_<id>_<name>`; needs the `tools` capability. `run(input, {caller, ah})`: `ctx.ah.call` acts for the calling session, limited to what this extension was approved for. Sessions see a new tool when they next start or resume |
| `ah.fetch(mount, path, init)` | A request to an approved mount's upstream, with the mount's key added by Agenthail |
| `ah.db` | The shared SQLite store (`node:sqlite` `DatabaseSync`) |
| `ah.table(name)` | The table name to use: `<id>_<name>` with dashes as underscores |
| `ah.log(...)` | A line in the extension host's log |

Capabilities: `read.all` (read every session, receive events), `control.all`
(send to any session), `requests.answer` (answer approvals and questions), `tools` (offer tools to
agent sessions).
Declare only what the extension uses.

## The page

Plain HTML and ES modules. Reach the server with:

```html
<script type="module">
  import { action } from '/x/_ah.js';
  const rows = await action('overview', {});
</script>
```

The page may fetch only its own origin, so everything else goes through an
action.

## Mounts and keys

A mount puts an existing web app behind Agenthail on its own port:

```json
{"name": "console", "label": "Proxy console", "upstream": "http://host:8317", "entry": "/", "signin": "assets/signin.js"}
```

A source that needs a key is always reached through a mount, by the page or by
`ah.fetch` on the server. The person names where the key is kept; Agenthail
reads it and adds it to requests. The page sends
`Authorization: Bearer agenthail-key` and Agenthail swaps in the real key.

## Rules

- Never put a key, token or password in this folder, the store, a log or a
  page. Not in code, not in a comment, not in a test fixture.
- A keyed source goes through a mount, and the person names the key.
- Don't run `agenthail extensions approve` or `agenthail extensions key`
  yourself. Tell the person what to approve, or which key to name, and why.
- Name your tables with `ah.table`, so undoing the extension removes them.
- Jobs run at most once a minute.
- Before you end a turn, run the extension against the real source and see it
  succeed:

  ```
  agenthail extensions call <id> <action> --input '{"...": "..."}'
  agenthail extensions run <id> <job>
  ```

  `call` and `run` load what is in the folder first. Fix what fails and run
  them again. Say what you ran and what it returned; the person sees whether a
  successful call ran against the version you leave.

## What the person sees

After each of your turns, Agenthail records what changed here and asks the
person to keep or undo it, with whether a successful `call` or `run` checked
that exact version. Undoing the turn that created an extension removes its
folder and its tables. `agenthail extensions history` lists the changes.

## Recipes

- `recipes/data-page.md`: a page that reads a data source
- `recipes/mount-app.md`: putting an existing web app in the dashboard
- `recipes/keyed-api.md`: an API that needs a key, through a mount and `ah.fetch`
- `recipes/collector.md`: a background job that collects into a table and prunes it
