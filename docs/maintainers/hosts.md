# Paired hosts

One Agenthail daemon can show and drive the sessions of other Agenthail daemons
on the same tailnet. When a laptop pairs with a server, the server's sessions
appear in the laptop's list, CLI, Mac app and phone, and every action on them
goes through the laptop's daemon to the server's daemon. A daemon can pair
with any number of hosts, on macOS or Linux.

## Identity

Each daemon has a stable host id (`h` plus 12 hex characters, created once in
`host_identity`) and a display name (lowercase letters, digits, `.` and `-`).
The default name is the short hostname. `agenthail hosts name <name>` renames
it. `GET /api/v1/version` reports both, so a peer can tell it reached the host
it paired with.

`GET /api/v1/version` also names the build the daemon runs: `version`,
`revision` (the commit) and `modified` (built from a tree with uncommitted
changes). Release builds stamp all three. A daemon reports each paired host's
build in its host list, and the apps show it as information only: hosts may run
different builds as long as their protocol and federation ranges overlap.
`agenthail doctor` and the Mac app compare the local daemon's build with their
own and report a mismatch only when both name an unmodified commit and the
commits differ; the fix is to restart the daemon.

## Pairing

Pairing reuses the device-token model the phone uses.

1. On the remote: `agenthail hosts invite` creates a five-minute, single-use
   pairing link with the `read`, `control` and `host` scopes. It needs remote
   access (Tailscale Serve), which is what gives the link an HTTPS endpoint.
   The `host` scope gives the catalog stream every row, so the pairing mirrors
   subagents the session list hides; a device token without it streams the
   session list. A host paired without it must be paired again.
2. On the local host: `agenthail hosts add '<link>' [--address <tailnet-ip>]`. The
   daemon redeems the secret, sending its own host id, receives an `ahd_`
   device token, checks the protocol version and host id, and stores the
   pairing in `hosts`. The remote binds the token to the host id it was
   redeemed with (`paired_devices.host_id`), so a request on that token speaks
   for that host and no other.

The daemon always dials the tailnet IP and verifies TLS against the endpoint's
DNS name, so it works where MagicDNS does not resolve. The address must be in
the Tailscale ranges (`100.64.0.0/10` or `fd7a:115c:a1e0::/48`); anything else
is refused before dialing. `GET /api/v1/version` also reports the federation range
(`minimumFederation`, `maximumFederation`): the version of what one daemon
serves another beyond the client API, such as the launch catalog, history
filtered by session, project coordinators (version 3, see
[coordinator](coordinator.md)), and whether each `/api/v1/resolve` match
identifies its session rather than matching its name or folder, which decides
whether a CLI delivery may go to it (version 4). If the remote's protocol or federation range does not
include this daemon's, pairing fails and the device it just issued is revoked.
A paired host that later reports a range without this daemon's is
`incompatible` (shown as needing an update), and nothing is forwarded to it
until it is updated. Each connection starts with that version check, and no
read, stream, action or held send goes to the host until the latest check has
passed; a send made while it is pending is held. Within a compatible range a host can
still lack a newer client route. A daemon answers any `/api/` path it does not
serve with `404` and `error.code` `unknown_route`, never the dashboard sign-in
response, and the apps show that as the computer running an older Agenthail. Without `--address` it
asks `tailscale status --json` for the peer's IPv4 address. No new port is
opened: the remote is reached through the Tailscale Serve route it already
has. Device tokens stay in the registry and never appear in API responses,
logs or CLI output. `agenthail hosts remove <host>` revokes the device on the
remote, then drops the pairing, every mirrored session and the host's mirrored
coordinators.

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
- A mirrored row's project `id` stays as it is when it is a `repo:` key, so
  the same folder of the same repository is one project on both hosts. A
  project without a key, and every project's `localId`, is qualified with the
  host. A `repo:` id is never host-qualified, even though a key for a folder
  inside a repository contains `::`.

A conversation moved between machines keeps its id, so a local row and a
host's row can name the same session. Whichever registered first keeps it. A
host row whose id is already local is refused: the local row is unchanged and
the mirror carries on with the host's other rows. The host's copy is not
shown; instead the host's entry in `agenthail hosts`, `GET /api/v1/hosts`, the
snapshot's `daemon.hosts` and the Mac and iPhone host lists carries a notice
such as `1 session on server not shown: conversation <id> is also a local
session`. A local session row is never deleted, so the notice clears when the
host stops serving that session. While the host serves a session, neither
discovery nor any local registration can take over its mirrored row
(`SessionOwnedError`). Once the host stops serving it, the row no longer owns
the id: the next local registration takes it, and sends still held for the
host fail, as unknown when the host may have received them. A
mirror write for a host that is no longer paired is refused, and removing a
host waits for its mirror and delivery loops to exit before deleting its rows.
Pairing, attaching, detaching and removing a host are serialized by one
lifecycle lock, so each host has at most one mirror and delivery loop pair and
a host removed mid-pairing is never attached.

A mirrored row's `queueCount` is the host's own count plus the sends this
daemon holds for it. `catalog_sessions.host_queue_count` keeps the host's
count, and the registry recomputes the sum in one transaction whenever the
mirror writes the row or the held queue changes.

## Routing

`routeSession` in `internal/daemon/hosts_federation.go` is the one owner that
decides which host a session belongs to. Session reads, streams, metadata,
model lists, attachments, uploads and actions are wrapped so that a host-qualified id,
a `host` query field, or a raw id owned by a paired host is forwarded to that
host with its raw id, request body included; the answer comes back with every
session reference re-qualified. An upload for a remote session is therefore
stored on that host, and the upload id the client sends with the message is
resolved there. An action names sessions on at most one other host. Queue and
delivery records and voice are refused for remote sessions with a reason, and
opening a session's terminal is refused because the terminal is on the other
machine. The mirror stores remote rows with `runtime.focusable` cleared, and
the web, Mac and iPhone hide voice and leave remote sessions out of relay and
channel pickers; the CLI and the API reach them. A qualifier
naming this daemon on a raw id a paired host owns is an ownership conflict,
never a local action, and a `host` field that disagrees with the qualifier is
`host_mismatch`.

## Senders across hosts

A message forwarded to another host carries its sender: the forwarding daemon
qualifies a local source as `<this host id>::<id>` and adds its display name.
The receiving daemon trusts that qualifier only from a token bound to the same
host, refuses one from an unbound token (it names `agenthail hosts invite` and
`agenthail hosts add` to pair again) and refuses one for any other host as
`host_mismatch`. When the receiver is paired back and mirrors the sender, the
source becomes that mirrored row, so a reply routes back. A one-way pairing
has no route back: the source becomes an `unrepliable:<host>:<id>` operator
row named `<host>/<name> (no reply address)`.

Nothing reads the operator or an unrepliable row, so a message sent to one is
refused, never recorded as received. `agenthail send` and `/api/v1/actions`
return the reason; for an unrepliable row it names the original sender as
`<host>::<id>`. A Claude peer message to one gets a `refused` receipt, which
Claude reports to its model as undelivered, followed by a message that gives
the same reason (at most once every ten minutes per sender), and delivery
history records it as `peer_rejected`. A failed or held send from an
unrepliable row stays in the problem list; no delivery notice is queued for
it, since nothing would read one. Notices for the operator still queue, for the
person to see.

Claude peers: while a paired host is connected, its sessions, Claude included,
are advertised to local Claude sessions as peers named
`agenthail/<surface>: <host>/<name>`. Local peers keep their name without a
host. A peer message to a remote session is held for its host like any send,
with the native Claude sender as its source when one is known, and otherwise
with a `[Claude peer message from ...]` prefix.

## Relays and channels across hosts

A relay lives with its source, where its completions are observed. `relay-add`
with a remote source is forwarded to that host, with the target named the way
that host resolves it: a session of this host arrives qualified and resolves
there as its mirrored row. A target on a host the source's host has not paired
is refused with the pairing commands to run. When a relay fires toward a
session another host owns, the leg is held for that host with its sender and a
hop count one higher; the receiving daemon accepts a relay leg only from a
host token and only below the hop limit, and runs it through its own relay
queue. `GET /api/v1/relays?host=<host>` lists a host's relays with their ends
qualified for this daemon.

A channel lives where it was made. A remote member is stored by its raw id and
mirrored row; `channel-send` holds the message for that member's host and
delivers to local members directly.

A project channel has no stored members. `channel-send` to `#<project>` reads
them from the catalog when it sends: every row, local or mirrored, whose
project `id` is the project's, that is not stale or archived and is working
or was active within `surface.ActiveWindow`, the window peer advertising uses.
The project's coordinators are always included. A coordinator on this host
gets the message through `internal/coordinator`, which starts or resumes it. A
paired host's coordinator with a session is held for that host like any
member; one without a session gets a `coordinator-send` to its host, which
starts it. Only a project with a repository key spans hosts, since a project
without one has a host-qualified `id`. The answer lists every recipient with
its outcome. `internal/broadcast` holds the resolution and the delivery for
both the CLI and the action.

## Starting a session on a host

`GET /api/v1/session-options?host=<host>` is that host's launch catalog: the
host it came from, the agents it can start and whether each is connected, its
launchers, its projects (`projects`: id, display name, root path, kind and
whether it was added; the most recently active first, added projects without
sessions after, archived projects left out), and `folders`: the working
folders of its recent sessions that no project holds, the most recent first,
at most 12. A client offers those folders when adding a project on that host;
the host reveals no other path.
`GET /api/v1/models?surface=<agent>&host=<host>` lists that host's models
with their reasoning efforts.
`GET /api/v1/session-options/codex-usage?host=<host>` is the limits of the
Codex account on that host, as its newest rollout recorded them:
`usage{fetchedAt, limits[{kind, percent, resetsAt}]}`, where `kind` is `5h`,
`weekly`, or `<n>m` for another window, and `fetchedAt` is when that rollout
last wrote them. With no rollout that records them, `usage` is null and
`usageProblem` says so. `session-create` with `host` starts the
session there. The web dashboard, the Mac app and the iPhone app show one
host button in the new session form once a host is paired. It opens a short
list of this machine and each paired host (hosts that are not connected are
listed with their state and cannot be chosen) and defaults to the selected
session's host. Choosing a host reloads the form from that host's catalog.
The form's project picker lists that host's projects by name and starts the
session in the chosen project's root; a typed folder is still accepted.

## Usage limits

`GET /api/v1/usage` is every account's limits on this machine and on every
connected paired host, as `sets[{host, hostId, local, provider, account, root,
primary, meters, problem}]`. Each machine lists its primary Claude account
first, then Codex while it is healthy, then its other Claude accounts. A Claude
account is named by the email it is signed in with, or by its root's name.
Each meter has a `provider`, a `kind` (`session`, `5h`, `weekly`,
`weekly_all`, `weekly_scoped` or `<n>m`), a `label`, `percent`, `asOf` and
`aged`, and when known a `severity`, `resetsAt` and `until`. A client hides a
meter once `until` passes and shows the reading's age instead of its reset when
`aged` is true. `account`, `root`, `primary` and `problem` are left out when
they do not apply.

The daemon asks each connected host with `scope=local`, so a host never asks
its own peers in turn, and waits at most a few seconds for each. A host that
runs an older Agenthail, does not answer in time or fails is one set with no
provider and a `problem` that names it. The Mac status strip and the web
dashboard's top bar show this machine's primary account and Codex in full and
every other account or host by name and its highest limit. When the Mac strip
narrows, it keeps only the other accounts near their limit. The iPhone shows
the same line under the session filters; it asks each computer it is paired
with, counts only the first one's own accounts as in use, and lists a host two
computers both report once. A computer that does not answer keeps its place,
so a later one is never taken for the first. When the line is too wide for the
phone, the other accounts move to a second line before any of them is dropped.

## Unreachable hosts and delivery

A host that stops answering keeps its rows. They are re-rendered unavailable
with the host named (`host server is unreachable`), and the host's state is one
of `connecting`, `connected`, `unreachable`, `unauthorized` or
`incompatible`.

A send to a remote session carries an idempotency key (the CLI and native
clients send one; the daemon generates one otherwise):

- If no byte reached the remote, the daemon holds the exact request as a
  pending queue row and reports `queued`. The row never expires: `queue list`,
  the dashboard and the apps show it held by `hostUnreachable` until the host
  takes it or you cancel it.
- If the request was written but no complete answer came back (no response,
  a body cut off mid-read, or an answer from something in front of the daemon
  such as Tailscale Serve without the `Agenthail-Host` header), the row is
  inflight with evidence `unknown`, and the CLI reports `delivery submitted`.
- While the host is connected the daemon replays held rows with the same key,
  as soon as a row is held and again every five seconds. Holding a row,
  retrying a dead one, or handing a moved session's queue to its new host,
  from any process, touches `changes/host-deliveries` next to the registry; the
  daemon follows that file and wakes each host's delivery loop. The touch is
  only a hint, and the five-second tick still retries anything it misses.
  Each row is claimed inflight before it is sent, so a canceled message or
  one another drain already took is never sent.
  The remote's action receipts make a replay return the first result instead
  of starting a second turn. A replay the remote still reports as in progress
  stays unknown, then becomes a dead letter marked unknown 15 minutes after
  it was first sent.
- A replay the remote answers is delivered here with the remote's receipt as
  its evidence. `queue list` shows `delivered (remote queued)` when the host
  queued it for a busy session: the message now waits in that host's queue,
  and this daemon's row is finished.

`net/http` itself replays a request that carries an `Idempotency-Key` header
once when a reused connection fails, which is safe for the same reason.

## Targets

`host::target` resolves `target` on that host. A target without a host is
resolved on this host and on every paired host. One match is used, more than
one is an error listing each `host::target`, never a pick. With the daemon
stopped, the CLI still checks mirrored rows by exact id and alias so a local
match cannot shadow a remote one silently.

Every session command reaches paired hosts through the local daemon:
`send`, `steer`, `interrupt`, `reply`, `last`, `stream`, `compact`, `model`,
`goal`, `identify`, `history`, `queue <target> "message"` (a send the host
queues while busy), `thread fork`, `thread queue`, `thread status`, `stop`,
`resume` and `logs`, and `thread create --host <host>`. A send this daemon
holds for an unreachable host is local queue data: `queue list`, `queue
clear`, `queue cancel` and `queue retry` act on it. `channel add`, `rm` and
`send` accept a remote member, `relay add` accepts remote ends and creates the
relay on its source's host, `relay rm <host>::<id>` removes one there, and
`relay list` adds each paired host's relays under `<host>::#<id>`.
`list` adds a HOST column once a host is paired and
marks an unreachable host's rows `down`.

## Where an empty host_id means local

Every place that treats `host_id = ''` as "this daemon's session":

- `Registry.ResolveTarget`, `Registry.ResolveSurfaceTarget` and
  `Registry.ExactTarget`: alias, exact id, Claude transcript, id prefix and
  name or cwd queries.
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
- `registerSessionTx` (behind `RegisterSession` and
  `RegisterSessionAndDeletePending`) refuses a session another host owns.
- `Dispatcher.RegisterReached`: a send, queued send or compact whose provider
  reports the session under an id a paired host owns ends as an unknown
  outcome naming that host, recorded only under the addressed session.
- `dashboardState` and `publishCatalogPresence` render mirrored rows from
  their stored projection; `RefreshHostQueueCounts` keeps its queue count.
- `registerRecentClaudePeers` advertises a mirrored session only while its
  host is connected, under its host-prefixed peer name.
- The CLI `list` renders rows with a host id from their projection.
