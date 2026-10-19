# Claude human turns and authenticated profiles

Status: desktop composer delivery and browser sign-in profiles have shipped.
Per-session profile bindings, account ownership, persisted provenance snapshots
and voice delivery are later layers. Their plans follow below.

## Shipped: desktop composer human turns

`Claude.SendHumanTurn` submits a `type: user` event whose message has
`role: user` through the existing authenticated web request. It requires a
Remote Control attachment: `Session.Bridge`, the `bridgeSessionId` of the live
process record whose `sessionId` is the session ID
([`Claude.List`](../../internal/surface/surfaces/claude.go)). It is cleared when
that process moves to another conversation. `CanSendHumanTurn`
reports whether that identity exists, matching the controls that already use it
(`EffectiveCapabilities`). Busy sessions return an unaccepted result without
posting. `Claude.Send` stays native-peer-first for UDS sessions.

Authorship is declared by the composer, not inferred. The web dashboard
(`/api/action`) and the Mac and iPhone apps (`/api/v1/actions`, including the
Mac notification Reply) post composer messages as action `send` with
`"author": "person"` to
[`dashboardActionHandler`](../../internal/daemon/dashboard.go). The handler
rejects any other author value, and rejects `author: person` combined with
`sourceSessionId`. It carries the value as `SendOptions.Author`
(`surface.AuthorPerson`). A send that does not declare an author is
unattributed, whatever its caller: scripts and other `/api/v1/actions`
clients, channel broadcasts, the CLI, relays and voice. An unattributed message
to a session with a native socket uses that socket. A bridge-only session (one
with no socket) receives every send through Remote Control, as it already did,
whatever the author.

[`delivery.RouteFor`](../../internal/delivery/route.go) is the one place that
picks a message's transport. A person's message to an adapter that implements
`surface.HumanTurnSender` and has a Remote Control attachment
(`CanSendHumanTurn`) takes `RouteHumanTurn`. A Claude UDS session otherwise
takes `RoutePeer`. A session the Claude host runs takes `RouteHumanTurn` for a
person's message without Remote Control ([Claude session host](claude-host.md)).
Everything else takes `RouteSend`. `Dispatcher.deliver` and
[`drainClaimedMessage`](../../internal/daemon/outbox.go) both call it, and so do
the CLI and voice checks that reject reply correlation on a peer route. The
route is derived each time a message is sent and never stored, so a queued
message follows the target's bridge state at drain time. The choice is made
before sending and is never revised after a failure.

Options never change authorship. On `RouteHumanTurn`, a per-message model or
turn option is rejected as an invalid request: the bridge user event has no
per-message model or effort field. The native route already rejects these
options for Claude, which has no per-message option sender.

A human turn records `delivered` evidence and arms completion tracking. A native
send records `transport_accepted`. The path is visible in the API receipt's
`evidence` and in history. The dashboard and app composers show both as "Sent",
and this layer adds no UI for the difference.

A busy target follows the existing busy-delivery policy. A steer from a
composer, whether chosen directly or by the `steer` policy, goes through
`Claude.Steer`, which posts through the Remote Control attachment, so it is also
the person's turn. With `queue`, the author persists as `message_queue.author`,
and the drain replays a person's message through `SendHumanTurn` once the
session is idle. `Claude.Send`'s bridge branch and `SendHumanTurn` share
`postWhenIdle`, so both apply the same busy check. Outcome semantics are
unchanged. An uncertain POST is `unknown` and becomes a submitted intent or a
dead letter, and it is never retried.

The primitive does not authenticate the person behind a composer. The trust
boundary is the dashboard's control authority (the dashboard token or a paired
device with `control` scope), where a client declares authorship. Tests cover
the routing through both HTTP routes with the real Claude adapter and a fixture
web request: person to bridge, unmarked and agent sends to native, no bridge to
native, contradictory or unknown authors rejected, person with options
rejected, a busy queue replayed as a human turn, and uncertain POSTs never
retried. The native helper tests check that the Mac composer and notification
Reply declare the author and that an unmarked API send does not. A live
authenticated POST through the composer path has not been run for this layer.

## Shipped: browser sign-in profiles

A sign-in profile is `{id, label, browser, profile selector}` in the registry
([`signin_profiles`](../../internal/registry/registry.go)), the one owner shared
by the CLI and the daemon under launchd. Each consumer (`notion`, and `claude`
for Remote Control) is automatic, pinned to one profile through
`signin_bindings`, or off through `signin_off`. Automatic means the system
default browser (the LaunchServices https handler on macOS, Safari when none is
set; `xdg-settings get default-web-browser` on Linux) and the profile that
browser last used ([`default.go`](../../internal/signin/default.go)). Choices
and selectors are stored; cookies are not. Removing a pinned profile returns
its consumers to automatic. There is no process-wide profile and no
environment override.

Each request resolves one `signin.Source` through `signin.Resolver` with a
`signin.Target{Consumer, SessionID}` before anything is sent
([`signin.go`](../../internal/signin/signin.go),
[`registry/signin.go`](../../internal/registry/signin.go)). The cookie cache,
the per-key read lock and invalidation are keyed on that source
([`transport.go`](../../internal/surface/surfaces/transport.go)), and
[`cookie.mjs`](../../sidecar/cookie.mjs) reads only the browser and profile
directory it is given. Resolution opens the profile's cookie store and closes
it unread, so a missing store and a permission error (EPERM on a protected
folder, which on macOS needs Full Disk Access for the daemon binary) are
reported with their fix as `signin.ProblemError`, and sends fail as terminal
`signin_unreadable` before anything is posted. A consumer that is off returns
`signin.OffError`; sends fail as terminal `signin_off`, and doctor and health
show "sign-in off" rather than a failure. Notion keeps the user and space it
detected for one profile in an immutable account value, so a binding change
never mixes two accounts within one operation.

`GET /api/v1/signin` lists profiles, each consumer's mode with the browser
profile it resolves to and any problem, and the browsers found on this
computer. The dashboard snapshot carries the same consumer status for clients
without the settings scope. Each browser probe reads only profile metadata, is bounded by a
timeout, and refuses special files. Profiles are added through
`POST /api/v1/signin/profiles`, removed through `DELETE ?id=`, and chosen
through `PUT /api/v1/signin/bindings` with a profile ID or label, `auto` or
`none`. All of these require the settings
scope. The CLI (`agenthail signin`), the web dashboard and the Mac app use
these endpoints.

`SessionID` is carried but not yet used. Per-session Claude profile bindings
attach behind the same resolver without changing callers.

## Planned account model

The working assumption is multiple separately signed-in Claude accounts selected
through named browser sign-in profiles. A profile is an authentication source, not proof
of a subscription tier. Organization selection within one account requires a
separate authenticated contract; it is not inferred from a profile name.

Sign-in profiles, their canonical registry owner and per-request resolution
have shipped (above). What remains is binding a Claude session to a profile
through `Target.SessionID`. A browser profile can be signed into another account later:
pin an authenticated account identity and reject identity drift before posting.
The account identity response and a read-only session ownership check still need
to be verified against the existing authenticated bridge.

Session discovery reads local Claude records. A bridge ID by itself does not
establish which browser account owns it. Require an explicit binding plus a
verified read-only ownership check; never discover ownership by posting test
messages or retrying across accounts. Health must probe web authentication per
profile independently: `Claude.Health` currently returns early for a live UDS
session and therefore cannot establish browser authentication in that case.

## Planned human delivery contract

The trusted desktop/voice boundary records the human's instruction and authority
to deliver it. Server-owned provenance must distinguish this from agent-authored
work. A request-body role flag, an operator source ID, or an agent's claim of
human origin is insufficient. Voice delegation must preserve the confirmed user
instruction or record explicit user approval of any transformed instruction.

Persist the delivery kind, selected profile ID, authenticated account identity,
target bridge identity and binding revision with the intent before sending.
Queue replay uses those values after restart; a later session rebind cannot
redirect pending work. The existing [registry](../../internal/registry/registry.go)
stores sessions and queued turn options but does not preserve these bindings.
Legacy queued work must retain its original delivery semantics.

Authentication failure, access denial, missing bridge and identity mismatch need
distinct actionable errors. Never switch profiles or native/web transport on a
failure. An uncertain POST outcome remains uncertain and requires inspection
before retry; it must not become another human turn through automatic replay.

Voice consumers must prepare the shared journal subscription before dispatch,
retain the returned user-event UUID as the authoritative turn identity and filter
replies by it. Verify this against local provider events before enabling speech.
See [shared stream consumers](shared-stream-consumers.md).

## Delivery sequence and surfaces

1. Add explicit profile context to the authenticated transport, cache and health
   probes. Verify concurrent isolated profiles with fake cookie bridges and
   sidecars before changing producers.
2. Add canonical profile configuration and authenticated account identity checks,
   session binding, removal/rebind rules, and durable intent/queue fields.
3. Add the trusted human delivery entry point and replay path. Test native peer
   routing alongside it and retain delivery receipts in the existing journal.
4. Expose account health and session binding through authenticated settings and
   actions, CLI configuration/help, web dashboard, Mac and iOS models/views.
   Show the bound account at send/call time and surface reauthentication errors.
   Settings mutation requires settings authority; session delivery requires
   control authority. Neither API returns credential material.
5. Wire confirmed voice and desktop instructions, update operator guidance and
   package/config examples, then perform authorized live acceptance with two
   accounts and two sessions.

The shipped layers cover the trusted desktop entry point and queue replay, step 1,
and the profile configuration and settings surfaces from steps 2 and 4. Still to
do: account identity checks, per-session bindings and durable intent fields from
step 2, account health and session binding in step 4, the voice part of step 5,
and the two-account live acceptance.

## Acceptance gates

- Concurrent A/B requests use only their selected cookies; failures and cache
  invalidation affect only the selected profile. Profile replacement and account
  sign-in drift cannot change the identity of queued or in-flight work.
- Native-only sessions cannot emit web human turns. Agent-originated traffic
  remains native. Forged human provenance is rejected at the producer boundary.
- Session binding and queue replay survive restart, a Remote Control change and alias changes.
  Unbound or mismatched targets fail before POST. Unknown outcomes never resend
  automatically; rebinding or removing a profile leaves pending work explicit.
- Each promised field follows config/storage -> load -> runtime -> API/CLI ->
  web/Mac/iOS consumers, including reduced catalog shapes and delivery errors.
- Live acceptance observes the exact user event, correlated reply and completion
  on each owning account. Cookies, tokens and full auth responses are absent from
  public output and logs. Fixture tests alone do not establish this live gate.

Automatic subscription selection, usage-based account rotation, credential
copying, and native Claude CLI login switching are outside this proposal.
