# Claude human turns and authenticated profiles

Status: planned / WIP. This branch implements and tests the isolated human-turn
primitive. Multiple profiles and delivery from voice or desktop are planned.
This document describes the remaining contract, not available product controls.

## Implemented primitive

[`Claude.List`](../../internal/surface/surfaces/claude.go) retains the provider's
`bridgeSessionId` in `surface.Session.BridgeSessionID`. The field is excluded
from JSON and is not persisted by the registry.

`Claude.SendHumanTurn` selects that bridge identity, checks transcript readiness,
and uses the existing authenticated web request to submit a `type: user` event
whose message has `role: user`. A bridge-only session may use its session ID.
A native UDS session without a bridge identity returns `DeliveryUnavailable`.
Busy sessions return an unaccepted result without posting. Ordinary `Claude.Send`
continues to select native peer delivery for UDS sessions.

The primitive does not authenticate the message's human origin. Its caller must
establish that authority before invocation. There are no voice, desktop, queue,
CLI or HTTP producers calling this method in this branch. Transport acceptance
does not prove that Claude completed or accepted the instruction semantically.

## Planned account model

The working assumption is multiple separately signed-in Claude accounts selected
through named Chrome profiles. A profile is an authentication source, not proof
of a subscription tier. Organization selection within one account requires a
separate authenticated contract; it is not inferred from a profile name.

Use a stable internal profile ID with a user-facing label and a validated Chrome
profile selector. Persist selectors and bindings, never browser cookies. Read
cookies through the existing bridge when needed. Configuration must have one
canonical owner shared by CLI and daemon startup, including launchd; replace the
process-global profile selection rather than changing it around each request.

The current startup selects one `AGENTHAIL_CHROME_PROFILE`
([main](../../cmd/agenthail/main.go), [launchd](../../internal/cli/daemon.go)).
[`transport.go`](../../internal/surface/surfaces/transport.go) uses a global
`chromeProfile` for cookie reads, invalidation and sidecar request metadata.
Although `Claude` stores a constructor profile, the request path does not read
that value. Cache keys already include a profile selector.

Each authenticated request must resolve one immutable profile identity before
loading cookies. Cache and invalidation must use that same identity throughout
the request. Removing or editing a profile must not redirect existing work to a
different account. A Chrome profile can be signed into another account later:
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

The current draft contains the primitive and this plan. Every step above remains
unchecked implementation work. UI, producer wiring, profile configuration,
schema changes and live authenticated requests are deferred in this draft.

## Acceptance gates

- Concurrent A/B requests use only their selected cookies; failures and cache
  invalidation affect only the selected profile. Profile replacement and account
  sign-in drift cannot change the identity of queued or in-flight work.
- Native-only sessions cannot emit web human turns. Agent-originated traffic
  remains native. Forged human provenance is rejected at the producer boundary.
- Session binding and queue replay survive restart, ID merge and alias changes.
  Unbound or mismatched targets fail before POST. Unknown outcomes never resend
  automatically; rebinding or removing a profile leaves pending work explicit.
- Each promised field follows config/storage -> load -> runtime -> API/CLI ->
  web/Mac/iOS consumers, including reduced catalog shapes and delivery errors.
- Live acceptance observes the exact user event, correlated reply and completion
  on each owning account. Cookies, tokens and full auth responses are absent from
  public output and logs. Fixture tests alone do not establish this live gate.

Automatic subscription selection, usage-based account rotation, credential
copying, and native Claude CLI login switching are outside this proposal.
