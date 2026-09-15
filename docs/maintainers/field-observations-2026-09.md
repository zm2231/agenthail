# September 2026 field observations

This is an operator-facing record of reported outcomes and follow-up checks, not
an assertion that every report still reproduces. Keep a delivery receipt, a
phone audio observation, and a completed model turn distinct.

| Report / check | Evidence available here | Disposition and next check |
| --- | --- | --- |
| Real Codex Voice conversation: two consecutive answers, only first audible; Hang up felt 5–10 seconds late | User screenshots of two answers and user report | Earlier symptom. On 2026-09-15, the user repeated an interim-update/final-answer call: **both were spoken and Hang up stopped audio immediately**. Check another call with call ID, host-close event, build, and audio route if the symptom recurs. |
| Claude held an Agenthail relay from `@agent-hands-fixes` (`hops=1`, route `id=40`) | User-provided Claude notification: verified UDS PID `84034`, sender permission mode unattested, receiver bypassing prompts, **one held / not delivered** | Inspect the full held message and receiving session's `/config` → **messages from your other sessions** → **Accept** policy if cross-session receipt is intended. Verify the actual hold disposition/history; do not infer delivery from the preview. See [Claude peer setup](claude-peers.md#claude-inbound-policy-for-cross-session-messages). |
| `send --reply` accepted a delivery and Codex read the message, but the expected delivery notification did not appear | User report of the message being read and notification absent; a separate live check returned a turn ID and completed reply | Notification presentation/receipt correlation remains a separate investigation. Inspect the actual send receipt, target transcript, notification state, and matching turn ID; do not equate a missing notification with failed send. A peer-message PR preview was held in Claude, so it is not a verified correction here. |
| CLI session listing showed `?` for connected Claude peers and `run` on a session last seen 25 minutes earlier | User-provided table; prior CLI observations also found optional Notion discovery could return a nonzero exit while rows were rendered | Status/liveness semantics need a fresh live reproduction. Separate current discovery, historical saved rows, reported state, and actual activity. Do not interpret `?` as idle or a saved `run` as proof of work. |
| Codex Desktop app-server read RPC timed out while peer work was still delivered | User-provided worker transcript and subsequent confirmation of arrival | Read timeout and delivery state must be checked independently using the exact target identity, receipt, and transcript. Do not automatically resend on a timed-out read. |
| Transcript-screen accessibility test failed twice in full CI but passed in isolation locally | Earlier user-provided CI report | CI outcome for a later fix is not established by this record. Check the current full-run result and test isolation before declaring it fixed. |

The physical-phone observation is user-reported, not captured by the Simulator
harness. The Claude notification describes a **held** message; no full-body review
or receiving-session acknowledgement was provided. Update each row with a dated
receipt when its next check completes, rather than changing its status based on
an unrelated release or a passing focused test.
