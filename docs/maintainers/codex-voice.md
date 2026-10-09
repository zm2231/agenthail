# Speak to an existing Agenthail session through Codex Voice

The iPhone companion has a conversational Codex Voice operator backed by one
persistent Codex Desktop thread on your Mac. It receives the packaged Agenthail
Operations skill before its first turn. You can discuss a plan, ask about live
agents, create a new agent, or ask it to send work and read back the result.
There is no on-device speech recognition, command grammar, separate TTS service,
or separate OpenAI API key.

Catalog, target-resolution, and journal reads release the call-state lock and
have an eight-second ceiling. At most four unfinished reads are retained; a
cancelled or changed call cannot commit a stale read result. Tool-result delivery
also releases the lock, so it does not prevent heartbeat or local hangup requests.

This feature requires matching Agenthail host and iOS builds containing the
voice module. An older installed daemon or TestFlight build does not acquire it
merely by checking out this branch. See [verification](#verification) for the
distinction between automated checks, real Codex evaluation, and phone testing.

## Setup and first call

1. Run the matching Agenthail host build. Have Codex Desktop signed in to an
   account with working native Voice access. Agenthail uses the account and
   permission configuration of that Desktop app-server, not a second login.
2. Ensure the existing Desktop bridge is available. `agenthail doctor --json`
   checks the configured surfaces. For first-time Desktop setup,
   `agenthail launch codex` launches with the loopback bridge. Do not quit or
   restart an app that owns active work without the operator's approval.
3. Follow [phone pairing](../native-apps.md#iphone-app): Tailscale on both devices,
   private HTTPS phone access, and a paired token with `read` and `control`
   scopes. A read-only pairing cannot start Voice.
4. Choose **Talk to orchestrator** for the normal Agenthail workflow, or open
   **Session details** and choose **Call this session** to prefill one exact
   existing Codex or Claude target. Grant microphone access. **Connected**
   requires both the audio peer and its data channel; accepting the start
   request alone is not a connected call.
5. Ask the orchestrator or selected session a bounded request you own and
   intend to control.
   Inspect **Agent activity** and **Open full timeline** to verify tools,
   delivery evidence, and the actual reply.

Only audio and WebRTC negotiation go to Codex's realtime service. The phone sends
control requests to the paired Mac over HTTPS. The native WebRTC peer never
receives the paired token or Codex credentials. Codex's existing authenticated
client creates the realtime call. CallKit owns audio activation; media begins
only after its activation callback. The app owns one call independently of sheets.
The entry is a compact control above the tab bar; Sessions, Inbox, and Settings
remain available while it is present.

The call button keeps a solid appearance through host refreshes. It remains
disabled while hangup is awaiting confirmation; local audio has already stopped.
Agent activity uses the same chronological message and tool rows as session
transcripts. Expanding activity pauses following the latest update so inspecting
recorded work does not move the viewport. Resizing the call controls alone does
not scroll the conversation.

## Session-bound voice targets

The current call has one audio provider, `openai-realtime-via-codex`, and can
select one existing Agenthail Codex or Claude session as its coding target. The
selected target remains the coding model and keeps its ordinary session history;
selecting or reconnecting a call never creates a worker. The host resolves the
exact registered identity again before dispatch, checks writability and capability,
then sends only explicit instructions. A busy target must support steering;
unsupported steering fails without putting the instruction in a queue.

The voice operator continuously observes the selected session's shared journal,
including work already running before the call. Claude UDS sessions can be
observed and messaged without claiming a socket receipt identifies a later
transcript turn. Updates are attributed to the selected session, not invented as
replies to a particular request. A held message is not proof of worker execution.
Audio/provider identity is not a claim that Claude supplies native audio.

The bound `session_list` tool reads the current catalog used by the CLI, not
historical registry rows. Transfer success requires a live journal subscription
and acceptance of its initial context by realtime. It does not certify that a
subsequent worker update has been spoken. Changing workers cancels the previous
worker subscription, while keeping the connected conversational intermediary.

Codex transcript continuations are resolved through its read-only thread index.
A different rollout is accepted only when its metadata names the exact thread;
subagent family IDs do not authorize a switch. The journal records the validated
path and file identity before publishing continuation events. Replacement of a
file at the same path remains an error rather than a continuation.

### Realtime context handover boundary

The installed experimental protocol supports starting a thread sideband with an
`existingCall` transport and a client-negotiated call ID. That is not a context
reset or a verified hot-switch operation. The realtime methods expose start,
appendAudio, appendText, appendSpeech, stop and listVoices, not a reset method.
Startup history settings do not remove the history of an existing voice call.
The Agenthail attempt ID is not a client-negotiated call ID.
The installed Codex rejects a prompt on an `existingCall` start, and the upstream
attachment path deliberately skips session configuration. Reattaching a sideband
therefore cannot be used as a context-reset contract.

Before implementing backing-thread replacement while preserving media, use a
disposable client-owned call: attach thread A, detach its sideband, attach B with
the same call ID, and verify media continuity, routing, removal of A's authority
and context, and return to A. A successful start receipt alone is insufficient.
The production transfer does not attempt this unverified operation.

## Conversation and work flow

```text
iPhone microphone ⇄ Codex realtime voice
                         │ explicit Agenthail tool instruction only
                         ▼
             exact existing Codex or Claude worker
                         │ continuous shared journal context
                         ▼
              conversational voice intermediary
                         │ interprets useful new updates
                         ▼
                   spoken response

iPhone call controls ── authenticated Agenthail API ── Desktop app-server
iPhone conversation ◄─ live voice events + normal recorded session timeline
```

The host resolves the entered target through Agenthail's live discovery and
ambiguity checks; no local parser turns a guessed name into a command. Speech and
typed discussion remain conversational. The backing operator uses `session_send`
only for complete user-authorized work, corrections or constraints. Progress
questions are answered from supplied context or the bounded `session_read` tool,
not automatically injected into the worker.
While bound, only the selected worker may receive instructions. Transfer or return
to the orchestrator before instructing another worker.

The persistent audio operator retains the packaged Operations skill for ordinary
voice lifecycle support, but it is not the coding provider for a selected target.
The host appends bounded, labeled transcript data to that existing realtime
session. All automatic updates, including assistant messages, history, tool
metadata and removals, use `appendText` as background context, never the
`appendSpeech` speakable channel. The intermediary decides whether and when to
summarize useful updates without interrupting the current conversation.
Updates are labeled untrusted data, not instructions to execute or JSON to
recite. A context acknowledgment is not proof of audible playback.
Stable item versions and incremental text prevent replaying a full
message after its progress was already supplied. Transfer replaces the journal
binding; return and hangup cancel it. A generation guard rejects stale context
even when the same session is selected again. Native audio stays connected.

Agenthail's Sent and Submitted receipts are not completion evidence. Voice worker
instructions are never implicitly queued. Other agents' output is data, not
permission to expand the user's request. Unsupported controls fail through the
existing transport; the voice layer does not emulate them.

## Capabilities and limits

| Surface | Behavior |
| --- | --- |
| Conversation | Native Codex realtime audio remains a conversational intermediary. Completed utterances are not automatically sent to a worker. The selected session's journal supplies continuous context. |
| Agent operations | The complete packaged `agenthail-operations` skill is embedded in the host binary and passed as literal developer instructions when creating the operator. Availability still depends on configured runtimes and their capabilities. |
| Identity | One saved operator per Agenthail registry at a time. Calls reuse it unless the tool catalog needs renewal. Idle renewal creates a thread carrying bounded recent conversation and the selected target; the old thread remains discoverable. **New conversation** deliberately starts without that continuity. Neither operation runs during an active call or unresolved creation. |
| Phone visibility | Live transcript deltas and completed utterances; expandable normal agent tool activity; full session timeline; connection, occupancy, truncation, and error states. Native recorded voice segments also appear in the normal timeline. |
| Text during a call | **Type** sends discussion to the voice intermediary. Worker instructions require the bound `session_send` tool. Message IDs prevent replay of an instruction after an uncertain response. |
| Mute | Stops sending microphone content without stopping the call or agent work. |
| Hang up / leave app | Hang up stops local microphone, playback, and peer immediately, then requests native realtime stop. The phone shows local audio as ended while it waits for the native close receipt; an unconfirmed request stays visible and can be retried. Closing the sheet, switching apps or locking the phone does not hang up. Force-quit/OS termination cannot preserve a call. It does not interrupt an agent turn. |
| Interrupt | **Interrupt orchestrator turn** only stops the operator. **Stop selected session** asks for confirmation, then checks the selected session's exact identity, writability, interrupt capability, and confirmed active turn before requesting that exact turn's interruption. It keeps audio connected and does not parse an ambiguous spoken "stop" as a worker cancellation. |
| Approvals | Existing native approval policy remains in effect. The phone can display recorded activity but does not implement approval/question replies. A blocked native approval needs attention on the Mac. |
| Multiple phones | One paired token owns an active call. Other devices see occupancy and cannot take over or read its negotiation SDP. |
| Reconnect | No automatic redial or inference replay. Hang up an uncertain call, then call again. A missing phone heartbeat requests audio stop after about 40 seconds; worker turns remain. |

The iOS call uses native WebRTC, CallKit and a voice audio session. It is not an
incoming-call service or remote desktop; it does not use PushKit. Existing Codex
and Claude sessions with an available journal may be
targets; they are not alternative voice providers. Voice availability and model
usage remain subject to the signed-in Codex account.

## Lifecycle, failures, and storage

`prepare` records creation intent, creates the Desktop-owned thread with the full
skill, saves its returned identity, and registers it for normal session discovery.
No bootstrap turn runs during preparation. Creation with an unknown outcome does
not create another operator automatically: inspect Codex and the state file before
attempting recovery.

`new` creates a distinct fresh operator through the same creation path and replaces
the saved state (identity, cursor, and recorded events reset). It is refused while a
call is active or while a prior creation outcome is unknown. The previous operator is
not deleted; it remains discoverable in the normal Sessions timeline.

`start` captures the Desktop event cursor before sending the microphone SDP offer.
The peer submits the offer after `setLocalDescription`; ICE gathering may continue
while signaling proceeds. A network that does not report `complete` gathering is
not treated as a failed call before the host receives the offer.
The native start request uses realtime `v3`, audio output, and startup context.
The authenticated Desktop backing task owns the Agenthail tool catalog; no private
handoff-mode override or separate MCP registry is supplied. The selected worker
remains the coding target, and the voice intermediary decides when to instruct it. A
matching native `started` notification binds the call;
its SDP answer permits negotiation. Only the phone's connected peer/data-channel
acknowledgment advances it to `connected`.
If local setup fails before `start` is submitted, the phone tears down only its
local audio peer. It does not send a stop request for an identity the host never
accepted, and cleanup errors do not replace the original setup failure.

```text
idle → creating → ready → starting → negotiating → connected
                              └─ unknown                │
                                    └────── stopping ◄──┘
                                               │ native closed
                                               ▼
                                             ended → new call, same operator
```

An uncertain start cannot be replayed with another attempt ID until the old call
ends. A lost renderer event window is marked truncated and cannot silently attach
an SDP from an untrusted window. Native `closed` is the end receipt; a successful
stop RPC alone is only a hangup request. An event-connection failure and a local
microphone shutdown are visible separately from confirmed host closure.
After Hang up, the call controls do not continue to imply active local audio while
the host stops. The phone reports local audio termination immediately, waits for a
matching native `closed` receipt, and makes an unconfirmed host request visible
with a retry control.
If the host ends the current attempt while the phone is still connecting, the
phone displays that failure instead of silently returning to Ready to talk.
CallKit audio deactivation pauses media without ending the call; activation
restores the native audio session. System hold preserves the user's mute choice.
The iPhone uses one native media peer and CallKit-managed audio activation, not
WebKit capture. Audio and the system call UUID are tied to the host's attempt ID.
Terminal-state polling cleans
up local audio once per call, not on every refresh. Voice details shows the call
ID and last host-close reason/event number for a completed call. These messages
identify the teardown path, not the cause of an earlier host-side `closed` event.
Capture those details before attributing an instant disconnect.

The phone keeps the last 20 attempt summaries in its private Application Support
`voice-attempts.json`. Voice details exposes the latest attempt's monotonic
milestones and first terminal cause, plus the first failed setup stage and a
sanitized error domain/code when available. Cleanup cannot replace that cause. Records
contain no speech, SDP, bearer token or session text. An unfinished record loaded
after app relaunch is classified `process_terminated_or_unknown`, not connected.
These records diagnose lifecycle behavior; physical-device audio, lock-screen
and Bluetooth acceptance still require the matrix below.

The phone stops voice-status polling after confirmed hangup and restarts it when
calling or reopening the screen. Local audio ends before host confirmation. The
jump-to-latest control retains its layout space while hidden, so scroll geometry
does not resize the footer. Button availability changes are recorded as bounded
reason codes in the attempt milestones and the `voice-call` log category.

Native starts send `heartbeatMode: "explicit"`. The owning phone renews the exact
attempt every ten seconds independently of transcript rendering; state reads
cannot renew this mode. The host rejects stale IDs and non-owning devices, and
expires an unrenewed call after forty seconds. Local media stops after thirty
seconds without a successful renewal; there is no automatic redial.

State lives beside `registry.db` at `voice/operator.json`, with a private operator
workspace at `voice/operator/`. The state file is atomically replaced with mode
0600. It retains the operator ID, skill digest, call attempt, hashed device owner,
bounded recent events, text deduplication IDs, and the latest text receipt. SDP is
never persisted. A host
restart cannot restore a physical media connection; an active saved call becomes
unknown and requires hangup. Corrupt state blocks creation rather than discarding
identity. Do not delete it as a generic retry strategy.

The tool catalog is supplied at operator creation, not realtime start. The bound
tools are `session_list`, `session_read`, `session_send`, `session_interrupt`,
`voice_transfer`, and `voice_return_to_orchestrator`. Normal configured MCPs
remain part of the backing Codex task; these Agenthail-owned dynamic tools are
not a second MCP registry or a voice-only MCP filter.

On prepare, an idle operator whose instruction digest differs is replaced with
a catalog-current thread carrying bounded recent conversation context. The old
thread remains discoverable and is never deleted. Renewal is refused during a
live call, if continuity cannot be read, or after an ambiguous creation. The
details sheet shows the digest. No call, worker or approval policy is restarted.

Recent voice events are limited to 100 records and 16 KiB per record. Normal
session activity uses the existing bounded, paginated timeline. Large or missing
records are labeled; neither channel is an unlimited transcript export. Codex owns
the size and cadence of native spoken responses; Agenthail does not duplicate or
truncate them through a second speech-submission path.

## Implementation and parent UI hook

Read these units in order:

1. `internal/surface/surfaces/codex_bridge.go`: actual Desktop renderer message
   contract. Notifications are flat `method`/`params`. App-server responses arriving
   at the renderer contain `message`; dynamic-tool responses sent to the host
   contain `response` and `threadId`, preserving the original numeric or string
   request ID. A bridge upgrade retains unresolved request identity. Duplicate
   tool requests reuse their committed result rather than repeating an action.
2. `internal/voice/service.go`: persistent identity, lease, call state, duplicate
   protection, and native request parameters. `skills/operations.go` embeds the
   distributed Operations skill.
3. `internal/surface/surfaces/codex_voice.go`: existing Desktop owner/client and
   event cursor. No managed-runtime takeover or approval-policy override.
4. `internal/daemon/voice.go` and `voice_peer.js`: control-scoped Bearer-only API
   and audio-only WebRTC program. A dashboard cookie cannot substitute for a
   valid control token.
5. `native/iOS/VoiceAPI.swift`, `VoiceAudioBridge.swift`, `VoiceCallCoordinator.swift`,
   `VoiceOperator.swift`: paired HTTPS requests, native media and CallKit,
   app-lifetime ownership, call/conversation UI and cancellation guards.

The reusable sheet is `AgenthailVoiceOperatorSheet(model:openSession:)`; its callback
opens the same normal session ID, not a voice-specific replacement session.
The standalone app entry is a separate integration unit, allowing the main
Sessions UI to put that entry in its own toolbar. The consuming iOS target must
include the microphone purpose string and background audio modes from
`native/project.yml`; regenerate the
Xcode project with XcodeGen after changing that source.

## Verification

Ordinary tests do not call live models or record a microphone:

```bash
go test ./... -race -count=1
node --test internal/daemon/voice_peer_test.cjs
xcodebuild test -project native/Agenthail.xcodeproj -scheme AgenthailIOS \
  -destination "platform=iOS Simulator,id=$(scripts/ios-simulator.sh)" \
  -parallel-testing-enabled NO \
  -only-testing:AgenthailIOSTests/VoiceTests CODE_SIGN_IDENTITY=-
```

The renderer regression executes the actual generated JavaScript in Node, including
flat notifications, target isolation, an empty event ring, and event loss. Media
tests execute the actual peer program with fixture browser interfaces. Service
tests exercise persistence, unknown outcomes, ownership, hangup versus interrupt,
lease expiry, and lost-event refusal. Native tests exercise the authenticated API,
live transcript assembly, previous-call polling during microphone permission,
unavailable audio, and late asynchronous completion after dismissal. Ad-hoc signing
is intentional: unsigned Simulator app tests cannot exercise the app's Keychain
entitlements reliably.

### Actual iOS app evaluation

The [2026-09-12 Simulator receipt](codex-voice-simulator-evidence.md) records a real
Codex conversation through the native app: existing-task discovery, one existing
worker receiving real work, executed Go tests, a verified reply, and a truthful
spoken summary. The input microphone was simulated; Codex, the authenticated API,
task delivery, and returned audio were live. This does not establish physical
iPhone microphone or Bluetooth behavior.

Use a dedicated Simulator and an isolated registry. Reuse a test-owned operator
whose audio is confirmed ended; do not copy an active production call or create
tasks as a substitute for discovery. The following opt-in host serves the ordinary
pairing/session/voice APIs, not a mock app or browser-only UI:

```bash
export AGENTHAIL_NATIVE_VOICE=1
export AGENTHAIL_NATIVE_VOICE_DIRECTORY="$(mktemp -d "$PWD/build/native-voice.XXXXXX")"
export AGENTHAIL_NATIVE_VOICE_HTTPS_ORIGIN=https://YOUR_MAC.YOUR_TAILNET.ts.net:7443
export AGENTHAIL_VOICE_SMOKE_STATE=/absolute/path/to/test-owned/voice/operator.json
go test -tags voice_live ./internal/daemon -run '^TestNativeVoiceHost$' \
  -v -count=1 -timeout=47m
```

In a second terminal, point a **dedicated** Tailscale HTTPS Serve port at the
printed loopback upstream. Do not overwrite the production Serve rule. The host
writes a private `pairing.json`; open its `pairingURL` on the test Simulator and
confirm the normal pairing UI. Pairing links expire after 15 minutes; the host
has a 45-minute evaluation window.

The old WebKit Simulator recording hook is not part of the native app. Its
historical receipt establishes the Codex/provider path, not this native peer.
Use a paired physical iPhone and a matching signed build for native acceptance:

1. Start a cold orchestrator call; record tap-to-audio time. Hang up, then call
   again on the same operator and record the warm time. Native connection logs
   contain durations, not SDP, bearer tokens or audio.
2. Ask for live session discovery, then bounded read-only work in a test-owned
   Codex session. Verify that the actual target performs it and both its interim
   update and final answer are spoken. Inspect the target's transcript and tools;
   a registered tool or a spoken claim alone is not evidence of execution.
3. Ask for a transfer to that exact session, then return to the orchestrator.
   Verify the committed route shown on the phone and the matching host target.
   Also call the target directly from its session menu without creating a worker.
   While selected, ask a conversational follow-up: the worker must receive no
   automatic input. Correct a busy capable worker and verify steer, not queue.
   Observe a turn already running before transfer and a later independent turn;
   both must reach voice. Return and reselect, checking for stale or repeated updates.
4. Close the call sheet, switch apps, and lock the phone for at least two minutes.
   Speak and listen while locked, then reopen the sheet: same call ID, same route,
   no duplicate call. Confirm the host's heartbeat remains fresh without timeline
   reads. Test system Mute and End alongside in-app controls.
5. Change Wi-Fi/cellular network, connect/disconnect headphones, and take a system
   interruption. Verify recovery or a visible terminal cause, never an automatic
   redial or replay of spoken work. Repeat hangup during preparation/negotiation.
6. Force-quit the app, lose the Mac connection, and restart only the isolated test
   host. Verify audio stops, stale state cannot claim a connected call, and the
   missing heartbeat closes host audio without stopping coding work.

The host test's successful exit only means the host closed; it is **not** the
conversation's acceptance verdict. Simulator tests cannot establish physical
background execution, Bluetooth routing, microphone capture or lock-screen audio.

After confirmed native hangup, preserve the recording and receipts, create the
isolated directory's `finished` marker, close its app connection, and remove only
the dedicated test Serve rule. Confirm the production rule is unchanged. Revoke
or discard only this isolated pairing; never reset the user's phone or host.

### Browser diagnostic fallback

The development-only real evaluation is explicitly opt-in and incurs native
Codex usage. It starts a temporary loopback browser harness; the production peer
program receives a known WAV file instead of recording the tester's microphone:

```bash
AGENTHAIL_LIVE_VOICE=1 \
AGENTHAIL_VOICE_SMOKE_CLI=/absolute/path/to/matching/agenthail \
AGENTHAIL_VOICE_SMOKE_AUDIO=/absolute/path/to/list-existing-tasks.wav \
AGENTHAIL_VOICE_SMOKE_FOLLOWUP_AUDIO=/absolute/path/to/transfer-instruct-and-return.wav \
AGENTHAIL_VOICE_SMOKE_WORKER=existing-test-owned-worker-uuid \
AGENTHAIL_VOICE_SMOKE_STATE=/absolute/path/to/test-only/operator.json \
go test -tags voice_live ./internal/daemon -run '^TestLiveCodexVoice$' \
  -v -count=1 -timeout=9m
```

The state file must identify a test-owned operator with confirmed ended audio.
The evaluation copies that state into an isolated registry and uses the daemon's
target resolver, delivery dispatcher and shared session journal. Preparation may
renew an outdated tool catalog with a continuity-carrying operator thread; the
old thread is retained. No worker is created. Open its printed
loopback URL and choose **Speak listing request**. The first recording must ask
for live existing tasks without messaging or creating any. Wait for the spoken
listing before choosing **Speak followup request**. That recording should ask to
read and transfer to the preselected existing worker, give it real bounded work,
and return to the orchestrator. For example: “Read this worker's recent activity,
transfer to it, and ask it to run the focused voice tests without editing files.
When it answers, summarize what passed and return to the orchestrator.”
Never target arbitrary existing work. After the completed reply is spoken, call
`window.finishProbe('existing-test-owned-worker-uuid')` in the test page console.
The harness captures bound list/read/send/transfer/return requests, the existing
worker's new tool activity and answer, voice events, and the final selected route.
It requires received audio energy and speech after an operator final answer, then
requests hangup. It does not use a fixed response phrase as semantic acceptance.

Review the captured JSON against the actual scenario: Did the bound tools succeed,
and was the requested target selected? Did discussion leave worker input unchanged?
Did an intentional instruction steer a busy worker? Were later independent worker
updates interpreted without repeating speech? Did the spoken summary accurately
report actual commands, results and remaining limits?
Passing capture checks alone does not answer those questions. Preserve the state
path to exercise reconnect to the same operator. The synthesized microphone must
continue sending silence after each recording, as a live microphone would; an
inactive Web Audio source can stall realtime output and invalidate the evaluation.

Browser evaluation is not proof of the native iOS UI, physical iPhone microphone,
Bluetooth routing, Tailscale, permissions, or interruption behavior. The final phone check must use
the matching installed host and iOS builds: grant permission, speak a bounded
request, inspect the target's reply, mute, hang up, reopen the same operator, and
background the app. Installing/restarting the live host remains an explicit
operator action. Simulator/sample screenshots are not live voice receipts.

## References and protocol provenance

The implementation was checked against Codex Desktop's bundled CLI/app-server
0.154.0-alpha.6.2 schema and the native realtime implementation at
[Codex commit 364b511](https://github.com/openai/codex/tree/364b511dcd60bcde3849bde4e579a5c7cad1e96a/codex-rs).
Relevant contracts are `thread/start`, `thread/realtime/start`, `appendText`,
`stop`, and their asynchronous notifications. Schema availability is not evidence
that a particular account can connect; use a real call to verify it.

[pi-gippity-control](https://github.com/IgorWarzocha/howaboua-pi-stuff/tree/7121d6f8faa7963052f1a06404137e20ef4778f1/packages/pi-gippity-control)
was inspected as a concrete reference for realtime conversation plus tool-capable
agent delegation and cancellation. Its direct call/auth transport was not copied:
Agenthail uses the app-server that already owns its Codex Desktop thread. This
feature does not substitute the separate STT/LLM/TTS architecture found in other
voice-agent frameworks.

| Reference mechanism | Agenthail adaptation |
| --- | --- |
| `conversation/session.ts` and `session-messages.ts`: deliberate delegation separate from conversation | Agenthail keeps discussion with the intermediary and submits work only through an explicit instruction tool. |
| `register.ts`: `pi.sendUserMessage`, with steer when busy | Agenthail resolves the exact target, steers a busy capable session, and refuses unsupported busy delivery without queueing. |
| `context.ts` and `controller-sessions.ts`: current session context | A selected-session journal snapshot and continuous sequenced tail keep the intermediary aware of existing and subsequent work, including Claude UDS sessions. |
| `handoff.ts`: coalesced progress and final result deduplication | Agenthail coalesces assistant updates, forwards final suffixes, and cancels obsolete bindings on transfer, return, and hangup. Transcript data is labeled untrusted. |
| LAN controller retains the host audio call across device moves | No cross-device takeover. The phone owns an app-lifetime native peer with background audio; reopening its sheet shows the existing call. A new call uses the same persistent operator only after the previous call ends. |
