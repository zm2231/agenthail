# Speak to an existing Agenthail session through Codex Voice

The iPhone companion has a conversational Codex Voice operator backed by one
persistent Codex thread on Codex's app-server on the host. Codex Desktop does not
need to be running. It receives the packaged Agenthail
Operations skill before its first turn. You can discuss a plan, ask about live
agents, create a new agent, or ask it to send work and read back the result.
There is no on-device speech recognition, command grammar, separate TTS service,
or separate OpenAI API key.

This feature requires matching Agenthail host and iOS builds containing the
voice module. An older installed daemon or TestFlight build does not acquire it
merely by checking out this branch. See [verification](#verification) for the
distinction between automated checks, real Codex evaluation, and phone testing.

## Setup and first call

1. Run the matching Agenthail host build. Sign Codex in on the host with
   `codex login` (ChatGPT), or configure Codex with an OpenAI API key that has
   Realtime API access. Agenthail uses the login of Codex's app-server, not a
   second one. With a ChatGPT login, voice goes to ChatGPT's realtime service;
   with an API key, to the OpenAI Realtime API with that key.
2. Agenthail starts Codex's app-server daemon when a call needs it.
   `agenthail doctor --json` checks the configured surfaces. An operator that
   Codex Desktop currently has open keeps running in Desktop, through its
   loopback bridge, until Desktop closes it.
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
control requests to the paired computer over HTTPS. Codex's existing authenticated
client creates the realtime call. On iPhone the media peer is native WebRTC
running under CallKit; it never receives the paired token or Codex credentials.
The Mac and the web dashboard run the same peer program in WebKit or the
browser; the Mac's audio page contains no token, and its native container
supplies the authenticated page request.
The app owns one call for its lifetime, independent of the Voice screen.
The entry is a compact control above the tab bar; Sessions, Inbox, and Settings
remain available while it is present.

## Session-bound voice targets

The current call has one audio provider, `openai-realtime-via-codex`, and can
select one existing Agenthail Codex or Claude session as its coding target. The
selected target remains the coding model and keeps its ordinary session history;
selecting or reconnecting a call never creates a worker. The host resolves the
exact registered identity again before dispatch, checks writability and capability,
then records the delivery evidence and target turn ID before accepting speakable
updates.

Only a target with a correlatable turn stream can receive realtime delegation.
Claude's native UDS peer transport is deliberately blocked for this path because
socket acceptance alone cannot be joined to a later transcript turn. Busy, held,
read-only, unavailable, and unknown outcomes remain visible in the call state and
are not represented as a spoken reply. Audio/provider identity is therefore not
a claim that Claude supplies native audio.

Codex's own realtime prompt has the realtime model hand every request to the
operator thread. A call that starts with a target selected replaces that prompt
with its own and omits the operator's startup context: the host delivers each
spoken request to the target, and the realtime model only speaks the target's
updates, so one request is never worked by both the operator and the target. A
call that starts with the orchestrator keeps Codex's prompt, with the
orchestrator role added as a developer item. A prompt is fixed for the life of a
realtime session, so a call transferred to a session after it started keeps the
prompt it started with; the transfer tells the model in a developer message not
to do the work.

A session-bound call also gets a brief of its session as its first item, and a
transfer appends the same brief to its developer message. The host builds it
without a model call, from the session's own records: its status, goal,
compaction count and newest exchanges from the session journal, newest first,
within 6 KB. Every field has its own limit, and each part it shortens, leaves
out or could not read is named in the brief, and the brief tells the model that
anything not in it is unknown. When the names of those parts outgrow their 1 KB
line, the rest are counted. The call state's `brief` records what the call was
given: the session, when it was read, the journal position, the size, the
number of exchanges and every omitted part. A call waits at most three seconds
for the reads behind its brief; a part not read by then is named as missing,
and a brief that could not be read still lets the call start.

## Conversation and work flow

```text
iPhone microphone ⇄ Codex realtime voice
                         │ completed user transcript
                         ▼
             exact existing Codex or Claude target
                         │ delivery evidence + target turn ID
                         ▼
             correlated target stream updates
                         │ appendText to existing audio plane
                         ▼
                   spoken response

iPhone call controls ── authenticated Agenthail API ── Codex app-server
iPhone conversation ◄─ live voice events + normal recorded session timeline
```

The host resolves the entered target through Agenthail's live discovery and
ambiguity checks; no local parser turns a guessed name into a command. On each
completed user transcript, it delivers the exact text to that selected target.
It speaks a reply to that request only when it arrives from the receipt's exact
target turn, so a newer unrelated reply cannot pass for it.

A call follows its selected session's journal from when voice moves to the
session until it moves away, the call ends or the host closes, with one
subscription per call. Each turn in the journal is placed before anything
about it is spoken:

- A turn a request from the call started streams its reply to voice as it
  comes, as above.
- A turn the person typed in the session itself, from any device, gives voice
  a note with the first 300 bytes of what they typed, then, once the turn ends,
  the opening sentences of its reply, within 300 bytes, and "The full reply is
  in the session". That reply waits for a pause: the person is not mid-utterance
  and voice has not spoken for two seconds. A transcript delta whose done never
  arrives holds it at most four seconds. Up to eight replies wait; past that the
  oldest is dropped and recorded.
- A request sent to a Claude peer over its socket is known by its frame id
  until the journal shows the prompt: as `origin.msg_id` on the turn it
  started, or as `source_uuid` when the peer absorbed it into a turn already
  running. That turn becomes the request's turn and its reply streams to voice.
  A typed turn that absorbs it becomes a voice turn, and two requests on one
  turn are both answered by its reply. A request kept from an earlier call by
  its frame id is found again the same way.
- A request sent on a route that gives no turn stays silent, as voice said,
  wherever it lands, including a session the delivery moved to. Its turn is
  known by the id the delivery gave the prompt, never by the prompt's text: the
  receipt's turn ID, or its `messageId`, which a Claude prompt keeps in the
  journal as its record uuid, its peer `msg_id` or, when absorbed mid-turn, its
  queued command's `source_uuid`. A held request whose delivery returned no id
  cannot be placed, and its turn is taken as typed.
- While a request's turn is not yet known, a turn the host cannot place waits
  for that request to settle, at most 30 seconds. Past that it is taken as typed,
  recorded in the call as `voice/target/turn-unattributed` and written to the
  daemon log.
- A journal source error ends the follower, as it ends any journal consumer.
  Voice then follows the session again with a new subscription, recorded as
  `voice/target/refollowed`. The new follower takes over the requests whose
  turns were open: it reads their outcomes again, speaks a reply that ended
  meanwhile, and follows the rest, so a recovered source still speaks them.
  Replies waiting for a pause are kept. A journal that ends without a source
  error ends the follower's requests as failed in the call; they stay kept for
  the next call.

Everything said about the session runs under its follower, so moving voice to
another session or back to the orchestrator cancels an update or reply still
being handed to the call.

A request whose turn voice follows is kept, in the voice state file, until
voice has its outcome. One whose turn ends during the call is spoken then and
forgotten. One still open when the call ends, or whose journal ended before its
turn did, is reported once in the brief of the next call to the same
session, read from the session's journal: its reply's opening sentences, how it
failed, or that it is still working. A request still working is followed by the
new call. Once the call connects, its follower reads those turns again after it
subscribes, so a turn that ended between the brief and the subscription is
spoken then, as the request's own; later replies are spoken as they come. A
request is never sent again. Moving voice to another session or to the
orchestrator, or calling one of them, forgets the requests kept for the previous
session. Up to 16 are kept, and dropping the oldest past that is recorded as a
`voice/request/dropped` event. A brief reports the oldest 6 and counts the rest,
which stay kept for a later call; a request whose outcome could not be read is
named as such and kept.

After each turn the host reads the session's brief again. When the session's
compaction count rose since the brief voice holds, voice is told the session
compacted its context and given the fresh brief, which replaces the call state's
`brief`.

The persistent audio operator retains the packaged Operations skill for ordinary
voice lifecycle support, but it is not the coding provider for a selected target.
The host appends correlated target stream text to that existing realtime session;
it remains Codex's native voice, not a separate TTS service.

Agenthail's delivery semantics still apply. Accepted, queued, delivered, completed,
failed, and unknown are different outcomes. Other agents' output is data, not
permission to expand the user's request. Unsupported controls fail through the
existing transport; the voice layer does not emulate them.

## Capabilities and limits

| Surface | Behavior |
| --- | --- |
| Conversation | Native Codex realtime audio transcribes speech. Each completed user transcript is delivered to the selected existing session, except filler: an utterance of at most four words made only of the filler um, umm, uh, uhm, er, hmm, mm, mhm, thanks, thank you, cool, nice or got it stays with voice, is recorded in the call as `voice/utterance/voice-only` with its text and target, and is written to the daemon log. Words that can answer a question, such as yes, no, okay or sure, are never on the list, so they always reach the session; and only its correlated stream is appended back to audio. No dictation-only replacement. |
| Agent operations | The complete packaged `agenthail-operations` skill is embedded in the host binary and passed as literal developer instructions when creating the operator. Availability still depends on configured runtimes and their capabilities. |
| Identity | One saved operator per Agenthail registry at a time. Calling again reuses its thread, work, and history; it creates a new audio connection, not a new operator. The explicit **New conversation** action deliberately creates a distinct new operator thread and makes it the saved operator; the previous operator stays discoverable in the normal Sessions timeline. It is refused while a call is active or while a creation outcome is still unknown. |
| Phone visibility | Live transcript deltas and completed utterances; expandable normal agent tool activity; full session timeline; connection, occupancy, truncation, and error states. Native recorded voice segments also appear in the normal timeline. |
| Text during a call | **Type** sends the request to the selected session through the same correlation path. Message IDs prevent automatic replay after an uncertain response. Typing in the session itself during a call is noted to voice, and its reply is spoken at the next pause. |
| Mute | Stops sending microphone content without stopping the call or agent work. The phone reports mute and system hold to the host in order. While input is muted, the host refuses `voice_transfer`, `voice_return_to_orchestrator` and `voice_send_to_lead` and does not deliver spoken requests; read-only tools still answer. Each of those actions checks the mute again just before it sends anything, so a mute that arrives while one is being prepared stops it; one already sending is not recalled. If the host cannot record a mute, the phone ends the call. |
| Hang up / leave app | Hang up, in the app or with the system call controls, stops local microphone, playback, and peer immediately, then requests native realtime stop. The phone shows local audio as ended while it waits for the native close receipt; an unconfirmed request stays visible and can be retried. It does not interrupt an agent turn. Closing the Voice screen, switching apps or locking the phone does not hang up; **Return to call** reopens it. Force-quitting the app or OS termination ends the call. |
| Interrupt | **Interrupt orchestrator turn** only stops the operator. **Stop selected session** asks for confirmation, then checks the selected session's exact identity, writability, interrupt capability, and confirmed active turn before requesting that exact turn's interruption. It keeps audio connected and does not parse an ambiguous spoken "stop" as a worker cancellation. |
| Approvals | Existing native approval policy remains in effect. The phone can display recorded activity but does not implement approval/question replies. A blocked native approval needs attention on the Mac. |
| Multiple phones | One paired token owns an active call. Other devices see occupancy and cannot take over or read its negotiation SDP. |
| Project coordinators | The operator's `voice_projects` tool lists every project this host knows, from sessions on this host and paired hosts, added projects, and coordinators, with each project's coordinators by host. `voice_send_to_lead` sends to a project's coordinator by project name or id, on this host or a paired host, and starts the coordinator's conversation when it has none. `voice_transfer` takes `project` instead of `targetId` to move the call to a coordinator on this host that already has a conversation. A project is found by the same rules as `@lead` and `@<project>-lead`: a name several projects share, a project with no coordinator, and a project with coordinators on two hosts are refused with the reason, never picked. |
| Reconnect | No automatic redial or inference replay. Hang up an uncertain call, then call again. A missing phone heartbeat requests audio stop after about 40 seconds; worker turns remain. |

On iPhone the call is an outgoing CallKit call with the `audio` and `voip`
background modes. It is not an incoming-call service and does not use PushKit,
and it is not a remote desktop. Existing Codex and correlated-transcript Claude sessions may be
targets; they are not alternative voice providers. Voice availability and model
usage remain subject to the signed-in Codex account.

## Lifecycle, failures, and storage

`prepare` records creation intent, creates the operator thread on Codex's
app-server with the full skill and Agenthail's voice tools, names it, saves its
returned identity, and registers it for normal session discovery under the
`@voice` alias. A saved operator is registered with the route and runtime Codex
reports for it now, not the ones saved with it. `@voice` moves to each new
operator; it is never taken from a session someone else named.
No bootstrap turn runs during preparation. Creation with an unknown outcome does
not create another operator automatically: inspect Codex and the state file before
attempting recovery.

`new` creates a distinct fresh operator through the same creation path and replaces
the saved state (identity, cursor, and recorded events reset). It is refused while a
call is active or while a prior creation outcome is unknown. The previous operator is
not deleted; it remains discoverable in the normal Sessions timeline.

`start` opens the call's own connection to Codex's app-server, resumes the
operator on it, and only then sends the microphone SDP offer on that connection.
The call's events, its tool calls and their answers all travel on it; another
call never reads them. An operator Codex Desktop has open uses Desktop's event
cursor instead. If Codex's login cannot do realtime, the call ends with the
reason and what fixes it (sign in with `codex login`, or a key with Realtime API
access); a start Codex definitely refused ends the call instead of leaving it
unknown. Each call starts with no events from the previous one, and events from
another realtime session are ignored. The host waits on the call's events rather
than checking for them on a timer, so a tool call reaches its handler as soon as
Codex sends it.
The peer submits the offer after `setLocalDescription`; ICE gathering may continue
while signaling proceeds. A network that does not report `complete` gathering is
not treated as a failed call before the host receives the offer.
The native start request uses realtime `v3` and audio output; an orchestrator call includes startup context and a session-bound call does not.
It does not enable the legacy Codex response-handoff route: the realtime session
is the audio plane, while the selected session remains the only coding target. A
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
ends. A dropped call connection or a lost event window is marked truncated and cannot silently attach
an SDP from an untrusted window. Native `closed` is the end receipt; a successful
stop RPC alone is only a hangup request. An event-connection failure and a local
microphone shutdown are visible separately from confirmed host closure.
After Hang up, the call controls do not continue to imply active local audio while
the host stops. The phone reports local audio termination immediately, waits for a
matching native `closed` receipt, and makes an unconfirmed host request visible
with a retry control.
If the host ends the current attempt while the phone is still connecting, the
phone displays that failure instead of silently returning to Ready to talk.
On iPhone, CallKit owns audio activation. The app requests the system call, and
media starts only after CallKit's activation callback; WebRTC uses that
activated session rather than configuring its own. A system hold or audio
deactivation mutes the microphone and keeps the call; CallKit's mute stays in
step with the app's mute. A system hangup ends local media without needing an
open screen. If iOS does not activate audio within 20 seconds, the call fails
visibly. A brief ICE disconnect has 10 seconds to recover before the call ends;
failed and closed are final. The Mac and web peers run in WebKit or the
browser, which own their own audio sessions. Terminal-state polling cleans
up local audio once per call, not on every refresh. Voice details shows the call
ID and last host-close reason/event number for a completed call. These messages
identify the teardown path, not the cause of an earlier host-side `closed` event.
Capture those details before attributing an instant disconnect. The iPhone also
keeps a local record of its last 20 call attempts (milestone times, the first
terminal cause, and for a failed attempt the step that failed with its error
domain and code, never the error's text) under Call lifecycle in Voice details.
The record starts when Call is pressed, so a failure choosing the target or
preparing the operator is kept too. When Call is unavailable, Voice details says why. An attempt the app never
finished, because it was force-quit or terminated, is labeled
`process_terminated_or_unknown` on the next launch.

The native apps keep a call alive with a heartbeat on the existing voice action
API: `{"action":"heartbeat","attemptId":"<current attempt>"}` with the paired
token. The host accepts it only from the call's owner for the current active
attempt; it renews liveness without calling Codex. The apps send the first
heartbeat after the host accepts `start`, then one every 10 seconds, and hang up
locally if none succeeds for 30 seconds. Once a call has sent a heartbeat, state
reads (`GET`) no longer renew it, so an idle open screen cannot keep a dead call
alive. The web dashboard does not enroll and keeps using its state reads. An
unrenewed call is stopped after about 40 seconds with terminal reason
`phone_disconnected`; a local stop records `local_hangup`. The reason survives
the native close event, and `start` clears it.

State lives beside `registry.db` at `voice/operator.json`, with a private operator
workspace at `voice/operator/`. The state file is atomically replaced with mode
0600. It retains the operator ID, skill and tool digests, call attempt, hashed device owner,
bounded recent events, text deduplication IDs, and the latest text receipt. SDP is
never persisted. A host
restart cannot restore a physical media connection; an active saved call becomes
unknown and requires hangup. Corrupt state blocks creation rather than discarding
identity. Do not delete it as a generic retry strategy.

The skill is pinned to the binary that created the operator. Updating a binary
does not rewrite developer instructions of an already-running Codex thread. The
details sheet shows the digest so this is inspectable. Reprovisioning a different
operator or recovering an ambiguous creation is a manual operator decision, not an
automatic migration.

The host-handled voice tools are pinned the same way. Codex registers dynamic
tools only in `thread/start`; `thread/resume` and the thread settings update take
none, and a resumed thread keeps the tools persisted at its start. The state file
records a digest of the tool set. When the saved operator was created with a
different set, the voice screen says so, naming **New conversation** as the way to
get the current tools, until the first call with that operator ends.

Recent voice events are limited to 100 records and 16 KiB per record. Normal
session activity uses the existing bounded, paginated timeline. Large or missing
records are labeled; neither channel is an unlimited transcript export. Codex owns
the size and cadence of native spoken responses; Agenthail does not duplicate or
truncate them through a second speech-submission path.

## Implementation and parent UI hook

Read these units in order:

1. `internal/surface/surfaces/codex_voice_managed.go`: the call's app-server
   connection, its event window, and tool-call answers repeating Codex's
   original request id. `codex_bridge.go` is the Desktop renderer contract for
   an operator Desktop has open: a tool answer is `mcp-response` with
   `threadId` and `response`, and the hook reinstalls whenever its source
   changes.
2. `internal/voice/service.go`: persistent identity, lease, call state, duplicate
   protection, and native request parameters. `skills/operations.go` embeds the
   distributed Operations skill.
3. `internal/surface/surfaces/codex_voice.go`: operator creation and the route
   choice: Desktop only while Desktop has the operator open, otherwise Codex's
   app-server. No approval-policy override.
4. `internal/daemon/voice.go` and `voice_peer.js`: control-scoped Bearer-only API
   and audio-only WebRTC program. A dashboard cookie cannot substitute for a
   valid control token.
5. `native/VoiceAPI.swift` and `native/VoiceOperatorModel.swift`: paired
   requests, the shared call model, heartbeat, cancellation guards and timeline
   integration. `native/VoiceAudioBridge.swift` is the Mac's origin-restricted
   WebKit audio.
6. `native/iOS/NativeVoiceAudio.swift`, `NativeVoicePeer.swift`,
   `VoiceSystemCall.swift`, `VoiceCallCoordinator.swift`, `VoiceOperator.swift`:
   the iPhone's CallKit call, native WebRTC peer, app-lifetime call ownership
   and call/conversation UI.

The reusable sheet is `AgenthailVoiceOperatorSheet(model:openSession:)`; it shows
the app's current call from `VoiceCallCoordinator`. Its callback opens the same
normal session ID, not a voice-specific replacement session.
The standalone app entry is a separate integration unit, allowing the main
Sessions UI to put that entry in its own toolbar. The consuming iOS target must
include the microphone purpose string, the `audio` and `voip` background modes,
the WebRTC package and its bundled license from `native/project.yml`; regenerate the
Xcode project with XcodeGen after changing that source.

## Verification

Ordinary tests do not call live models or record a microphone:

```bash
go test ./... -race -count=1
node --test internal/daemon/voice_peer_test.cjs
scripts/ios-simulator.sh iphone run xcodebuild test -project native/Agenthail.xcodeproj -scheme AgenthailIOS \
  -destination "platform=iOS Simulator,id=$(scripts/ios-simulator.sh)" \
  -parallel-testing-enabled NO \
  -only-testing:AgenthailIOSTests/VoiceTests \
  -only-testing:AgenthailIOSTests/NativeVoiceAudioTests \
  -only-testing:AgenthailIOSTests/VoiceCallDiagnosticsTests CODE_SIGN_IDENTITY=-
scripts/test-native-helpers.sh
```

The renderer regression executes the actual generated JavaScript in Node, including
flat notifications, target isolation, an empty event ring, and event loss. Media
tests execute the actual peer program with fixture browser interfaces. Service
tests exercise persistence, unknown outcomes, ownership, hangup versus interrupt,
lease expiry, and lost-event refusal. Native tests exercise the authenticated API,
live transcript assembly, previous-call polling during microphone permission,
unavailable audio, late asynchronous completion after dismissal, heartbeats,
app-lifetime ownership, and CallKit activation ordering with fixture system calls.
One test creates a real WebRTC offer in the Simulator. Ad-hoc signing
is intentional: unsigned Simulator app tests cannot exercise the app's Keychain
entitlements reliably.

### Actual iOS app evaluation

The [2026-09-12 Simulator receipt](codex-voice-simulator-evidence.md) records a real
Codex conversation through the native app when it still used a WebKit peer.
The current app uses the same native WebRTC peer on the Simulator and on a phone.
The Simulator refuses CallKit call requests
(`com.apple.CallKit.error.requesttransaction` code 2), so Simulator builds
activate the voice audio session directly instead of reporting a system call.
Everything else in the call is the same: coordinator, heartbeat, peer and hangup.

Use the dedicated Simulator and an isolated registry. Reuse a test-owned operator
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

Build the Debug iOS app normally. Without launch arguments, a call uses the
Simulator's microphone. For a repeatable conversation, place two user-speech WAV
recordings in its data container at `Documents/VoiceEvaluation/request-1.wav`
and `request-2.wav` and launch the app with `--voice-evaluation`. The screen
explicitly says **Simulated microphone · Real Codex**. This mode is compiled only
for Debug Simulator builds:

- A WebRTC audio device replaces the microphone and speaker. It skips microphone
  permission and refuses to call if either recording is missing.
- **Start voice call** sends the first recording after the real data channel
  opens. Wait for the spoken result; **Speak next request** sends the second.
  Silence is sent between requests, as a live microphone would.
- Received Codex audio is recorded, not played aloud. **Hang up** saves the
  mixed user/Codex dialogue as `real-codex-dialogue.wav` in the same directory;
  `diagnostics.jsonl` records input and output audio energy every five seconds.

Give the existing worker bounded, meaningful work, then independently inspect its
actual command output and completion. Check the spoken summary against those
results, not a predetermined phrase. Compare worktree state before and after a
read-only request. The host test's successful exit only means the host closed;
it is **not** the conversation's acceptance verdict.

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
AGENTHAIL_VOICE_SMOKE_FOLLOWUP_AUDIO=/absolute/path/to/select-and-message.wav \
AGENTHAIL_VOICE_SMOKE_WORKER=existing-test-owned-worker-uuid \
AGENTHAIL_VOICE_SMOKE_STATE=/absolute/path/to/test-only/operator.json \
go test -tags voice_live ./internal/daemon -run '^TestLiveCodexVoice$' \
  -v -count=1 -timeout=9m
```

The state file must already identify a test-owned operator. The evaluation refuses
to create an operator and rejects task-creation tool calls. Open its printed
loopback URL and choose **Speak listing request**. The first recording must ask
for live existing tasks without messaging or creating any. Wait for the spoken
listing before choosing **Speak followup request**. That recording should identify
the preselected existing test task and give it real bounded work. For example:
“Ask that agent to inspect this worktree, run the focused voice tests without
editing files, and report what passed and what still needs phone testing.”
Never target arbitrary existing work. After the completed reply is spoken, call
`window.finishProbe('existing-test-owned-worker-uuid')` in the test page console.
The harness captures listing/send evidence, the existing worker's new tool
activity and answer, voice events, and an independent read of the worker's reply.
It requires received audio energy and speech after an operator final answer, then
requests hangup. It does not use a fixed response phrase as semantic acceptance.

Review the captured JSON against the actual scenario: Was the requested target
selected? Did it receive the intended work? Did it run the reported commands?
Did the spoken summary accurately report their results and remaining limits?
Passing capture checks alone does not answer those questions. Preserve the state
path to exercise reconnect to the same operator. The synthesized microphone must
continue sending silence after each recording, as a live microphone would; an
inactive Web Audio source can stall realtime output and invalidate the evaluation.

Browser evaluation is not proof of the native iOS UI, physical iPhone microphone,
Bluetooth routing, Tailscale, permissions, or interruption behavior. The final phone check must use
the matching installed host and iOS builds: grant permission, speak a bounded
request, inspect the target's reply, mute from the app and from the system call
controls, lock the phone and switch apps while talking (audio should continue),
take a system hold or incoming call, hang up from the Lock Screen, and reopen
the same operator. Installing/restarting the live host remains an explicit
operator action. The Simulator cannot show the system call UI, Lock Screen
controls, background audio or Bluetooth routing; those need a phone.

## References and protocol provenance

The implementation was checked against the Codex CLI 0.163.0 app-server schema
(`codex app-server generate-json-schema --experimental`), Codex Desktop's bundled
CLI/app-server 0.154.0-alpha.6.2 schema and the native realtime implementation at
[Codex commit 364b511](https://github.com/openai/codex/tree/364b511dcd60bcde3849bde4e579a5c7cad1e96a/codex-rs).
Relevant contracts are `thread/start`, `thread/realtime/start`, `appendText`,
`stop`, and their asynchronous notifications. Schema availability is not evidence
that a particular account can connect; use a real call to verify it.

[pi-gippity-control](https://github.com/IgorWarzocha/howaboua-pi-stuff/tree/4593f066d447925eae8e3106435f117236690f9e/packages/pi-gippity-control)
was inspected as a concrete reference for realtime conversation plus tool-capable
agent delegation and cancellation. Its direct call/auth transport was not copied:
Agenthail goes through Codex's app-server, which owns the operator thread. This
feature does not substitute the separate STT/LLM/TTS architecture found in other
voice-agent frameworks.

| Reference mechanism | Agenthail adaptation |
| --- | --- |
| `conversation/session.ts`: validated delegation lifecycle | Agenthail uses the existing authenticated audio session but does not enable its legacy operator handoff route. |
| `register.ts`: `pi.sendUserMessage`, with steer when busy | Agenthail resolves and messages the explicitly selected existing session through its authenticated transport. |
| `controller.ts`: `finishAgentMessage` → `agentResult`; `handoff.ts`: target-scoped speakable result return | The reference informed Agenthail's receipt-plus-turn-ID requirement; Agenthail appends only that correlated target stream to its audio plane. |
| LAN controller retains the host audio call across device moves | Not implemented here: the phone owns its media peer. On iPhone it keeps running under CallKit in the background; moving a call to another device means hanging up and calling again on the same operator. |
