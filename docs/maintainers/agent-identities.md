# Agent identities: a name and an inbox for an agent outside Agenthail

An agent that is not an Agenthail session, such as an assistant that runs shell commands on a machine with Agenthail installed, can send with `agenthail send`. Without an identity its messages come from the Agenthail CLI, which nothing reads, so no one can answer it. An agent identity gives it a durable `@name`: its sends name it, and messages sent to it wait in an inbox it reads with the CLI. No model runs behind an identity.

## Use

```
agenthail identity add scout
export AGENTHAIL_SESSION_ID=scout           # or pass --from scout on each send
agenthail send @builder "the deploy is green"
agenthail inbox --wait 10m                # prints unread messages and marks them read
```

| Command | What it does |
| --- | --- |
| `identity add <name>` | Records the identity and binds `@<name>` to it. |
| `identity rm <name>` | Removes the identity, its alias and its inbox. |
| `identity list [--json]` | Lists identities with unread and total counts. |
| `inbox [<name>] [--all] [--peek] [--limit <n>] [--wait <duration>] [--json]` | Prints unread messages oldest first and marks them read in the same transaction, so two readers never both take one. `--all` includes read ones, `--peek` leaves them unread, `--wait` blocks until one is unread and exits 1 when the time runs out. The name defaults to the identity in `AGENTHAIL_SESSION_ID`. |
| `inbox [<name>] --prune <age>` | Deletes read messages received before an age such as `30d` or `12h`. Unread messages are never pruned. |

Each message shows who sent it as a reply address on this host: `@builder`, `laptop::@builder` for a session on a paired host, or the session id when it has no alias. `agenthail send <that address> ...` answers it.

A name follows the alias rules: lowercase letters, digits, `.`, `_` and `-`, at most 64, starting with a letter or digit. A name an alias, a coordinator, an alias reservation or a session id already holds is refused, and so is `lead`. `identity rm` refuses while `@<name>` names another session.

## How it is stored

An identity is a local session row (`sessions`, id `agent:<host id>:<name>`, surface `agent`, runtime kind `agent-inbox`) with its alias in `aliases`, plus a row in `agent_identities`. The host id in the session id keeps two machines' identities of the same name apart once they pair, since paired hosts key mirrored rows by session id. Its messages are rows in `agent_inbox`, which keeps the full text, the sender's id, the reply address the sender had when it arrived, and when it was read. A message's id is the delivery intent's uuid, so a send replayed to this host is stored once. Both tables arrived with schema 114.

The `agentinbox` adapter serves the `agent-inbox` runtime. Its `Send` stores the message and reports it delivered; the dispatcher takes the inbox route for it, which records the intent as sent but starts no turn (`Route.StartsTurn` is false, so neither a direct send nor a queued one marks a turn started), and its receipt says the message is in the inbox and that nothing replies. `send --reply` and `--stream` to an identity are refused before anything is sent. Interrupt, steer, goals, compaction and models are refused. `agenthail last @scout` lists the inbox without marking anything read.

The adapter lists identities to the catalog like any surface, so each identity has a catalog row. The session list leaves identity rows out (`CatalogListing`), the way it leaves out idle subagents, and clients get no `agent` surface: no health chip, no filter and nothing to start (`surface.Listed`). A paired host's mirror reads every catalog row, so it carries identities.

## Sender and reach

`--from scout` and `AGENTHAIL_SESSION_ID=scout` resolve to the identity through the same lookup as any session. The message is labeled `[from @scout]`, and a Claude peer message names `agenthail/agent: @scout`. Failure and held notices for an identity's own sends go to its inbox. Local Claude sessions get each identity, and each one a connected paired host mirrors, as a peer however long it has been quiet (`CatalogActiveSessions` keeps identity rows out of the activity window), so a Claude session can answer it with its own send.

An identity has the reach of the Agenthail CLI: the CLI does not check it as an agent session, so it is not held to one project. It only names the sender of a message the CLI could already send without one, and grants nothing the person running that CLI does not already have.

In the other direction, any agent may send an identity a queued message, from any project, on this host or a paired one. An identity belongs to no project, so the project reach that limits an agent session (`mcpControlTarget` here, `mcpRemoteTarget` for a paired host's session) admits it, `remoteSendScope` names no project for it, and the owning host's `sendScope` lets it through even when an older sender names a project. A send now (`mode: now`) to an identity on a paired host is refused like any send now across hosts.

## Across paired hosts

The pairing carries an identity both ways with no new path (see [paired hosts](hosts.md)):

- From the identity's host, a send to a paired host's session goes qualified as `<host id>::agent:<host id>:scout` with the name `@scout`. The receiver mirrors that row, so the message's sender is the mirror and the receiver addresses it as `server::@scout`. A reply routes to the identity's host and lands in the inbox.
- From a paired host, `agenthail send server::@scout ...` resolves through the mirror. While the identity's host is unreachable the send is held and replayed with its idempotency key, and the inbox keeps it once.
- A one-way pairing falls back to the unrepliable sender row, as for any session.

After `identity rm`, the next discovery passes retire the row and paired hosts drop their mirror; a send that still reaches the host is refused with `no such agent identity`.

## Remote MCP

An agent that runs on no machine you pair, such as a hosted agent, uses an identity through the daemon's `/mcp` from the internet. It is the same endpoint with the same tools; the caller is the identity.

```
agenthail identity add scout
agenthail identity access set --team example-team --aud <application AUD tag>
agenthail identity remote scout --access-client-id <service token Client ID>
```

`identity remote` prints a token starting `ahr_` once and keeps only its SHA-256 hash (`identity_remote`, schema 115). Running it again, or with `--rotate`, issues a new token and the old one stops working; `--off` removes remote access and keeps the identity and its inbox. `identity list` and `agenthail doctor` show each binding and when it last called.

Each request carries the token as `Authorization: Bearer ahr_...`. With Access settings set (`mcp_access`), it must also carry the `Cf-Access-Jwt-Assertion` header that Cloudflare Access adds. The daemon checks that JWT's RS256 signature against the team's key set (`https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, fetched again at most once a minute for a key id it does not know), its issuer, its audience and its time with 60 seconds of skew. The JWT must belong to the identity the token names: a service token, whose JWT has an empty `sub`, by its Client ID in `common_name` (compared lowercased and without `.access`), or a person's login by `email` with `--access-email`. Either credential alone, or two that name different identities, gets 401. Without Access settings the token alone gets in, and a request that carries an Access header is refused, since nothing here can check it; in that mode, publish only `/mcp` to the daemon. With either mode, a request that came through Access for any path other than `/mcp` gets 404.

The remote caller has the identity's reach: the person's, as in [Sender and reach](#sender-and-reach). Its MCP capabilities are the operator's (`control.all`, `read.all`). Every tool call it makes is recorded in history as `mcp-remote`, naming the tool and how it got in, never the token.

The inbox is read and acknowledged under this contract:

1. A read (`agenthail_read` with no `sessionId`) returns the oldest unread messages that fit in 12 KiB of encoded output, with `remaining` and an `ack` token, `inbox-ack:<id>`, naming the last one. It records those messages as returned (`agent_inbox.delivered_at_ms`) and marks nothing read.
2. An ack, passed as `ack` on the next read (a `bodyRef` read included) or as `cursor` to `agenthail_wait`, marks read every message of the caller's own inbox that a read has returned, up to the id it names. A value in another form, such as a session's `latestSeq`, is refused.
3. If a reply is lost, its messages stay unread and the next read returns them again, first.
4. Repeating an ack changes nothing, even after its messages are pruned.
5. An older ack marks only up to its own id. An ack naming a later id, even one never returned, marks only messages already returned to that identity.

An ack can only affect the caller's own inbox, and a client that follows the loop never acknowledges past what it received: after a lost reply its next read returns the lost messages first, oldest first, so its next ack covers only messages it has seen. The server therefore keeps no record of which tokens it issued; that would only guard a client against itself.

A message whose encoded text is over 4 KiB comes back clipped with `truncated`, its full size in `bytes` and a `bodyRef`; `agenthail_read` with that `bodyRef` (and `offset`, from `nextOffset`) pages its whole text, acknowledged or not. `agenthail_wait` with `tasks: []` returns once a message is unread, so an agent long-waits on it to learn of mail. The loop is: wait, read, handle, then read with `ack` (which also returns anything newer), and wait again with `ack` as `cursor`.

### Push and the session lifecycle

The transport is stateful Streamable HTTP, so the daemon can tell an identity when mail arrives. Every request, the event stream (GET) included, is still checked on its own as above.

- **Contentless.** A notice carries no sender and no text: `notifications/resources/updated` for `agenthail://inbox/<name>` if the session subscribed to it, and a `notifications/message` at level `notice` with `{inbox, unread, next}` if the session set a logging level. Reading the resource gives the same count. Message text is reached only through `agenthail_read`, a verified request.
- **Open.** `initialize` opens an SDK session, which the SDK binds to the identity id it was verified as; a request from another identity naming that session is refused.
- **Bind.** The first verified message of an initialized session binds it to the notifier with the identity and the credential epoch its request was verified under. If the epoch has moved on since, the session is closed instead of bound. The binding lasts until the SDK ends the session, never longer and never shorter. A new, reconnected or resubscribed session, and one that sets a logging level, is told of everything still unread, even if a notice pass was already under way, so a dropped stream or a daemon restart loses nothing. A notice the session could not be sent is tried again on the next pass; one sent while its event stream is down waits in the SDK's event store for the client to resume.
- **Revoke.** `mcp_remote_epoch` holds one counter that SQLite triggers raise in the same transaction as any change to a remote token or binding, removal of remote access or of the identity, and any write to the Access team or audience (last-seen and key-status writes do not count). The counter never repeats, so clearing and restoring the same settings, or two rotations in one millisecond, still count as changes. Each notice pass closes every session bound under an older epoch as it picks recipients, and checks the epoch again just before it sends. A change landing after that last check can still let one contentless notice through before the next pass closes the session; that is what keeping notices contentless is for. Any remote credential change closes every remote session; clients reconnect with whatever credential is now valid and are told of their unread mail again.
- **Expire.** A session with no request for an hour is closed (a request in flight, such as a long `agenthail_wait`, pauses that), and the daemon closes every remote session, bound or not, when it stops. The daemon does not ping clients: the SDK's keepalive closes the session of any client that keeps no event stream open, and a client that only calls tools would then find its session gone.

The notifier follows the inbox's change signal and also sweeps every 5 seconds.

Cloudflare's side (the Access application, its service token, the tunnel and DNS) is set up in Cloudflare; the daemon only checks what arrives.

## Known gaps

- The Mac and iPhone apps and the web dashboard do not show identities or their inboxes. Messages from one show its name like any agent's.
- `agenthail inbox` reads the local registry, so it runs on the identity's own host. Off that host, use the remote MCP.
- `notifications/message` reaches only a client that sets a logging level, and MCP logging is deprecated in the 2026-07-28 protocol revision; `resources/updated` reaches only a subscriber. A client that does neither learns of mail with `agenthail_wait`.
