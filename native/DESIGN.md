# Native session experience

Mode: Operate for session management; Read for the transcript.

On iPhone and iPad, Agenthail uses native navigation, semantic system colors, San Francisco for controls, a system serif for assistant prose, and a blue tint for controls, so amber means only that something needs the person. Sessions is the primary tab. Inbox holds delivery work and history. Settings holds connection and notification controls.

The session transcript is a reading surface: user messages have a quiet inset background, assistant replies use Markdown within a 600-point reading column, contiguous tool activity has one plain semantic summary with visible failure counts, and expanding the run reveals individual invocations with paired results. Reasoning remains inspectable within the run. Lifecycle events are quiet inline annotations. No generic activity wrapper, record-count card, or empty disclosure should interrupt reading.

The header keeps status, model, and context reachable without occupying a large part of the transcript. Session controls remain in the inspector. The rounded material composer has a clear send or steer placeholder, model selection, and a stop control when supported. Its receipt explicitly describes the latest instruction and goes to a queued message's row at the end of the transcript. The tab bar recedes in a session.

The session menu switches between Chat and All events. Original record order remains available in All events. Sessions are grouped by the actual Mac workspace, with live work indicators. Failed tools expose their error preview; missing results are labeled as missing, never inferred to be successful or running. Long code scrolls inside its own block. Body text follows Dynamic Type, controls have 44-point targets, and iPad retains a session sidebar at normal text sizes and uses a single column at accessibility sizes. Composer controls stack at accessibility sizes while icon buttons retain their target bounds.

Opening a conversation lands at its latest work. Scrolling back or inspecting a tool stops following incoming content; Jump to latest resumes following. Repository, environment and permission instructions use compact document rows that open the full recorded text. Long user messages have a short preview with Read full message. All events exposes the original sequence. Assistant headings stay close to body size, and inline code uses monospace without a filled background. Controls share a darker blue in light appearance for legible normal-size text.

The model picker is searchable and reads the configured runtime catalog. An unavailable catalog has an explicit reload path; the current model remains visible. Choosing a model requires Apply. Codex effort and plan mode affect the next normal instruction; steering uses the active turn. The phone does not expose schema editors or service-tier controls. Capability coverage is about understanding and directing work, rather than displaying every API parameter.

Inbox Current contains pending, inflight and recent failed deliveries. Pending instructions never expire. Failed deliveries move to History an hour after they failed, including outcomes that were never confirmed. Moving to History must not turn an unknown outcome into a claim that nothing was delivered. An inflight attempt remains current until its delivery result is known. History never asks the user to dismiss old delivery failures as current decisions.

## Mac visual system

The Mac app draws from the tokens in `macOS/DesktopPalette.swift`, whose surfaces and text roles come from `internal/uiconf/palette.json`, and follows the system appearance, with light and dark specified.

- Neutrals are graphite: window, sidebar, raised surfaces, hairlines, and three text levels. Every text role reads at 4.5:1 or more on every Mac surface, including the amber and red status grounds, before any lift.
- Each status colour has one job. Amber marks only what needs the person: a session waiting on an answer, a question not answered yet, a message that needs a decision, a message that was not delivered. Blue is live work, green is finished or passed, red is an error or broken state. Idle has no mark. Warnings that need nothing from the person, such as context or usage running high, use body text. `TestEachStatusColourKeepsItsOneJob` lists every file that uses amber, blue or green and what it marks there, on Mac and iPhone.
- Interaction (buttons, links, focus) uses the system accent colour, so it follows the person's own setting. Selection is a neutral grey wash, never the accent, so a selected row never reads as focus or as a state.
- Messages from other agents name the agent in body text; colour is for state, not identity.
- Project colours mark project identity only, on a project's tile mark; they never mark state, and status colours never mark a project. Six muted hues sit between the status hues; a project's hue comes from its id, so it is the same on every computer, the Mac, iPhone and the web dashboard. The `project` entry in `TestEachStatusColourKeepsItsOneJob` lists where they are used.
- SF Pro throughout, including assistant and user prose; SF Mono only for code, paths, commands and hashes. The scale is 11 caption, 11.5 meta, 12 secondary, 13 body, 14.5 prose with a 1.58 line height, 15 title, 20 display.
- Spacing sits on a 4-point grid. Radii are 4 for badges, 6 for controls, 8 for rows, 12 for cards and menus, 16 for the composer and sheets.
