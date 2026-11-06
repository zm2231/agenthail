# Browser host

The browser host ([`browser-host/host.mjs`](../../browser-host/host.mjs)) is a
Node process that owns one headless Chromium per user, driven through
`playwright-core`. The daemon starts it, supervises it and reports its state;
agent tools reach the browser only through the daemon.

## Process and protocol

The daemon runs `runtime/node/bin/node browser-host/host.mjs --socket
~/.agenthail/browser-host/host.sock --browsers ~/.agenthail/browser-host/browsers
--version <daemon version>`, with the host's stdin as an owner pipe. The host
exits, closing Chromium and removing its socket, when stdin closes, on SIGTERM
and on SIGINT, so it never outlives the daemon. `AGENTHAIL_BROWSER_HOST`
overrides the script path.

The socket is mode 0600. Each connection carries newline-delimited JSON:
requests `{id, method, params}`, responses `{id, result}` or
`{id, error: {code, message}}`, one line at most 1 MiB.

| Method | Result |
|---|---|
| `hello` | `{protocol, hostVersion, pid}` |
| `status` | the status below |
| `launch` | downloads Chromium if absent, starts it, returns the status |
| `close` | closes Chromium, returns the status |
| `tabs.open`, `tabs.navigate`, `tabs.snapshot`, `tabs.act`, `tabs.wait`, `tabs.dialog`, `tabs.resize`, `tabs.list`, `tabs.close`, `tabs.closeOwner` | see [Tabs](#tabs) |

The daemon accepts a host only when `hello` reports protocol 2, the daemon's
own version and the pid it spawned, within 15 seconds. Every call is bounded by
its caller's context: when the context ends, the connection is cut, so a
wedged host cannot hold up the daemon.

## Tabs

Agents drive tabs through `agenthail_browser` ([MCP tools](mcp.md)). The daemon
passes the calling session's id as `owner` on every `tabs.*` call, and the host
keeps the only record of tabs:

- Each owner gets its own Playwright browser context, so cookies and storage
  are never shared between sessions. A tab id from another owner answers
  `not_found`, the same as a closed one.
- Tabs stay open with no client connected and between turns. Archiving a
  session calls `tabs.closeOwner`, which closes its tabs and its context.
- `tabs.list` reads the host; nothing is cached in the daemon. When the host
  or Chromium restarts, every tab is gone: `list` is empty, an old id answers
  `not_found` (tab ids are random, so one never names a newer tab), and `open`
  starts a fresh tab.
- `tabs.snapshot` returns the page's accessibility tree in Playwright's `ai`
  mode, with `[ref=eN]` element refs, cut at a line to at most 20 KiB
  (`truncated: true`). `includeImage` also writes a PNG under
  `~/.agenthail/browser-host/shots` and returns its path.
- `tabs.act` takes a `kind` (`click`, `type`, `select`, `hover`, `drag`,
  `press`, `scroll`, `upload`) and a `ref` from the latest snapshot, with a
  10-second limit. `tabs.wait` waits for text, a ref, a URL part or network
  idle, at most 30 seconds.
- A JavaScript dialog is held until `tabs.dialog` accepts or dismisses it. An
  action that opens one returns at once with `dialog: {type, message}`, and
  further actions answer `dialog_open` until it is answered.

## Chromium on first use

Chromium is not shipped. The first `launch` runs `playwright-core`'s installer
for `chromium-headless-shell` into `~/.agenthail/browser-host/browsers`, pinned
by the `playwright-core` version in `package-lock.json`. Until then the browser
costs nothing beyond an idle Node process. Chromium is launched over a pipe
(`--remote-debugging-pipe`), so it opens no TCP debugging port; the Node test
checks that no process under the host listens on TCP.

On Linux the host runs `ldd` on the downloaded executable. If shared libraries
are missing it reports `missing_libraries` with the list and the command that
installs them (`sudo <node> <playwright-core cli> install-deps
chromium-headless-shell`). Agenthail never runs that command; the person does.

## Supervision and status

`internal/browserhost.Manager` restarts a host that exits, waiting 1 second
and doubling to 30 seconds, and resets the wait after a run of a minute. On
daemon shutdown it closes the owner pipe and kills the host after 5 seconds.
The host's output goes to `~/.agenthail/browser-host/host.log`, rotated at
4 MiB.

The snapshot (`/api/v1/snapshot`) reports `daemon.browser`:

| `state` | Meaning |
|---|---|
| `unavailable` | no host is answering; `error` carries the reason and log tail |
| `absent` | the host is up and Chromium is not downloaded |
| `ready` | Chromium is installed; `running` says whether it is open |
| `missing_libraries` | Chromium is installed and cannot load; see `missing` and `install` |
| `failed` | the last download or launch failed, or `ldd` did not finish within 10 seconds; see `error` |

Each status change invalidates the snapshot's ETag.

## Packaging

`scripts/stage-node-host.sh browser-host DEST` stages the host with its locked
dependencies, the same way as the Claude host; every install path ships
`browser-host/` beside `claude-host/`.

## Tests

```bash
npm ci --prefix browser-host
npm test --prefix browser-host
go test ./internal/browserhost/ ./internal/daemon/ -run 'BrowserHost|AgentsTab' -race
```

The Node and Go tests download Chromium unless `AGENTHAIL_BROWSER_TEST_BROWSERS`
names a directory to keep it in; CI caches that directory.
