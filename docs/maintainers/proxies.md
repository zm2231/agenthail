# Account proxies

Some setups send Claude Code and Codex through a proxy that holds several
subscription logins and picks one for each request. Agenthail supports
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). It shows which
accounts a proxy has, how much of each is used and whether each can take
work. It only reads; it never changes the proxy's choice.

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

The automatic account choice for new Claude sessions picks a root through a
proxy only when it is the preferred root; the proxy then picks the account.

## Reading a proxy

The daemon asks each configured proxy at most once a minute, shared by every
client and poll: `GET /healthz`, then `GET /v0/management/auth-files` with the
key. The proxy only records usage from the responses it relays, so an account
that has served no traffic has no reading yet, and an idle account's reading
ages and then drops once its window has passed. For each Claude and Codex
account it reports the status (`active`, `cooling` with when it may be tried
again, `disabled` or `unavailable`), the proxy's preference, and the session,
week and per-model week windows with the proxy's own ceiling for each.

A refused key is not offered again until the key it reads changes, and after
the proxy blocks this host for failed attempts the daemon waits that out, so
polling never adds to the proxy's failed-attempt count.

## In the usage API

`GET /api/v1/usage` adds one set per proxy, after the host's own accounts:
`{provider: "proxy", name, proxy: {name, id, url}, accounts[{id, provider,
label, status, nextRetry, preference, meters, asOf}], problem, problemDetail,
proxyProblem}`. Account meters are the same as other meters, with the proxy's
`ceiling`; a meter at its ceiling is `critical`. A root set through a proxy has
`login: "proxy"` and `proxy: {name, id}`. `id` is the proxy's scheme, host and
port; for a loopback proxy it also names the host, so two machines' own local
proxies stay apart.

Problems are explicit lines, with `proxyProblem` naming the case:

| `proxyProblem` | Line |
| --- | --- |
| `unreachable` | the proxy did not answer its health check |
| `key_missing` | no key reference on this host; add one with `agenthail proxies key` |
| `key_unreadable` | the file, keychain item or variable could not be read, or the file is readable by others |
| `key_refused` | the proxy refused the key; Agenthail stops asking until the key changes; the detail gives the command that sets a new reference |
| `banned` | the proxy blocked this host after failed attempts; retried after the block ends |
| `remote_off` | the proxy does not allow management from other machines |
| `no_management` | the proxy has no management key set |

When every Claude account is cooling or at its own ceiling the set's problem
says so and when the first frees up.

Several hosts that share a proxy each report it. The daemon, and the iPhone
across the computers it is paired with, keep one copy per `id`: one read with a
working key when any host has one. When none has a key the line says the
accounts are not readable from any host.

## In the apps and the CLI

The Mac status strip, the dashboard's top bar and the iPhone's usage line give
every proxy account a chip named like any other account, followed by the
proxy's name when another chip has the same name. Opening the line shows each
proxy as a group after the hosts, titled `shared (proxy)` and `read from <host>`
when another host read it, with each account's limits, its ceiling beside when
it was read, its cooling or disabled state, the group's problem, and the
sessions on every root through the proxy on any host. The proxy does not say
which account each session is on.

`agenthail usage` prints the same groups. `agenthail proxies [--json]` lists
each proxy with its accounts, problems and the roots that use it.
`agenthail roots` says when a root goes through a proxy. New-session forms name
such a root `shared (proxy)`.
