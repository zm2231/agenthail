# Agenthail on Mac and iPhone

Agenthail includes a Mac app, a menu bar shortcut, and an optional iPhone companion. The browser dashboard remains available too.

## Mac app

The Mac app opens automatically and keeps your connected conversations in one place.

- **Overview** shows what is working and which apps are connected.
- **Conversations** lets you read, send, steer, stop, compact, and change models when supported.
- **Operations** manages waiting messages, automatic handoffs, history, phone access, and settings.

The menu bar item gives you a quick connection check and opens the full app.

Agenthail updates the app, command line tool, and background service together. If parts of an installation ever fall out of sync, the app asks you to update instead of attempting an unsafe action.

## iPhone app

The iPhone app reaches your Mac privately through Tailscale. It does not make your Mac public on the internet.

To connect it:

1. Install Tailscale on your Mac and iPhone and sign into the same account.
2. Open **Operations** in Agenthail on the Mac.
3. Turn on **Private phone access**.
4. Choose **Pair an iPhone**.
5. Scan the code with the Agenthail iPhone app.

The pairing code expires quickly and works once. You can disconnect a phone from either device.

On iPhone, **Today** shows current work, **Conversations** lets you check in or send the next instruction, and **Settings** manages the connection and notifications.

Agenthail only offers message controls when it knows a conversation is writable. Older Codex history can still be read, but it will not show a composer.

## Notifications

Notifications are optional. Agenthail can tell you when an agent finishes or fails without putting conversation text in the alert. Tapping an alert opens the related conversation.

Turning notifications off removes that phone's notification registration. You can also forget a Mac from the iPhone app if the Mac is no longer available.

## Privacy

Phone access stays inside your Tailscale network. Pairing details are stored securely on each device, and you can revoke a paired phone at any time.

See the full [security and privacy model](../SECURITY.md).
