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

Each request carries the token as `Authorization: Bearer ahr_...`. With Access settings set (`mcp_access`), it must also carry the `Cf-Access-Jwt-Assertion` header that Cloudflare Access adds. The daemon checks that JWT's RS256 signature against the team's key set (`https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, fetched again at most once a minute for a key id it does not know), its issuer, its audience and its time with 60 seconds of skew. The JWT must belong to the identity the token names: a service token, whose JWT has an empty `sub`, by its Client ID in `common_name` (compared lowercased and without `.access`), or a person's login by `email` with `--access-email`. Either credential alone, or two that name different identities, gets 401. Without Access settings the token alone gets in, and a request that carries an Access header is refused, since nothing here can check it; in that mode, publish only `/mcp` to the daemon. With either mode, a request that came through Access for any path other than `/mcp` gets 404. A request to `/mcp` with no bearer that came through Cloudflare (it carries `Cf-Ray` or `Cf-Connecting-Ip`, which the edge sets on every request it proxies) gets 401 with `WWW-Authenticate: Bearer realm="agenthail"`, so a client that has only the URL asks the person for the token.

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

Notices are contentless: no sender and no text. A session subscribed to `agenthail://inbox/<name>` gets `notifications/resources/updated`, and a session that set a logging level gets a `notifications/message` at level `notice` with `{inbox, unread, next}`. Reading the resource gives the same count. Message text is reached only through `agenthail_read`, a verified request.

The credential epoch (`mcp_remote_epoch`) is one counter that SQLite triggers raise in the same transaction as any change to a remote token or binding, removal of remote access or of the identity, and any write to the Access team or audience; last-seen and key-status writes do not count. It never repeats, so clearing and restoring the same settings, or two rotations in one millisecond, still count. Any change retires every remote session; clients reconnect with whatever credential is now valid.

| State | Event | Next | Sent |
|---|---|---|---|
| open | `initialize` (the SDK binds the session to the identity it was verified as; another identity naming it is refused) | open | nothing |
| open | first verified message once initialized, verified under the current epoch, daemon not stopping | bound | the unread count |
| open | that message was verified under an older epoch, or the daemon is stopping | retiring | nothing; the request is refused |
| bound | subscribe or set a logging level | bound | the unread count again, even if a notice pass is under way |
| bound | notice pass with new mail, epoch unchanged at the pass's last check | bound | one contentless notice |
| bound | notice pass finds the epoch has risen | retiring | nothing |
| bound | daemon stops | retiring | nothing |
| bound | client ends the session, an hour with no request (a request in flight pauses this), or the connection ends | gone | nothing |
| retiring | the SDK ends the session | gone | nothing |

Binding reads the epoch and records the session under one lock, so a notice pass either sees the session or read its epoch first. Every request on a bound session runs under the session's own context. Retiring cancels that context, which ends any request in flight such as a long `agenthail_wait`, and closes the session; it stays tracked until the SDK ends it, so it is never bound again or retold. A notice that could not be sent is tried again on the next pass; one sent while the event stream is down waits in the SDK's event store for the client to resume.

Two windows are accepted, both because notices are contentless: a credential change landing after a pass's last epoch check can let that pass's notice through, and a retiring session keeps its subscription until the SDK ends it, so it can still get the `resources/updated` broadcast meant for another session of the same inbox. Either way it learns only that mail is waiting, and reading needs a valid credential. Holding notices back until a retiring session is gone would delay every live session of that inbox for as long as the close takes, with no limit.

When the daemon stops it refuses new remote requests (503), cancels and drains the requests already admitted, then retires every remote session, bound or not. If an admitted request has not finished within 2 seconds, the stop goes ahead after retiring them, and they are retired again in the background once every admitted request has exited, so a session such a request opens is closed when the request ends. The daemon does not ping clients: the SDK's keepalive closes the session of any client that keeps no event stream open, and a client that only calls tools would then find its session gone.

The notifier follows the inbox's change signal and also sweeps every 5 seconds.

### Webhook events

A client that is not connected when mail arrives can be woken by a webhook, following the draft MCP Events extension as ChatGPT implements it. The remote `/mcp` advertises `capabilities.events` in its `initialize` and `server/discover` results and serves `events/list`, `events/subscribe` and `events/unsubscribe`. These are not served on the local MCP endpoint.

There is one event, `inbox.unread`. It takes no arguments, and its data is `{"unread": <count>}`: no sender and no text. Its cursor is always null (it is not replayable) and `truncated` is always false.

- **Subscribe.** `delivery` is `{mode: "webhook", url, secret}`. The URL must be https, at most 2048 bytes, with no credentials or fragment, and a literal IP in it must be public. The secret is `whsec_` plus base64 of 24 to 64 bytes. The subscription id is `sub_` plus the first 16 bytes of the SHA-256 of the identity, URL, event name and arguments, so subscribing again updates the same subscription. An identity may hold 8; a ninth is refused with `-32602` and `data: {limit: "subscriptions", max: 8}`.
- **Verification.** Before the first delivery to a URL, the daemon POSTs a signed `{"type": "verification", "challenge": ...}` and needs a 2xx whose JSON `challenge` matches, compared in constant time. A URL that passed stays verified for that identity for 24 hours. A failure is refused with `-32015` (CallbackEndpointError, the code ChatGPT's integration documents; the draft's `-32027` is provisional) and `data.reason`: `connection_refused`, `timeout`, `tls_error`, `http_4xx`, `http_5xx`, `challenge_failed` or `blocked_address`.
- **Lifetime.** With `ttlMs` omitted a subscription lasts 7 days; `ttlMs: null` grants no expiry and returns `refreshBefore: null`; a given `ttlMs` is honored with a 5 minute floor. Subscribing again re-grants it. A new secret replaces the old one, and the old one also signs for 10 minutes.
- **Unsubscribe.** It takes `name`, `arguments` and `delivery.url`, and returns `{}` whether or not the subscription existed.

An event is sent when the identity's newest unread message is newer than the last one its subscription was told of. At most one event per subscription is in flight; mail that arrives meanwhile is told in one more event with the count at that time. Each POST carries `{eventId, name, timestamp, data, cursor}` and the Standard Webhooks headers `webhook-id` (the event id), `webhook-timestamp` and `webhook-signature` (`v1,` and the base64 HMAC-SHA256 of `id.timestamp.body` keyed with the decoded secret), plus `X-MCP-Subscription-Id`.

A 2xx acknowledges an event. Network errors, 408, 425, 429 and 5xx are retried after waits of about 15 seconds, then 1, 4 and 10 minutes (with 20% jitter, and later if the endpoint sends `Retry-After`), keeping the event id and signing each attempt afresh. A 410 deletes the subscription. A redirect, 413 or any other 4xx drops that event without retrying. Once three events in a row fail, the subscription is suspended; it then gets one trial delivery every 6 hours, and a 2xx, or subscribing again, resumes it. After a restart, an event that was mid-retry is sent again as a new event with the current count.

Outbound requests go through a dialer whose control function ([`code.dny.dev/ssrf`](https://github.com/daenney/ssrf)) refuses any address outside public unicast, checked against the IP actually connected after resolution, so a name that resolves to a private address is refused. There is no proxy, redirects are not followed, TLS 1.2 is the minimum, each request has 10 seconds, at most 4 run at once, and at most 64 KiB of a response is read.

Subscriptions are rows of `mcp_event_subscriptions` (schema 120). The secret is stored as given, since signing needs it, in the registry that only its owner can read; it is never returned, logged or recorded. A subscription belongs to the token it was made with. It is deleted when the identity's remote access is turned off or the identity is removed (a cascade), when its token or binding changes, or when the Access team or audience changes (triggers). Every attempt rereads the row joined to the identity's current token hash, and subscribing writes only if the token and epoch it was verified with are still current. History records each subscribe, refusal, unsubscribe, delivery, failure, suspension and 410 as `mcp-event`, with the URL cut to scheme, host and path.

Cloudflare's side (the Access application, its service token, the tunnel and DNS) is set up in Cloudflare; the daemon only checks what arrives.

## Known gaps

- The Mac and iPhone apps and the web dashboard do not show identities or their inboxes. Messages from one show its name like any agent's.
- `agenthail inbox` reads the local registry, so it runs on the identity's own host. Off that host, use the remote MCP.
- `notifications/message` reaches only a client that sets a logging level, and MCP logging is deprecated in the 2026-07-28 protocol revision; `resources/updated` reaches only a subscriber. A client that does neither learns of mail with `agenthail_wait` or a webhook event.
- Webhook events are not replayable, and the draft's poll and push delivery modes and its `gap` and `terminated` envelopes are not served.
