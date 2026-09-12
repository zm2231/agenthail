# Agenthail on Mac and iPhone

Agenthail includes a Mac app, a menu bar shortcut, and an optional iPhone companion. The browser dashboard remains available too.

## Mac app

The Mac app opens automatically and keeps your connected conversations in one place.

- **Overview** shows what is working and which apps are connected.
- **Conversations** lets you read, send, steer, stop, compact, and change models when supported.
- Search starts with Agenthail's saved conversations; typing at least three characters can also search older Codex history on demand.
- **Operations** manages waiting messages, automatic handoffs, history, phone access, and settings.

The menu bar item gives you a quick connection check and opens the full app.

Agenthail updates the app, command line tool, and background service together. If parts of an installation ever fall out of sync, the app asks you to update instead of attempting an unsafe action.

## iPhone app

The iPhone and iPad app requires iOS/iPadOS 18 or later. It reaches your Mac privately through Tailscale. It does not make your Mac public on the internet.

[Request TestFlight access privately](mailto:zainmer@protonmail.com?subject=Agenthail%20iPhone%20TestFlight%20access&body=Please%20invite%20this%20Apple%20Account%20email%3A%20). An App Store distribution `.ipa` cannot be installed directly on an ordinary iPhone, so GitHub release downloads are not used for the companion app.

To connect it:

1. Install Tailscale on your Mac and iPhone and sign into the same account.
2. Open **Operations** in Agenthail on the Mac.
3. Turn on **Private phone access**.
4. Choose **Pair an iPhone**.
5. Scan the code with the Agenthail iPhone app.

The pairing code expires quickly and works once. You can disconnect a phone from either device.

On iPhone, **Sessions** is the place to find, start, and continue work. **Inbox** separates current delivery decisions from completed or expired instructions. **Settings** manages the connection and notifications.

Tap the compose button in Sessions to start work with a configured Claude, Codex or Notion runtime. Choose a recent Mac workspace or enter an existing directory for Claude/Codex, optionally choose a model, and write the first instruction. Runtime permission defaults remain in effect unless you change the Claude permission setting; approvals may require attention on the Mac. If creation cannot be confirmed, check the agent catalog on your Mac before retrying. A returned session with uncertain first-instruction delivery opens with that uncertainty visible.

Claude creation uses the installed CLI’s native background mode (`--bg`) and the identity returned by `claude agents --json --all`. Expand **Claude options** to choose a session name, new worktree, named agent, effort or permission mode. Successful registration does not mean the first turn is complete. This integration uses the native background path; Claude Remote Control also supports spawning sessions, but Agenthail does not need its cloud creation endpoint for this workflow. See [session operations](maintainers/session-operations.md) for lifecycle controls available through the CLI and dashboard.

Session details preserve recent Claude Code and Codex activity from local transcripts: messages, commentary, tool calls and results, available reasoning summaries, and compaction events. Assistant messages render Markdown, including code blocks and tables. Consecutive tool activity appears as a compact summary such as **3 commands, 2 file reads (1 failed)**. Expand the run to see each command, file or query, then open an invocation to inspect its input and paired output. Long outputs begin with a bounded preview; **Show full output** and **Copy output** retain the recorded text. Failed tool results show an error preview. A missing result is labeled **No result yet**, which does not claim the tool is still running.

The session menu offers **Chat**, which pairs tools with results within message boundaries, and **All events**, which shows original record order. Reasoning has its own disclosure; lifecycle records appear as inline annotations. **Load older activity** pages backward. Reading older content pauses automatic scrolling; **Jump to latest** resumes it.

The session menu or compact header opens context usage, reported token breakdowns, goal, model, workspace, connection source, and supported controls. Status, model and context remain in the compact header. Drafts belong to individual sessions. The rounded composer offers model selection, steering and stop when supported. Its **Latest instruction** receipt follows that instruction’s delivery outcome and opens the session inbox, where earlier instructions remain visible. An interrupted send remains explicitly unconfirmed; check the transcript before retrying.

Session details also let you name a session and set, edit, or clear a supported goal. **Session inbox** shows that session's delivery work. Inbox **Current** contains waiting/sending instructions and delivery attempts needing review. **History** contains up to the latest 100 completed, canceled or expired instructions across sessions. Sending again requires a delivery decision; it is never an automatic retry of an uncertain attempt. Pending or failed instructions can be canceled or dismissed. Instructions already in flight cannot be canceled from Inbox.

Sessions groups work by its reported Mac workspace, with a working indicator on active sessions. It offers **Running**, **Recent**, and **All**. Recent follows the host's current-session catalog; All includes saved sessions. Searching with at least three characters also queries older Codex history. Notification links resolve the target directly even when it is absent from the current list. On iPad, the session list and selected session sit side by side at normal text sizes. Accessibility text sizes use a single column and stacked composer controls.

Activity is read in bounded pages: up to 200 items and roughly 512 KiB of encoded items per page, with a 4 MiB local read window and 16 KiB per text item. Oversized records/output are marked as shortened. Images are identified but must be opened in the original agent app. When local activity is unavailable, the app displays available message history and explains the limitation. Reading a recorded permission or question tool does not approve it; this version does not expose live approval/question replies or a file browser. An open session refreshes activity every four seconds in addition to event-driven updates.

Agenthail only offers message controls when it knows a conversation is writable. Older Codex history can still be read, but it will not show a composer.

## Codex Voice orchestrator

With matching voice-enabled host and iOS builds, **Talk to orchestrator** opens
a conversational Codex Voice call. Ask about your agents, discuss a plan, or ask
the orchestrator to create an agent and send it work. It uses one persistent Codex
Desktop thread with the packaged Agenthail Operations skill, not on-device
dictation. Your Codex account must support native Voice and your paired phone must
have control permission.

Live conversation and expandable agent activity remain visible. **Open full
timeline** opens the same ordinary session. **Hang up** ends audio, not agent work;
leaving the app also ends the call. Call again to resume the same orchestrator.
Native approvals may still need attention on your Mac.

See [Voice setup, capabilities, and verification](maintainers/codex-voice.md).

## Notifications

Notifications are optional. Agenthail can tell you whether a Claude Code, Codex, or Notion agent finishes or fails without putting the agent name, conversation title, or conversation text in the alert. Tapping an alert opens the related conversation.

Turning notifications off removes that phone's notification registration. You can also forget a Mac from the iPhone app if the Mac is no longer available.

## Privacy

Phone access stays inside your Tailscale network. Pairing details are stored securely on each device, and you can revoke a paired phone at any time.

See the full [security and privacy model](../SECURITY.md).
