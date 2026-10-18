# Paired hosts

One Agenthail daemon can show and drive the sessions of other Agenthail daemons
on the same tailnet. The Mac pairs with a Linux box, the box's sessions appear
in the Mac's list, CLI, Mac app and phone, and every action on them goes
through the Mac's daemon to the box's daemon.

## Identity

Each daemon has a stable host id (`h` plus 12 hex characters, created once in
`host_identity`) and a display name (lowercase letters, digits, `.` and `-`).
The default name is the short hostname. `agenthail hosts name <name>` renames
it. `GET /api/v1/version` reports both, so a peer can tell it reached the host
it paired with.

## Pairing

Pairing reuses the device-token model the phone uses.

1. On the remote: `agenthail hosts invite` creates a five-minute, single-use
   pairing link with the `read` and `control` scopes. It needs remote access
   (Tailscale Serve), which is what gives the link an HTTPS endpoint.
2. On the Mac: `agenthail hosts add '<link>' [--address <tailnet-ip>]`. The
   daemon redeems the secret, receives an `ahd_` device token, checks the
   protocol version and host id, and stores the pairing in `hosts`.

The daemon always dials the tailnet IP and verifies TLS against the endpoint's
DNS name, so it works where MagicDNS does not resolve. Without `--address` it
asks `tailscale status --json` for the peer's IPv4 address. No new port is
opened: the remote is reached through the Tailscale Serve route it already
has. Device tokens stay in the registry and never appear in API responses,
logs or CLI output. `agenthail hosts remove <host>` revokes the device on the
remote, then drops the pairing and every mirrored session.

## Ownership and the mirror

Sessions are keyed by their raw conversation id. `sessions.host_id` names the
owner: empty means this daemon, a host id means a paired host. The `host::id`
form exists only at the API and CLI boundary.

Each paired host has one mirror loop:

- On connect, after a stream gap, or when the remote's catalog epoch changes,
  it takes a full snapshot (`/api/v1/snapshot?fresh=1`) and reconciles: rows
  the remote no longer serves are removed.
- Otherwise it follows `/api/v1/catalog-events` and applies each
  `session.upserted`, `session.removed` and `session.queue` event as it
  arrives.
- It imports only sessions the remote serves itself: a row that is already
  host-qualified, or that names another host, is skipped. Two daemons paired
  both ways therefore show each other's sessions exactly once.

A mirrored row cannot take over a local row, and discovery cannot take over a
mirrored one (`SessionOwnedError`).

## Routing

`routeSession` in `internal/daemon/hosts_federation.go` is the one owner that
decides which host a session belongs to. Session reads, streams, metadata,
model lists, attachments and actions are wrapped so that a host-qualified id,
a `host` query field, or a raw id owned by a paired host is forwarded to that
host with its raw id; the answer comes back with every session reference
re-qualified. An action cannot span two hosts, and actions that only make
sense on one daemon (channels, relays, queue and delivery records, a message
from a local source session) are refused for remote sessions.

## Unreachable hosts and delivery

A host that stops answering keeps its rows. They are re-rendered unavailable
with the host named (`host forge is unreachable`), and the host's state is one
of `connecting`, `connected`, `unreachable`, `unauthorized` or
`incompatible`.

A send to a remote session carries an idempotency key (the CLI and native
clients send one; the daemon generates one otherwise):

- If no byte reached the remote, the daemon holds the exact request as a
  pending queue row with the normal queue TTL and reports `queued`.
- If the request was written but no answer came back, the row is inflight
  with evidence `unknown`, and the CLI reports `delivery submitted`.
- While the host is connected the daemon replays held rows with the same key.
  The remote's action receipts make a replay return the first result instead
  of starting a second turn. A replay the remote still reports as in progress
  stays unknown, then becomes a dead letter marked unknown after 15 minutes.

`net/http` itself replays a request that carries an `Idempotency-Key` header
once when a reused connection fails, which is safe for the same reason.

## Targets

`host::target` resolves `target` on that host. A target without a host is
resolved on this host and on every paired host. One match is used, more than
one is an error listing each `host::target`, never a pick. With the daemon
stopped, the CLI still checks mirrored rows by exact id and alias so a local
match cannot shadow a remote one silently.

`send`, `steer`, `interrupt`, `reply`, `last`, `stream` and
`thread create --host <host>` reach paired hosts. Other commands refuse a
remote session by name. `list` adds a HOST column once a host is paired and
marks an unreachable host's rows `down`.

## Where an empty host_id means local

Every place that treats `host_id = ''` as "this daemon's session":

- `Registry.ResolveTarget`: exact id, id prefix and name or cwd queries.
- `Registry.ListSessions`, `Registry.SearchSessions` and
  `Registry.WatchedSessions`.
- `Registry.RecordCatalogDiscovery` and `Registry.MarkCatalogDiscoveryFailure`:
  local discovery never ages out or fails a mirrored row.
- `Registry.RecordCatalogSession`: refuses a session another host owns.
- `CatalogSnapshot` and the catalog page: a mirrored row with an unavailable
  reason is stale.
- The `message_queue` triggers: queue changes on a mirrored session emit no
  local `session.queue` event.
- `claimCandidateFilter` and `RecoverInflight` skip held host deliveries
  (`host_request <> ''`); only the host delivery loop touches them.
- `dashboardState` and `publishCatalogQueueCounts` render mirrored rows from
  their stored projection.
- The CLI `list` renders rows with a host id from their projection.
