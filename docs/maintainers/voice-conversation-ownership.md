# Voice conversation ownership

The native audio connection belongs to the persistent operator. Selecting a worker changes its journal subscription, not its audio credentials or microphone connection.

Worker journal updates use an explicit commentary channel. Reasoning, tool arguments, tool output and worker user messages do not enter the automatic update channel. Bound read tools still provide explicit, bounded reads. Assistant progress is accumulated to a semantic boundary or final item; the conversation controller coalesces delayed versions by stable item ID. Queued prefixes are tracked, so superseded buffered versions do not lose text.

An active user or assistant realtime transcript holds the automatic context queue. Item-based completion retires only that item, not another concurrent response; role-based completion retires the current transcript for that role. Context resumes after a quiet interval. Transfer and hangup discard buffered context for the prior binding; stale callbacks are rejected by call attempt and binding generation.

Call startup enables `clientManagedHandoffs` and disables backend reasoning status and delegation filler. Only backing operator turns carrying the provider's realtime-delegation wrapper own explicit speech. Ordinary backing turns and transcript-tail flushes do not. Completed operator commentary/final items enter the serialized speakable queue through `appendSpeech`. A transfer initiated by an owned turn retains that turn's transfer response, while retiring prior worker updates. Failed speech submission is never automatically replayed.

The installed `appendText` schema has no commentary selector. Its v3 frame omits the channel, so it is not used for worker context. The host instead exposes at most 64 `contextFrames` in the owning device's voice state: `{id, generation, text}`, alongside `bindingGeneration`. The phone rejects retired generations and sends each ID once over `oai-events` as `session.context.append` with `channel: commentary`, using UTF-8-safe 500-byte chunks. It then sends `context-ack` with `attemptId` and `contextAck`. Acknowledgment means local data-channel acceptance, not proof of model consumption. Transfer drops unsent old frames; hangup retires the queue. Frames already transmitted before the phone observes a transfer cannot be recalled. A send or acknowledgment failure ends local audio instead of replaying uncertain context. Startup requires `contextTransport: native-commentary`; an older client is rejected with an upgrade instruction.

The gate observes transcript lifecycle, not physical speaker completion. It consumes both role-based transcript delta/done notifications emitted by the installed app-server and item-based lifecycle notifications when supplied. The installed app-server protocol has no realtime response-cancel or context-reset request. These boundaries do not establish that the remote model cannot spontaneously generate speech or interrupt existing output. Native data-channel acceptance and its audible behavior still require physical-device validation.

Context frames are volatile call data: enqueue and acknowledgment do no filesystem I/O. The selected target is persisted before retiring the previous subscription. A host restart fails the call closed and does not replay old frames. Each new call starts an empty voice transcript ring; recorded operator work remains in the session timeline.

## Muted input

The iPhone applies track mute locally, then reports `input-state` to the host with the call attempt, boolean `muted`, and monotonic `inputRevision`. Stale revisions cannot overwrite newer state. CallKit hold uses the same effective track state. A reporting failure ends local audio rather than allowing unconfirmed voice instructions.

While muted, voice-originated send, worker interrupt and transfer tools are rejected. Explicit call buttons and typed input remain separate paths. Read tools remain available. The host does not relabel an upstream user transcript as a real microphone utterance merely because the provider assigns it role user.

The phone records bounded, content-free mute transitions and raw realtime event types with their local mute state. Incoming provider user transcripts during mute remain evidence to investigate, not proof that the user spoke. This telemetry cannot determine whether the origin was buffered input, remote transcription or another upstream source.

## Verification boundary

Tests cover response ownership, superseded snapshots, prefix handling, transfer cleanup, mute revision ordering, rejected worker actions and canonical transcript item identity. Physical iPhone testing must still demonstrate silent muted operation, uninterrupted speech, sequential progress and final answers, and repeated transfers without previous-worker output. Those outcomes must not be inferred from unit tests or an accepted transfer receipt.
