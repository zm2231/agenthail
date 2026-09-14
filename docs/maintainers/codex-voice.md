# Talk to an Agenthail orchestrator through Codex Voice

The iPhone companion has a conversational Codex Voice operator backed by one
persistent Codex Desktop thread on your Mac. It receives the packaged Agenthail
Operations skill before its first turn. You can discuss a plan, ask about live
agents, create a new agent, or ask it to send work and read back the result.
There is no on-device speech recognition, command grammar, separate TTS service,
or separate OpenAI API key.

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
4. Choose **Talk to orchestrator**, then **Call Codex Voice**. Grant microphone
   access. **Connected** requires both the audio peer and its data channel;
   accepting the start request alone is not a connected call.
5. Try “List my existing Codex tasks,” then identify one and ask what it is
   working on. Send a bounded instruction only to a task you own and intend to
   control. Inspect **Agent activity** and **Open full timeline** to verify
   tools, delivery outcomes, and the actual reply.

Only audio and WebRTC negotiation go to Codex's realtime service. The phone sends
control requests to the paired Mac over HTTPS. The audio page contains no paired
token or Codex credentials; its native container supplies the authenticated page
request. Codex's existing authenticated client creates the realtime call.
The entry is a compact control above the tab bar; Sessions, Inbox, and Settings
remain available while it is present.

## Conversation and work flow

```text
iPhone microphone ⇄ Codex realtime voice
                         │ native automatic handoff
                         ▼
                persistent Codex operator
                + embedded Operations skill
                         │ agenthail CLI
                         ▼
                existing target resolution
                + capability / owner checks
                         │
                         ▼
                   selected agent
                         │ receipt and reply
                         └──────► operator final answer
                                         │ correlated turn / item
                                         ▼
                              native appendSpeech → spoken response

iPhone call controls ── authenticated Agenthail API ── Desktop app-server
iPhone conversation ◄─ live voice events + normal recorded session timeline
```

The voice model handles natural conversation. It hands environment questions and
actions to the regular Codex agent, which can use tools. The operator resolves
targets through Agenthail's live discovery and ambiguity checks. No local parser
turns a guessed agent name into a command. A conversational explanation of a plan
is not a universal extra confirmation gate: the operator acts within the user's
request and asks when the target or authority is unclear.

The host supplies its own executable's absolute path in the operator instructions.
This keeps CLI examples on the same build as the voice host even when the shell's
`PATH` contains an older installation. Native incoming delegation runs the regular
Codex agent. Response forwarding is explicitly client-managed: the host observes
an operator turn start and its final-answer item, matches the turn ID, and submits
that exact completed answer to `thread/realtime/appendSpeech`. This remains Codex's
native voice, not a separate TTS service. Automatic response forwarding is disabled.
An accepted speech submission is not proof that it was heard; live voice transcript
and received audio are separate evidence. Unknown submissions are not replayed.

Agenthail's delivery semantics still apply. Accepted, queued, delivered, completed,
failed, and unknown are different outcomes. Other agents' output is data, not
permission to expand the user's request. Unsupported controls fail through the
existing transport; the voice layer does not emulate them.

## Capabilities and limits

| Surface | Behavior |
| --- | --- |
| Conversation | Native Codex realtime audio delegates to the backing agent; correlated completed answers return through native `appendSpeech`. No dictation-only replacement. |
| Agent operations | The complete packaged `agenthail-operations` skill is embedded in the host binary and passed as literal developer instructions when creating the operator. Availability still depends on configured runtimes and their capabilities. |
| Identity | One saved operator per Agenthail registry. Calling again reuses its thread, work, and history; it creates a new audio connection, not a new operator. |
| Phone visibility | Live transcript deltas and completed utterances; expandable normal agent tool activity; full session timeline; connection, occupancy, truncation, and error states. Native recorded voice segments also appear in the normal timeline. |
| Text during a call | **Type** sends a user text item through Codex realtime. Message IDs prevent automatic replay after an uncertain response. |
| Mute | Stops sending microphone content without stopping the call or agent work. |
| Hang up / leave app | Stops local microphone and peer immediately, then requests native realtime stop. It does not interrupt an agent turn. Calls do not continue in the background. |
| Interrupt | Separate confirmed **Interrupt orchestrator turn** uses the existing native interrupt capability. It does not stop already-delegated agents. A spoken request to stop a worker is resolved to that specific worker and its capabilities. |
| Approvals | Existing native approval policy remains in effect. The phone can display recorded activity but does not implement approval/question replies. A blocked native approval needs attention on the Mac. |
| Multiple phones | One paired token owns an active call. Other devices see occupancy and cannot take over or read its negotiation SDP. |
| Reconnect | No automatic redial or inference replay. Hang up an uncertain call, then call again. A missing phone heartbeat requests audio stop after about 40 seconds; worker turns remain. |

This is a foreground call, not CallKit, a background telephone service, or a
remote desktop. Claude and Notion may be targets where their existing operations
permit it; they are not alternative voice providers. Voice availability and model
usage remain subject to the signed-in Codex account.

## Lifecycle, failures, and storage

`prepare` records creation intent, creates the Desktop-owned thread with the full
skill, saves its returned identity, and registers it for normal session discovery.
No bootstrap turn runs during preparation. Creation with an unknown outcome does
not create another operator automatically: inspect Codex and the state file before
attempting recovery.

`start` captures the Desktop event cursor before sending the microphone SDP offer.
The peer submits the offer after `setLocalDescription`; ICE gathering may continue
while signaling proceeds. A network that does not report `complete` gathering is
not treated as a failed call before the host receives the offer.
The native start request uses realtime `v3`, audio output, startup context, and
native incoming Codex delegation and explicit response return. A matching native `started` notification binds the call;
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
If the host ends the current attempt while the phone is still connecting, the
phone displays that failure instead of silently returning to Ready to talk.
An iOS microphone interruption displays its own reason; an interruption-ended
notification alone does not hang up an active call. Voice details shows the call
ID and last host-close reason/event number for a completed call. These messages
identify the teardown path, not the cause of an earlier host-side `closed` event.
Capture those details before attributing an instant disconnect.

State lives beside `registry.db` at `voice/operator.json`, with a private operator
workspace at `voice/operator/`. The state file is atomically replaced with mode
0600. It retains the operator ID, skill digest, call attempt, hashed device owner,
bounded recent events, text deduplication IDs, and the latest speech submission
receipt. SDP is never persisted. A host
restart cannot restore a physical media connection; an active saved call becomes
unknown and requires hangup. Corrupt state blocks creation rather than discarding
identity. Do not delete it as a generic retry strategy.

The skill is pinned to the binary that created the operator. Updating a binary
does not rewrite developer instructions of an already-running Codex thread. The
details sheet shows the digest so this is inspectable. Reprovisioning a different
operator or recovering an ambiguous creation is a manual operator decision, not an
automatic migration.

Recent voice events are limited to 100 records and 16 KiB per record. Normal
session activity uses the existing bounded, paginated timeline. Large or missing
records are labeled; neither channel is an unlimited transcript export. Spoken
returns are limited to 16 KiB per answer and 128 answers per call. Oversized answers
remain in the normal timeline with a visible limit message.

## Implementation and parent UI hook

Read these units in order:

1. `internal/surface/surfaces/codex_bridge.go`: actual Desktop renderer message
   contract. Notifications are flat `method`/`params`; responses contain `message`.
2. `internal/voice/service.go`: persistent identity, lease, call state, duplicate
   protection, and native request parameters. `skills/operations.go` embeds the
   distributed Operations skill.
3. `internal/surface/surfaces/codex_voice.go`: existing Desktop owner/client and
   event cursor. No managed-runtime takeover or approval-policy override.
4. `internal/daemon/voice.go` and `voice_peer.js`: control-scoped Bearer-only API
   and audio-only WebRTC program. A dashboard cookie cannot substitute for a
   valid control token.
5. `native/iOS/VoiceAPI.swift`, `VoiceAudioBridge.swift`, `VoiceOperator.swift`:
   paired HTTPS requests, origin-restricted WebKit audio, call/conversation UI,
   cancellation guards, and normal timeline integration.

The reusable sheet is `AgenthailVoiceOperatorSheet(openSession:)`; its callback
opens the same normal session ID, not a voice-specific replacement session.
The standalone app entry is a separate integration unit, allowing the main
Sessions UI to put that entry in its own toolbar. The consuming iOS target must
include the microphone purpose string from `native/project.yml`; regenerate the
Xcode project with XcodeGen after changing that source.

## Verification

Ordinary tests do not call live models or record a microphone:

```bash
go test ./... -race -count=1
node --test internal/daemon/voice_peer_test.cjs
xcodebuild test -project native/Agenthail.xcodeproj -scheme AgenthailIOS \
  -destination 'platform=iOS Simulator,id=YOUR_TEST_DEVICE_UUID' \
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

Build the Debug iOS app normally. Place two user-speech WAV recordings in its
data container at `Documents/VoiceEvaluation/request-1.wav` and `request-2.wav`.
Launch the app with `--voice-evaluation`. The screen explicitly says **Simulated
microphone · Real Codex**. This mode is compiled only for Debug Simulator builds:

- It refuses native microphone permission and refuses to call if the recording
  hook is missing. The WebKit media-device object is retained for the lifetime
  of the fixture, so its capture override survives between initialization and use.
- **Call Codex Voice** sends the first recording after the real data channel opens.
  Wait for the spoken discovery result; **Speak next request** sends the second.
- A continuous silent source keeps outgoing audio alive between requests.
  **Hang up** saves the actual mixed user/Codex dialogue to the same directory;
  `diagnostics.jsonl` includes input/output media counters and audio energy.

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
request, inspect the target's reply, mute, hang up, reopen the same operator, and
background the app. Installing/restarting the live host remains an explicit
operator action. Simulator/sample screenshots are not live voice receipts.

## References and protocol provenance

The implementation was checked against Codex Desktop's bundled app-server
0.153.4 experimental schema and the native realtime implementation at
[Codex commit 3d2ee51](https://github.com/openai/codex/tree/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs).
Relevant contracts are `thread/start`, `thread/realtime/start`, `appendText`, `appendSpeech`,
`stop`, and their asynchronous notifications. Schema availability is not evidence
that a particular account can connect; use a real call to verify it.

[pi-gippity-control](https://github.com/IgorWarzocha/howaboua-pi-stuff/tree/1892cc8f36303a0fd92a3d267fd6658e07fdf418/packages/pi-gippity-control)
was inspected as a concrete reference for realtime conversation plus tool-capable
agent delegation and cancellation. Its direct call/auth transport was not copied:
Agenthail uses the app-server that already owns its Codex Desktop thread. This
feature does not substitute the separate STT/LLM/TTS architecture found in other
voice-agent frameworks.

| Reference mechanism | Agenthail adaptation |
| --- | --- |
| `conversation/session.ts`: validated `delegation.created` input | Native Codex app-server owns incoming delegation into the same persistent operator. The phone does not transcribe or parse commands. |
| `register.ts`: `pi.sendUserMessage`, with steer when busy | Codex operator uses the embedded Operations skill and existing authenticated Agenthail transport to resolve and message the intended session. |
| `controller.ts`: `finishAgentMessage` → `agentResult`; `handoff.ts`: speakable result return | Host observes `turn/started` and the same turn's final `item/completed`, then calls native `appendSpeech` once. No reasoning or tool payload is spoken as a final answer. |
| LAN controller retains the host audio call across device moves | Not implemented here: the phone owns its foreground media peer. Backgrounding ends audio; the operator and work persist. Reopening explicitly starts a new call on the same operator. |
