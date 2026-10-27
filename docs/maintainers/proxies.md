# Account proxies

Some setups send Claude Code and Codex through a proxy that holds several
subscription logins and picks one for each request. Agenthail supports
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It shows which
accounts a proxy has, how much of each is used and whether each can take
work, and it can change which accounts the proxy prefers when you ask it to.
It never changes a preference on its own.

## Configure a proxy

Proxies are listed in `~/.agenthail/config.json` next to `claudeConfigDirs`:

```json
{
  "proxies": [
    {"name": "shared", "kind": "cliproxyapi", "url": "http://proxy.example:8317",
     "management": {"file": "/home/dev/.config/agenthail/shared-proxy.key"}}
  ]
}
```

`agenthail proxies add shared http://proxy.example:8317 --key-file <path>`
writes the same entry. `agenthail proxies key shared <key reference>` changes
where the key is kept, and `agenthail proxies rm shared` removes the proxy.

`management` says where the proxy's management key is kept, never the key:

- `{"file": "/abs/path"}`: a file only its owner can read (`chmod 600`). A
  file anyone else can read is refused.
- `{"keychain": {"service": "...", "account": "..."}}`: a macOS keychain item,
  read with `security find-generic-password`. `--keychain-service` with
  `--keychain-account` on the command line.
- `{"env": "NAME"}`: an environment variable of the daemon. `--key-env`.
- `{"command": ["/abs/path", "arg", ...]}`: a command whose output is the
  key, for a password manager such as `setec get NAME`, `op read ...` or
  `pass show ...`. `--key-command -- <argv...>`.

A key command runs without a shell, with no input, and has 10 seconds to
finish. Its trimmed output is the key, read the same way as the other
references: for each request and held only for that request. Its error output
is discarded. A command that fails, takes too long or prints nothing is a
`key_unreadable` problem that names the command and its exit status, never
its output. The command and its arguments are stored as given, so they name
where the key is kept, not the key. `agenthail proxies key` resolves a bare
command name to its full path when it saves the reference, because the daemon
may not have the same `PATH`.

The key is read when the proxy is asked, held only for that request, and never
logged, returned by the API or printed. Problems name the reference, not the
value. A proxy with no `management` is still known: roots that use it show as
going through it, and its accounts show from any paired host that has a key.

Agenthail never copies, forwards or syncs a management key between machines.
Pairing, the usage API and settings never carry one. Give a key only to the
hosts that should read the proxy's accounts; every other host shows them from
one of those. CLIProxyAPI's management key can change the proxy's settings, so
keep it as private as the proxy itself.

## Roots through a proxy

A Claude config root goes through a proxy when the `ANTHROPIC_BASE_URL` Claude
Code uses from it, from the root's `settings.json` `env` or else the daemon's
environment, has a configured proxy's scheme, host and port. Every loopback
address (`localhost`, `127.0.0.1`, `::1`) is the same host, so a proxy on the
same machine matches. When the base URL is set somewhere Agenthail cannot read,
name the proxy with the root:

```json
{"claudeConfigDirs": [{"path": "/home/dev/.work/.claude", "proxy": "shared"}]}
```

Such a root's `login` is `proxy` and it carries the proxy's `name`. Its usage
and sign-in are never read from claude.ai and it is never called an API key.
Remote Control messages to its sessions still use the browser sign-in of the
login the root holds. A base URL that matches no
configured proxy is an API key, as before. With no `proxies` entry nothing
changes.

The automatic account choice for new Claude sessions treats a root through a
proxy like a claude.ai root. Its readings are those of the proxy's Claude
account with the most headroom: one that is not cooling and is under the
proxy's own ceiling for each window, or under Agenthail's ceilings where the
proxy sets none. When every account is full, the root frees up when the first
account does: its cooling ends or its windows over a ceiling reset. A proxy
whose accounts this host cannot read ranks as a root with no reading. A
preferred proxy root is kept even when every account is full, because the
proxy waits for an account itself and leaving it would route the session
around the proxy; the reason says when the first account frees up. Disabled
and unavailable accounts never count. The new-session forms read such a root
as `shared via proxy · next: team · session 18%, week 41%`: the account with
the most headroom, the one the proxy would use next, where the status strip
names the fullest. The readings come from
`GET /api/v1/session-options/claude-usage?root=<path>`, which answers a proxy
root with that account as `best` and its windows as `usage`.
The pick only reads what the proxy reports; the proxy still chooses the
account for each request.

## Reading a proxy

The daemon asks each configured proxy at most once a minute, shared by every
client and poll: `GET /healthz`, then `GET /v0/management/auth-files` with the
key. The proxy only records usage from the responses it relays, so an account
that has served no traffic has no reading yet, and an idle account's reading
ages and then drops once its window has passed. For each Claude and Codex
account it reports the status (`active`, `cooling` with when it may be tried
again, `disabled` or `unavailable`), the proxy's preference, and the session,
week and per-model week windows with the proxy's own ceiling for each.

The usage API, including the answer a paired host asks for, and a session
start never wait on a proxy or its key. They use the last reading and refresh
it in the background once it is a minute old; the daemon starts the first read
when it starts. Until the first read lands, the set says it is reading the
proxy. The new-session preview reads through the once-a-minute cache, and a
preference plan and its change read the proxy afresh.

A refused key is not offered again until the key it reads changes, and after
the proxy blocks this host for failed attempts the daemon waits that out, so
polling never adds to the proxy's failed-attempt count.

## In the usage API

`GET /api/v1/usage` adds one set per proxy, after the host's own accounts:
`{provider: "proxy", name, proxy: {name, id, url}, accounts[{id, provider,
label, status, nextRetry, preference, preferred, setOn, setAt, meters, asOf}],
problem, problemDetail, proxyProblem}`. Account meters are the same as other meters, with the proxy's
`ceiling`; a meter at its ceiling is `critical`. A root set through a proxy has
`login: "proxy"` and `proxy: {name, id}`. `id` is the proxy's scheme, host and
port; for a loopback proxy it also names the host, so two machines' own local
proxies stay apart.

Problems are explicit lines, with `proxyProblem` naming the case:

| `proxyProblem` | Line |
| --- | --- |
| `reading` | the first read of the proxy has not finished yet |
| `unreachable` | the proxy did not answer its health check |
| `key_missing` | no key reference on this host; add one with `agenthail proxies key` |
| `key_unreadable` | the file, keychain item, variable or command could not be read, or the file is readable by others |
| `key_refused` | the proxy refused the key; Agenthail stops asking until the key changes; the detail gives the command that sets a new reference |
| `banned` | the proxy blocked this host after failed attempts; retried after the block ends |
| `remote_off` | the proxy does not allow management from other machines |
| `no_management` | the proxy has no management key set |

When every Claude account is cooling or at its own ceiling the set's problem
says so and when the first frees up.

Several hosts that share a proxy each report it. The daemon, and the iPhone
across the computers it is paired with, keep one copy per `id`: one read with a
working key when any host has one, and otherwise one from a host that has a
key over one that has none. When none has a key the line says the accounts are
not readable from any host.

## Preferring an account

CLIProxyAPI sends work to the accounts under their ceilings first, and among
those to the ones with the higher `quota_preference`. A ceiling always wins
over a preference. Agenthail changes the preference in two ways:

- **Preferred**, a toggle on each account, sets only that account's
  preference: 10 when on, 0 when off. Other accounts are not changed, so a
  proxy can prefer several accounts at once.
- **Prefer only this** sets the account to 10 and every other account of the
  same provider on the proxy to 0. Accounts of other providers and disabled
  accounts are not changed. With "move running sessions" (`--now`, off by
  default) it also releases the sessions on the accounts it turns off: each
  moves on its next request and rebuilds its prompt cache on the new account.
  An account that was already not preferred is not released. The proxy does
  not say which account a session is on, so the plan names the sessions that
  may move.

Every change is previewed first. `GET /api/v1/proxies/prefer?proxy=<id>&account=<id>&set=on|off|only&now=<bool>`
returns the plan: each account it changes with its preference before and
after, the accounts whose sessions it releases, every host with a root through
the proxy and that host's running sessions on it, and a `planId` covering the
proxy's current preferences. `POST /api/v1/proxies/prefer {proxy, account,
set, now, planId}` reads the proxy again and refuses with `plan_stale` when
anything changed since the preview; nothing is written then. Both need the
`settings` scope.

The change runs on the host that reads the proxy with its key. Any other host
forwards the request over its pairing, so the key never leaves the machine it
is on; the host holding the key accepts the forwarded request only from a
paired host's token. A toggle is one write. Prefer only writes the chosen
account first, then the others, then releases. A failed step stops the
sequence and the answer says what changed and what did not, for example
`ops set to 10; lead not changed: …; run it again to finish`. Nothing is
rolled back, because undoing would mean more writes to a proxy that just
failed one. A refused key stops writes and reads alike until the key changes.
A key that can read the proxy but not change it gets its own message. Each
change is logged on the host without the key.

An account whose preference is the highest of its provider, and above at
least one other account's, is marked `preferred`. After Agenthail sets a
preference the account adds `set on <host> at <time>`, for as long as the
proxy still has that value.

## In the apps and the CLI

The Mac status strip, the dashboard's top bar and the iPhone's usage line give
each proxy one chip, `shared · lead 64%`: the proxy's name and its fullest
account, with that account's fullest window in its tone. Opening the line
shows each proxy as a group after the hosts, titled `shared (proxy)` and
`read from <host>` when another host read it, with each account named like any
other account (followed by the proxy's name when another account has the same
name), its limits, its ceiling beside when it was read and `over cap` when a
window has reached it, its cooling or disabled state, the group's problem, and
the sessions on every root through the proxy on any host. The proxy does not
say which account each session is on.

Each account row of a proxy group has the Preferred toggle and, when its
provider has another account on the proxy, Prefer only this. Both open the
plan: each account it changes in words (`lead: not preferred → preferred`,
with the raw values in the help text), then under Affects the hosts, roots
and running sessions, and a confirm button named for the effect: `Prefer
ops`, `Stop preferring ops`, `Prefer only ops (turns off lead)`.

`agenthail usage` prints the same groups. `agenthail proxies [--json]` lists
each proxy with its accounts, problems and the roots that use it.
`agenthail proxies prefer <proxy> <account> on|off` toggles one account and
`agenthail proxies prefer <proxy> <account> --only [--now]` prefers it alone.
Both print the plan and ask before writing; `--yes` skips the question and
`--json` prints the plan without writing, so it refuses `--yes`. A proxy's
own error text is passed on with the key replaced by `[key]`.
`agenthail roots` says when a root goes through a proxy. New-session forms name
such a root `shared via proxy`.
