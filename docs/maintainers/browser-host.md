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
requests `{id, method, params, token}`, responses `{id, result}` or
`{id, error: {code, message}}`, one line at most 1 MiB.

Agents run as the same user as the daemon, so the socket's mode alone would
let any of them name another session as `owner`. The daemon writes a fresh
random secret as the first line of the host's stdin, a pipe only it holds; the
host listens only after reading it (and exits if it is shorter than 32
bytes), and answers `unauthorized` to every request whose `token` is not that
secret, `hello` included.

| Method | Result |
|---|---|
| `hello` | `{protocol, hostVersion, pid}` |
| `status` | the status below |
| `launch` | downloads Chromium if absent, starts it, returns the status |
| `close` | closes Chromium, returns the status |
| `tabs.open`, `tabs.navigate`, `tabs.snapshot`, `tabs.screenshot`, `tabs.act`, `tabs.wait`, `tabs.dialog`, `tabs.resize`, `tabs.list`, `tabs.close`, `tabs.closeOwner` | see [Tabs](#tabs) |

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
- `tabs.snapshot` takes the page's accessibility tree in Playwright's `ai`
  mode, with `[ref=eN]` element refs, writes all of it to a file (see
  [Output files](#output-files)) and returns `file`, `bytes`, `lines` and
  `snapshot`. When the whole result fits the caller's `limit` (encoded bytes,
  counting `<`, `>` and `&` as the daemon's six-byte escapes), `snapshot` is the
  whole tree and `truncated` is false. Otherwise `snapshot` is its head, cut at
  a line to fit, `truncated` is true and `full` says, in words a model reads,
  that the whole tree is in `file` and its refs are valid until the next
  snapshot. The daemon passes `limit` 512 bytes under its 16 KiB tool result
  limit, so a snapshot is never refused for its size. Every tab result cuts
  the page's URL to 2 KiB, its title to 512 bytes and a dialog's message to
  1 KiB, each ending in `…` when cut, so a page cannot push a result over the
  limit through them.
- `tabs.screenshot` writes a PNG of the viewport, of the whole scrollable page
  (`fullPage`) or of one element (`ref`, not with `fullPage`) and returns
  `file`, `bytes`, `width` and `height`.
- `tabs.act` takes a `kind` (`click`, `type`, `select`, `hover`, `drag`,
  `press`, `scroll`, `upload`) and a `ref` from the latest snapshot, with a
  10-second limit. `tabs.wait` waits for text, a ref, a URL part or network
  idle, at most 30 seconds.
- A JavaScript dialog is held until `tabs.dialog` accepts or dismisses it.
  Any page operation (`open` or `navigate` while the page loads, `snapshot`,
  `act`, `wait`, `resize`) during which one opens returns at once with
  `dialog: {type, message}`; `snapshot` then carries no tree, and `act`
  answers `dialog_open` until the dialog is answered.

## Output files

Snapshots and screenshots go under `~/.agenthail/browser-host/out/`, in one
directory per owner (named by a hash of the owner id, mode 0700; files 0600).
Each owner keeps at most 64 MiB: a write removes that owner's oldest files
beyond it, and a single result larger than that is refused (`too_large`).
`tabs.closeOwner` removes the owner's directory, and a snapshot or screenshot
still running when its tab closes writes nothing and answers `not_found`. When
Chromium goes away the tabs go with it and `out/` is cleared; the host also
clears it when it starts. A path is returned only to the owner whose tab
made it; like the socket, this keeps sessions apart by convention, since all
of them run as the same user.

## Servers

The `servers` action finds what a tab can open on this host. It runs in the
daemon (`internal/listeners`), not the browser host, and works with no host
running. It never connects to a port:

- Linux reads the LISTEN rows of `/proc/net/tcp` and `/proc/net/tcp6`, maps
  each socket inode to the lowest pid holding it through `/proc/*/fd`, and
  reads that process's command and starting environment from `/proc`.
- macOS runs `lsof -nP -iTCP -sTCP:LISTEN` and reads each owner's environment
  with the `kern.procargs2` sysctl.

A listener belongs to the session named by the first set identity variable
in its owner's environment, in the order the CLI resolves its caller:
`AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID` (a Claude session), then
`CODEX_THREAD_ID` (a Codex session). Agent tool commands carry these, and so
does everything they start, including a server whose shell has exited.
`session` and `mine` appear only for sessions the caller can read. A
listener whose owner this user cannot inspect has no `pid`, `command` or
`session`. The environment is the process's own claim, so `session` labels a
server and grants nothing.

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
go test ./internal/browserhost/ ./internal/daemon/ -run 'BrowserHost|AgentsTab|LedgerChecker|Servers' -race
```

The Node and Go tests download Chromium unless `AGENTHAIL_BROWSER_TEST_BROWSERS`
names a directory to keep it in; CI caches that directory.
