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
   session list.
2. On the local host: `agenthail hosts add '<link>' [--address <tailnet-ip>]`. The
   daemon redeems the secret, sending its own host id, receives an `ahd_`
   device token, checks the protocol version and host id, and stores the
   pairing in `hosts`. The remote binds the token to the host id it was
   redeemed with (`paired_devices.host_id`), so a request on that token speaks
   for that host and no other.

Only a token with the `host` scope and a bound host id speaks for a host.
Every host-only request (a relay leg, ledger sync, a session move in, a
forwarded proxy preference, a sender named as a host's session) on any other
device token is refused with `403` and `error.code` `host_unproven`, whose
message names the fix: `Host X doesn't know this host as a paired host. On X
run 'agenthail hosts invite', then here run 'agenthail hosts add
<pairing-link>'`. A held send refused that way is dead-lettered once, with
that sentence as its attention reason and fix. Each time a daemon connects to
a paired host it reads its own token there (`GET /api/v1/device`) and, if the
token does not speak for it, puts the same text in the host's `problem` in
`agenthail hosts`, `GET /api/v1/hosts`, the snapshot's `daemon.hosts`, and
`agenthail doctor`.

Running `agenthail hosts add` with a link from a host that is already paired
pairs it again in place. The `hosts` row keeps its host id, name and catalog
position; only the endpoint, address and token change, so mirrored sessions,
aliases, relays, held sends and ledger sync carry on. The daemon restarts only
that host's mirror, delivery and ledger loops, then revokes the token it
replaced on the host. If that revoke fails, the response names the device to
remove there. `--name` must be the name the host already has.

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
  the remote no longer serves are removed. After the first one it names the
  catalog epoch and clock it holds (`retiredEpoch`, `retiredAfter`), and the
  remote leaves out retired rows written before that clock and lists only their
  ids in `retiredIds`. Rows missing from both are removed. If a retired id isn't
  held here, or a row failed to apply, the next snapshot asks for every row
  again. A lost connection marks only unretired rows unavailable.
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
- A mirrored row's checkout `id` is always qualified with the host, since a
  checkout id is derived from the path alone. The same path on two hosts is
  then two checkouts of one project; a project header names every other host
  with a checkout, and the Mac's checkout rows name their host. The iPhone qualifies the checkout of each computer's own
  rows the same way, and a proxied answer's `checkout.id` is re-qualified with
  the session references.

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
resolved there. An action names sessions on at most one other host.

A request crosses at most one host boundary. A request a paired host sent may
go back to that host, never on to a third one, so a host never acts on a host
it is not paired with through a peer that is. Each path that could pass a
paired host's request on is refused with `host_transit` before anything changes:

- actions routed to another host (`hostRoutedAction`);
- session reads, streams, metadata, attachments and uploads (`hostRouted`);
- a coordinator set or replace that would clear a third host's coordinator;
- a send to a project's lead, and a channel post's delivery to a lead, on a
  third host;
- a session move to a third host;
- a ledger owner assignment to a third host.

`resolve` with `scope=remote` is refused for a paired host, and `usage` answers
a paired host with this host's own sets only. Mirrors are never re-exported, so
a host never shows its peers another host's sessions to address. Queue and
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

A project channel has no stored members, and each host decides its own.
`channel-send` to `#<project>` delivers to this host's members, read from its
catalog: every local row whose project `id` is the project's, that is not
stale or archived and is working or was active within `surface.ActiveWindow`,
the window peer advertising uses, plus the project's coordinators on this
host, which `internal/coordinator` starts or resumes. `internal/broadcast`
holds that resolution and delivery; only the daemon runs it, and the CLI posts
the action.

Every paired host that holds the project (`CatalogProjectHosts`, stale rows
included, plus the hosts of the project's coordinators) gets one
`channel-deliver` action at the same time, keyed `<action key>:<host id>`.
That host resolves and delivers to its own members the same way, by its own
busy rules, and answers with its recipients. It accepts `channel-deliver` only
from a paired host and passes it on to no one. A sender that is a session must
come with `sourceProjectId`: the host refuses a missing one
(`project_unconfirmed`) or another project's (`different_project`) with that
`rule` in the error's `detail`, the reach a direct send across hosts has. The
person, the CLI and Agenthail reach any project. A `channel-send` routed to a
paired host with `host` carries the sender's `sourceProjectId` the same way,
and that host applies the same rule to a project send.

A leg follows the rules of a held send. A host that never got the request
(`NotSentError`) is held pending; one that may have got it without answering is
held inflight. Both go in `host_held_actions` (schema 81) with their key and body, and the
host's delivery drain replays them after its held sends, so the host's action
receipts make a replay of an applied leg return its stored answer. A refusal,
or no confirmation within `hostDeliveryPatience`, ends the row and adds a
history entry. The answer joins every leg: `recipients` with paired hosts'
recipients qualified, `hosts` with each host's `state` (`reached`, `held`,
`unknown`, `refused`), and `unreachable`. A refused leg makes it a 409, like a
failed member. A host on a build without `channel-deliver` refuses it
(`sessionId is required`) rather than delivering, so nothing reaches it twice.
Only a project with a repository key spans hosts, since a project without one
has a host-qualified `id`.

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
connected paired host, as `sets[{host, hostId, local, provider, name, account,
login, root, primary, meters, asOf, problem, problemDetail}]`. Each machine lists its primary Claude
account first, then Codex while it is healthy, then its other Claude accounts.
A Claude set's `name` is its root's name, `account` the email the root is
signed in with, and `login` how the root signs in: `claude.ai`, `api_key` or
`signed_out`. `asOf` is when its usage was read. Each meter has a `provider`, a
`kind` (`session`, `5h`, `weekly`, `weekly_all`, `weekly_scoped` or `<n>m`), a
`label`, `percent`, `asOf` and `aged`, and when known a `severity`, `resetsAt`,
`until` and, for `weekly_scoped`, the `scope` model's name. A client hides a
meter once `until` passes and shows the reading's age instead of its reset when
`aged` is true. Fields that do not apply are left out.

A Claude root's usage is claude.ai's answer for the root's login, read with the
browser sign-in profile signed into that login. The daemon reads it at most
once a minute per root and shares the read across every client and poll; a
login change starts a fresh read. A read that fails keeps the last reading for
that login, with its `asOf` and the `problem`. A root that uses an API key or holds
no login is a set with no meters, a short `problem` that says which, and a
`problemDetail` that explains it; it never asks claude.ai. The daemon learns an
API key only from its presence in the environment, the root's `settings.json`
or `.claude.json`, never its value.

The daemon asks each connected host with `scope=local`, so a host never asks
its own peers in turn, and waits at most a few seconds for each. A host that
runs an older Agenthail, does not answer in time or fails is one set with no
provider and a `problem` that names it.

Every client shows every set. The Mac status strip, the web dashboard's top
bar and the iPhone's line under the session filters show one chip per account:
its login's name (the email before the `@`, `Codex`, or the host and how the
root signs in, as in `build-box (API key)`), the host's name in front only when
the same name is on more than one host, the root's name after it when one host
would show two accounts alike, small
bars for its limits, and its highest limit. An account with only a problem
shows a mark instead of a number; one whose readings have all passed shows a
dash. A root that uses an API key or is signed out is a normal state, shown in
secondary text; a signed-out root with sessions working or open on it shows as a
warning. Only a limit near its end, or a failed read that left no reading,
shows as an error. Opening the line lists every account by host with each limit, its reset,
when it was read, its problem with its detail as help text, and the sessions working or open on it, which
open on click. A session belongs to the account of its host and, for Claude,
its `claudeConfigRoot`. When the Mac strip narrows it drops the bars first. The
dashboard drops the bars first, then accounts that are neither near a limit nor
showing a warning or error, last one first, and counts what it dropped; narrower than
760px it shows no usage. The iPhone drops the bars, then accounts from the end,
counting them. The iPhone asks each computer it is paired with and lists a host
two computers both report once. `agenthail usage [--json]` prints the same
sets with their sessions. Accounts behind an account proxy are described in
[account proxies](proxies.md).

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

## Completion notifications

A paired host's session notifies here when its turn finishes, fails or ends
asking you something, the same as a local one. The host decides: when its own
observer raises a completion notification, it records the turn, the outcome
(`finished`, `failed` or `needs_you`) and, for an ask, the request id in
`session_runtime` (`notice_*`), and publishes them on the session's catalog
row as `turnCompletion`. A viewing daemon never judges a mirrored session's
turn finished from its status.

A viewing daemon notifies from a `session.upserted` event on the host's catalog
stream whose `turnCompletion` names a turn other than the one its stored row
had. Rows it reads from a snapshot only record where the host stands. The
mirror takes a snapshot on every connect, after a stream gap and when the
daemon starts, and follows the stream from the snapshot's position, so a turn
that finished while the host was unreachable, or while this daemon was down,
never notifies here, and a first-seen row never notifies.

The notification names the app, the host and the status: "Codex agent
finished on server" on this computer's desktop and paired phones. An ask
carries the host's request id: the desktop notification gets the ask's
buttons from the mirrored `endTurn`, the phone fetches the request from this
daemon, and an answer here is routed to the host like any other action.

Every computer paired to the same phone may send the notification: the host
for its own session, and each daemon viewing it. Each send carries the same
`dedupeKey`, the SHA-256 of the host id, the host's session id and the turn
id, and the push relay delivers a key to a phone once (see
[push relay](push-relay.md)). Whichever arrives first is shown, so a host
mirrored by another daemon also names itself in its own completion pushes.
Each viewing Mac shows its own desktop notification.

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
clear` and `queue cancel` act on it, and a send it could not relay asks on
this host. `channel add`, `rm` and
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
  Triggers on `message_queue` and `sessions` mark a mirrored session in
  `host_queue_dirty` in the writer's own transaction, whichever process
  writes. The refresh, run after each presence pass and host delivery, reads
  only marked sessions, rewrites rows whose count differs, and clears the
  marks; with no marks it returns without taking the write lock.
- `registerRecentClaudePeers` advertises a mirrored session only while its
  host is connected, under its host-prefixed peer name.
- The CLI `list` renders rows with a host id from their projection.
