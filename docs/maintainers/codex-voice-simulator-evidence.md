# Native Codex Voice Simulator evaluation — 2026-09-12

**Observed:** a prerecorded natural-language user conversation went through the
actual Agenthail iOS app and real Codex Voice. The orchestrator inspected existing
tasks, messaged one existing test-owned agent, waited for real Go test output, and
spoke an accurate summary. **Not established:** physical-iPhone microphone,
Bluetooth, background/interruption behavior, or general agent-task correctness.

This is a scenario receipt, not a whole-product SHIP verdict. The worker's own
review verdict is not adopted as the feature's release gate.

## Environment and identity

| Item | Observed value |
| --- | --- |
| App | Real Debug iOS app, iOS 26.5 Simulator; no mock session fixture |
| Phone/host connection | Normal `read`/`control` pairing over a dedicated private Tailscale HTTPS port |
| Voice | Codex Desktop native realtime v3, WebRTC audio and data channel |
| Input | Two prerecorded user requests; native microphone capture denied in evaluation mode |
| Operator | `01a09411-31b7-74b2-9271-fce22eef8069`, reused |
| Audio attempt | `21951C82-7FC5-42D4-8A91-73735234F008` |
| Existing target | `@voice-smoke-test`, `01a09411-a169-7d00-823b-1168b816d261` |
| Delivery | Agenthail history row `5069`, result `01a0944b-7f2a-7d91-8461-9d6ce2294cb2` |

No new task was requested or created by this scenario. The two delegated Codex
operator turns contain discovery, one send, and reply/status inspection; the
target's canonical timeline records the same delivered request and executed work.

## Observed conversation

All times are UTC. These are actual transcripts and commands, not expected reply
fixtures. The public record omits unrelated task output and authentication data.

1. **06:24:58 — user:** “What are my existing Codex tasks doing? List a few with
   their status. Do not create tasks or send any messages yet.”
2. **06:25:07 — operator command:** the matching built `agenthail list --json`.
   It returned current Codex sessions and a separate Notion discovery error.
   Codex spoke a few returned task names/statuses; it did not claim all surfaces
   were healthy or create a task. Operator turn:
   `01a0944a-6325-7b51-b6a7-1aa1577c6d2f`.
3. **06:26:04 — user:** “Ask voice smoke test to review this Agent Hail voice work
   tree, have it run a focused go test for the voice service without editing
   files, then explain what passed or failed and what’s still needs a real phone
   test. Wait for its actual results and summarize them for me. Do not create a
   new task.”
4. **06:26:10–11 — delivery:** `agenthail send @voice-smoke-test … --reply --json
   --timeout 10m`. The existing target recorded the instruction. The operator
   initially supplied a nested build directory; the worker resolved the real
   worktree with `git rev-parse --show-toplevel` before testing. No corrective
   message was injected into either agent.
5. **06:26:50 — real worker command**, from the resolved worktree:

   ```sh
   go test ./internal/daemon -run 'TestVoice(EndpointsRequirePairedControlAndNativeBearer|ControlScopeAndRevocationApplyToEveryEndpoint)$' -count=1
   ```

   Exit 0: `ok github.com/zm2231/agenthail/internal/daemon 0.025s`.
   These are **endpoint authentication and revocation tests**, not every voice
   service/lifecycle test. The wider local suite is a separate release check.
6. **06:27:30 — worker completion:** reported that exact command/result and the
   remaining physical-iPhone checks. Independent `agenthail last` and `history`
   by the target's full ID confirmed the received work and returned result.
7. **06:27:52 — operator completion:** turn
   `01a0944b-6423-76b1-8391-3f8c36ef6ba2`, final item
   `msg_02478eba00193a03016aa4f0e5c4a087d1a52433583bfe27cd`.
   The then-current manual speech-return request was accepted for this item. The
   current implementation uses Codex's target-scoped native delegation routing.
8. **Real returned speech:** “`@voice-smoke-test` reports the focused Go test
   passed: daemon voice endpoint auth and revocation checks are green. It also
   notes a real paired iPhone test is still required for mic permission, web-peer
   auth, two-way audio, receipts, and hangup behavior.”
9. **Native Hang up:** stopped the peer; host state recorded `ended` for the same
   attempt. The temporary host and dedicated Serve rule were then removed.

## Independent evidence retained locally

The original Codex rollouts contain native transcript segments, operator commands,
target user-message receipt, executed test output, and completed answers. The
isolated evaluation output is `build/native-voice.IAd6N6/` in the development
worktree. It is ignored, not part of the shipped app or an uploaded user transcript.

- `real-codex-dialogue.mp4`: actual mixed audio, AAC stereo at 48 kHz, 227.628 s,
  4,298,041 bytes; SHA-256
  `185405e2e6cd9e4c80202cf5ce2037a4b607d861728d585dd7fc782637b844b0`.
- `real-codex-dialogue.m4a`: lossless container copy for playback; SHA-256
  `b0c5ce1036955a6144534299034e73d27e5223727b0168f3aeacf14b7de40615`.
- `diagnostics.jsonl`: the accepted input hook, running audio context, increasing
  inbound/outbound packet counters, and nonzero received Codex audio energy.
- `voice/operator.json`: matching attempt, `ended`, accepted final-answer speech
  receipt, and actual transcript events. It contains no persisted SDP.
- Worktree tracked-diff SHA-256 before/after worker execution was identical:
  `d3329bb21b612e0c5db3c132813ae97461298b40e0d0390395c53dcecf879a00`.
  Both untracked source files were separately hashed and also unchanged.

The recording was presented for local review. Its existence alone is not a pass:
the actual target, commands, completion, spoken summary, and hangup were checked
individually. Follow [the evaluation procedure](codex-voice.md#actual-ios-app-evaluation)
to reproduce with your own test-owned tasks and recordings.

## Final-build follow-up checks

After the native lifecycle fixes, the final build reconnected to the **same**
operator with attempt `9102A133-556A-447C-A333-E21C0821607C`. A fresh recorded
discovery request produced live task output and real spoken results; no worker
message or task creation was requested in this follow-up. Native hangup recorded
`ended`. The updated recorder/context teardown produced an 81.302667-second,
1,376,630-byte audio file (`build/native-voice.UlRwZb/reconnect-dialogue.mp4`),
SHA-256 `ff7b116173219eb67e68abbc0a7b8b80b954adccbd2f48e780bd52ba3d0cae0d`.

The actual paired **Release** app was separately opened with the evaluation flag;
it showed normal Voice controls with no simulated-microphone label or playback
button. After the isolated host closed, the final Debug app showed **Reconnecting
to your Mac** and disabled **Call Codex Voice** while retaining the conversation.

Completed local checks:

- 37 native unit tests, zero failures. The permission-polling regression failed
  with the old behavior (three assertions), then passed with the fix.
- `go test ./... -race -count=1`, `go vet ./...`, and all four actual peer-program
  Node tests.
- Release Simulator build, generated Xcode project parity, and public-copy check.
- Independent final unscoped review: no blocking findings. Physical-device checks
  remain unverified; PR CI is a separate required publication gate.
