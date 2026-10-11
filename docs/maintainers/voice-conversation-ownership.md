# Voice conversation ownership

Agenthail owns the voice delegation lifecycle. The phone connects directly to a Codex-authenticated realtime call with `delegation.type: client`. The app-server is the coding executor and login manager, not a second realtime sideband. The embedded realtime system prompt becomes the direct call's `session.instructions`.

## Inputs and outputs

The ordered native data channel forwards `delegation.created`, transcript lifecycle and `turn.done` through the authenticated voice endpoint. Each envelope carries a call attempt, binding generation and contiguous sequence. Duplicates are ignored; gaps fail closed rather than replaying uncertain actions.

Explicit delegations create coding-executor requests serialized through turn completion so two requests cannot claim the same active turn. The realtime model remains conversational while execution is pending. Worker instructions use the bound `session_send` tool, which steers busy workers rather than queuing instructions.

Native tools require the same executor-turn receipt as output frames. Events arriving before that receipt wait for correlation; an unrelated turn in the persistent executor cannot send to workers or change the call's binding.

Only assistant results correlated to an executor receipt produce output frames: `{kind, id, generation, delegationId, channel: "speakable", text}`. For a native delegation the phone sends each once as `delegation.context.append`, then acknowledges local data-channel acceptance. That is not evidence of audible completion. Tool arguments, results, reasoning and unrelated assistant messages never become speech frames.

Worker subscriptions retain bounded latest observations on the host. They produce no realtime append and no speech. Current facts are available through `session_read`; initial selected-worker history is a bounded, untrusted developer message in a fresh conversation. Ordinary activity cannot independently start or interrupt an answer. This does not implement unsolicited milestone announcements.

Explicit typed input goes through the daemon's same serialized executor queue, with a unique message ID. Its correlated result has kind `typed-result` and uses `session.context.append`, not an invented upstream delegation ID. No direct typed request is also sent to the realtime model. Correlated typed actions remain authorized while the microphone is muted. Automatic observations never use this path.

## Transfer

Target selection prepares its subscription, retires the previous binding and requests a fresh realtime conversation. The phone replaces the peer and SDP answer while retaining the CallKit UUID and audio-session activation. A destination-scoped executor receives the same native Agenthail tools and only the selected worker's context. Return selects the persistent general executor and creates a fresh realtime conversation.

Subscription preparation is not completed transfer. Completion requires the phone's connection acknowledgment with the current binding generation. Replacement failure ends the call rather than leaving a dead peer labelled connected. Retired peer callbacks and binding events are rejected. Transfer and hangup do not cancel background worker work.

## Interruption and mute

Native input/output generations suppress audio belonging to the previous response when the user interrupts. Result submission waits while user input or assistant output is active. Transcript lifecycle is not hardware playback completion; physical-device acceptance must check that boundary.

Track mute applies locally, then reports an attempt-bound monotonic revision. Provider delegations cannot start work while muted; voice-originated worker actions are rejected. Explicit controls and typed input are separate authority. Provider transcripts during mute remain provider evidence, not attributed microphone speech.

## Failure and verification

Call state persists privately; output frames are volatile and never replayed after restart. Event loss, queue exhaustion, uncertain submission and forwarding failures must remain observable. Actions are not automatically retried. Tools have independent operation and response deadlines; cached responses do not repeat effects.

Fixtures cover setup, credential containment, redirects, budgets, ordered delegation ingestion, correlated output, retained observations, mute fencing, follow-ups and peer replacement. The installed-provider probe demonstrates why app-server realtime is not the ownership boundary. Audible interruption, Bluetooth, background/lock and repeated live transfers still require iPhone acceptance. Builds and receipts do not establish those outcomes.
