# Native session experience

Mode: Operate for session management; Read for the transcript.

Agenthail uses native navigation, semantic system colors, San Francisco for controls, a system serif for assistant prose, and a restrained orange tint. Sessions is the primary tab. Inbox holds delivery work and history. Settings holds connection and notification controls.

The session transcript is a reading surface: user messages have a quiet inset background, assistant replies use Markdown within a 600-point reading column, contiguous tool activity has one plain semantic summary with visible failure counts, and expanding the run reveals individual invocations with paired results. Reasoning remains inspectable within the run. Lifecycle events are quiet inline annotations. No generic activity wrapper, record-count card, or empty disclosure should interrupt reading.

The header keeps status, model, and context reachable without occupying a large part of the transcript. Session controls remain in the inspector. The rounded material composer has a clear send or steer placeholder, model selection, and a stop control when supported. Its receipt explicitly describes the latest instruction and opens the session inbox. The tab bar recedes in a session.

The session menu switches between Chat and All events. Original record order remains available in All events. Sessions are grouped by the actual Mac workspace, with live work indicators. Failed tools expose their error preview; missing results are labeled as missing, never inferred to be successful or running. Long code scrolls inside its own block. Body text follows Dynamic Type, controls have 44-point targets, and iPad retains a session sidebar at normal text sizes and uses a single column at accessibility sizes. Composer controls stack at accessibility sizes while icon buttons retain their target bounds.

Opening a conversation lands at its latest work. Scrolling back or inspecting a tool stops following incoming content; Jump to latest resumes following. Repository, environment and permission instructions use compact document rows that open the full recorded text. Long user messages have a short preview with Read full message. All events exposes the original sequence. Assistant headings stay close to body size, and inline code uses monospace without a filled background. Controls share a darker orange in light appearance for legible normal-size text.

The model picker is searchable and reads the configured runtime catalog. An unavailable catalog has an explicit reload path; the current model remains visible. Choosing a model requires Apply. Codex effort and plan mode affect the next normal instruction; steering uses the active turn. The phone does not expose schema editors or service-tier controls. Capability coverage is about understanding and directing work, rather than displaying every API parameter.

Inbox Current contains pending, inflight and unexpired failed deliveries. Past-expiry failed deliveries belong in History, including outcomes that were never confirmed. Expiry must not turn an unknown outcome into a claim that nothing was delivered. An inflight attempt remains current until its delivery result is known, even after its queue TTL. History never asks the user to dismiss old delivery failures as current decisions.
