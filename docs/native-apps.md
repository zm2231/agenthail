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

The iPhone app reaches your Mac privately through Tailscale. It does not make your Mac public on the internet.

[Request TestFlight access privately](mailto:zainmer@protonmail.com?subject=Agenthail%20iPhone%20TestFlight%20access&body=Please%20invite%20this%20Apple%20Account%20email%3A%20). An App Store distribution `.ipa` cannot be installed directly on an ordinary iPhone, so GitHub release downloads are not used for the companion app.

To connect it:

1. Install Tailscale on your Mac and iPhone and sign into the same account.
2. Open **Operations** in Agenthail on the Mac.
3. Turn on **Private phone access**.
4. Choose **Pair an iPhone**.
5. Scan the code with the Agenthail iPhone app.

The pairing code expires quickly and works once. You can disconnect a phone from either device.

On iPhone, **Today** shows current work, **Conversations** lets you check in or send the next instruction, and **Settings** manages the connection and notifications.

Conversation details preserve recent Claude Code and Codex activity from local transcripts: messages, commentary, tool calls and results, available reasoning summaries, and compaction events. Expand a tool to inspect its recorded input or output. Commands, edit inputs, and plans have focused presentations; unknown tools keep their recorded content. Choose **Activity** to focus on agent work, and **Load older activity** to page backward. Reading older content pauses automatic scrolling; **Jump to latest** resumes it.

The info button opens context usage, reported token breakdowns, goal, model, workspace, connection source, and supported controls. Context usage also stays beside the composer. Drafts belong to individual conversations, and send feedback distinguishes a queued instruction from an accepted one. If delivery cannot be confirmed, check the transcript before retrying.

**Saved** shows conversations known to Agenthail. Searching with at least three characters also queries older Codex history. Notification links resolve the target directly even when it is absent from the current list. On iPad, the conversation list and selected session can sit side by side.

Activity is read in bounded pages: up to 200 items and roughly 512 KiB of encoded items per page, with a 4 MiB local read window and 16 KiB per text item. Oversized records/output are marked as shortened. Images are identified but must be opened in the original agent app. When local activity is unavailable, the app displays available message history and explains the limitation. Reading a recorded permission or question tool does not approve it; this version does not expose live approval/question replies or a file browser. A visible working session refreshes activity every four seconds in addition to event-driven updates.

Agenthail only offers message controls when it knows a conversation is writable. Older Codex history can still be read, but it will not show a composer.

## Notifications

Notifications are optional. Agenthail can tell you whether a Claude Code, Codex, or Notion agent finishes or fails without putting the agent name, conversation title, or conversation text in the alert. Tapping an alert opens the related conversation.

Turning notifications off removes that phone's notification registration. You can also forget a Mac from the iPhone app if the Mac is no longer available.

## Privacy

Phone access stays inside your Tailscale network. Pairing details are stored securely on each device, and you can revoke a paired phone at any time.

See the full [security and privacy model](../SECURITY.md).
