# agenthail

<p align="center">
  <img src="docs/brand/logo-512.png" alt="agenthail" width="96" height="96" />
</p>

**Your agents are already working. Agenthail helps them work together.**

[![CI](https://github.com/zm2231/agenthail/actions/workflows/ci.yml/badge.svg)](https://github.com/zm2231/agenthail/actions/workflows/ci.yml) ![macOS](https://img.shields.io/badge/macOS-black) ![License](https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue)

Agenthail connects the Claude Code, Codex, and Notion conversations you already use. It gives them one shared place to pass work, keep going, and stay reachable when you leave your desk.

![Agenthail showing active agents, conversations, and handoffs](docs/brand/agenthail-dashboard-demo.gif)

## The problem

Your work is split across agents that cannot see each other.

Claude Code is investigating in one window. Codex is building in another. A Notion thread holds the research. Each one knows its own assignment, but none of them knows what happened next door.

You become the connection layer. You read one answer, decide who needs it, switch windows, paste it over, and remember which agent is still working. When you step away, that coordination stops.

Agenthail connects those pieces. It gives you one view of what is running, a way to reach every conversation, and handoffs that keep moving without you copying messages around.

## Install

Agenthail supports Apple silicon Macs and, as a headless daemon, [Linux](#linux). Download the latest `Agenthail-*-arm64.pkg` from [GitHub Releases](https://github.com/zm2231/agenthail/releases), open it, then run:

```bash
agenthail doctor
```

The package installs the Mac app, menu bar item, and command line tool. Agenthail opens automatically and stays ready in the background.

Homebrew is also available:

```bash
brew install zm2231/tap/agenthail
brew services start agenthail
```

To update:

```bash
agenthail update --check
agenthail update
```

To remove a package installation, run `sudo agenthail-uninstall`. Your local conversations, queue, and history are preserved unless you add `--purge-data`.

### Linux

Agenthail runs on Linux (amd64 and arm64) as a headless daemon for Claude Code and Codex on remote machines. It needs systemd and installs in your home directory without root:

```bash
tag=$(curl -fsSL https://api.github.com/repos/zm2231/agenthail/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p') && arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && name=agenthail-$tag-linux-$arch && cd "$(mktemp -d)" && curl -fsSLO "https://github.com/zm2231/agenthail/releases/download/$tag/$name.tar.gz" -O "https://github.com/zm2231/agenthail/releases/download/$tag/$name.tar.gz.sha256" && sha256sum -c "$name.tar.gz.sha256" && tar -xzf "$name.tar.gz" && test ! -e ~/.local/share/agenthail && mkdir -p ~/.local/share ~/.local/bin && mv "$name" ~/.local/share/agenthail && ln -sfn ~/.local/share/agenthail/agenthail ~/.local/bin/agenthail
agenthail daemon install
agenthail doctor
```

`daemon install` starts a systemd user service, `agenthail.service`, and turns on lingering so the daemon keeps running after you log out. If it prints `linger: off`, run `sudo loginctl enable-linger $USER`; until then systemd stops the daemon at logout and `agenthail doctor` reports it as unhealthy. Restarting or updating the daemon leaves the agents it started running. `agenthail update` installs new releases in place and restores the previous version if the new daemon does not answer.

Run `daemon install` from the shell your agents use: the service records that shell's `PATH`, `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. `CODEX_HOME` must be on a filesystem that can run programs, because Codex's app-server daemon installs and runs its own binary there; `agenthail doctor` flags one that cannot. Claude Remote Control and Notion need a signed-in desktop browser and helper components that the standard Linux download does not include, so a standard Linux install leaves them out; an install that supplies those components can turn Notion on.

Developing Agenthail itself? See the [maintainer documentation](docs/maintainers/).

## Connect your apps

Each app connects independently. If you do not use Notion, it simply stays out of the way. If one app needs attention, the others keep working.

### Claude Code

Agenthail discovers open Claude Code sessions through their local messaging sockets. Native messages require the Agenthail daemon and do not need browser cookies or Remote Control. Sessions that expose only a Remote Control bridge use that transport; enable it with `/rc` in Claude when needed.

A Claude session ID is the conversation ID, the name Claude gives the transcript (`projects/<project>/<session-id>.jsonl`). The process, its messaging socket and its Remote Control session are attachments that come and go; aliases, queues and routes stay with the conversation. `/branch` and `/clear` start a new conversation and therefore a new session with no alias; a branch records the session it was forked from as `forkedFrom`. Only `agenthail identify` moves an alias.

The daemon automatically registers up to 112 agents as individual Claude peers, busy agents first and then those most recently active within the last 24 hours, refreshing every 30 seconds. They appear in Claude's `ListAgents` as `agenthail/<surface>: <name>`. Claude can reply to them using native `SendMessage`; Agenthail puts those messages in its durable queue. Any other agent registers automatically on its next outbound send; a send never waits for a free peer slot. Peers outside the registered set retire 24 hours after their last send. Read-only agents can be discoverable, but inbound messages to them are denied and recorded in history.

Use `--from @alias` or `--from surface:session-id` to identify the sender. Without an explicit `--from`, Agenthail resolves the sender in this order: `AGENTHAIL_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`, then `CODEX_THREAD_ID`, read when the command runs. Claude Code sets `CLAUDE_CODE_SESSION_ID` in every command it runs, so a command run after `/branch` or `/resume` speaks for the conversation that is running now. Agents that Agenthail starts never inherit these variables from the process that started them. Without any of these, messages use an operator peer whose replies are kept in history.

Daemon restart, package replacement and an earlier crash do not require manual socket cleanup. Agenthail reconciles only artifacts it can prove belong to its peer workers, preserves foreign or ambiguous sockets, and resolves the target Claude process and socket again for every delivery.

```bash
agenthail send claude:<session-id> "Here are the findings" --from @builder --json
agenthail history @builder 25
```

A socket acknowledgement means transport acceptance, not model completion. Claude's inbound policy can still hold or deny the message. Agenthail does not invent a permission-mode attestation. Native sends do not support `--reply`, `--stream`, or slash commands; read the transcript with `last` or receive a native `SendMessage` reply. Compact, model, interrupt, and steer are typed controls. They require the target session to have a live Remote Control attachment and are never placed in the ordinary message queue.

A message you type in the web dashboard or the Mac or iPhone app goes to a Claude session's Remote Control attachment, so Claude records it as your own turn rather than a peer message. The apps mark it with `"author": "person"` on the `send` action. Sessions without Remote Control receive it over the native socket. A send that does not declare an author, including any script calling the API, and all agent, relay, channel, CLI and voice sends, use the native socket whenever the session has one. Sessions that expose only Remote Control receive every send through it, as before. A steer you send from the composer is also your own turn. A composer message to a busy session follows your busy-delivery setting, and a queued one is still delivered as your turn.

### Codex

Agenthail drives Codex conversations through Codex Desktop when it is open and through Codex's own app-server daemon when it is not, so the same commands work on a machine without Codex Desktop:

```bash
agenthail thread create codex "Draft the release notes" --alias notes --json
agenthail send @notes "Make it shorter"
```

The dashboard and the Mac app create conversations the same way. When a conversation needs the daemon and it is not running, Agenthail starts it with `codex app-server daemon start`. Codex 0.163 and later install what the daemon needs on first start; older versions ask for the standalone runtime (`curl -fsSL https://chatgpt.com/codex/install.sh | sh`), which Agenthail reports and never installs itself. Turns run inside Codex, so they keep going when Agenthail restarts.

A conversation created in Codex Desktop stays with Desktop. Start Desktop with `agenthail launch codex`; Agenthail then uses a loopback-only bridge to the app-server that already owns those conversations. If Codex is already open, quit it and run that command before using Desktop message controls. While Desktop is closed, its conversations are read only in Agenthail, and Agenthail never loads them into the daemon, because a loaded thread locks Desktop out for up to 30 minutes.

For an interactive terminal on the daemon, run `agenthail codex` in a project folder.

If a writer lock outlives the process that held it, Agenthail reports an ownership conflict instead of retrying. After confirming no `agenthail codex` terminal is active, run `agenthail codex --repair-managed-runtime`. That restarts only Codex's app-server daemon; it never restarts Codex Desktop.

### Notion

Notion is optional. It works as an external workspace for research, notes, and longer-running threads. Sign into Notion in your default browser and Agenthail finds those threads and can start new ones.

### Browser sign-in

Notion and Claude sessions reached over Remote Control authenticate with browser cookies. By default they use your system default browser and the profile it last used: Local State's last used profile for Chrome, Brave, Arc, Chromium and Edge, the default profile in profiles.ini for Firefox, and Safari's single profile. Nothing else reads cookies.

```bash
agenthail signin list                      # what each consumer reads, and the profiles on this computer
agenthail signin add chrome "Profile 2" --label Work
agenthail signin use notion Work           # pin Notion to that profile
agenthail signin use claude auto           # back to the system default browser
agenthail signin use claude none           # turn Claude Remote Control sign-in off
```

`signin list`, `agenthail doctor`, Settings > Browser Sign-in in the Mac app and the web dashboard, and Settings on iPhone all show the profile each consumer resolves to. If it cannot be read they say why and how to fix it. Safari keeps its cookies in a protected folder, so on macOS reading them needs Full Disk Access for `/Library/Application Support/Agenthail/agenthail` in System Settings > Privacy & Security > Full Disk Access. Supported browsers are Chrome, Brave, Arc, Chromium, Edge, Firefox and Safari.

## One place to check in

The Mac app shows what is working now, what is waiting, and what needs you. Open a conversation to read the transcript, send the next instruction, steer the current turn, stop it, compact it, or change models when that conversation supports it.

The same actions are available from the command line:

```bash
agenthail list
agenthail send @writer "draft the explanation" --reply
agenthail steer @builder "keep the example, cut the setup"
agenthail queue @reviewer "check the final implementation"
agenthail history @writer 25
agenthail search codex "quarterly planning"
```

When an agent is already working, Agenthail queues the next message by default. Set
the daemon/dashboard `busyDelivery` setting to `steer` to steer busy targets that
advertise that capability; unsupported targets remain queued. Use `steer` when you
want an explicit active-turn control.
If Agenthail cannot reach a Codex session before starting a turn, it keeps that message pending and retries safely. A timeout after a turn has started is kept as an explicit unknown outcome instead, so Agenthail never guesses whether to send duplicate work. Messages that still cannot move after one hour expire instead of building up forever. They remain visible in the audit trail.

Agenthail keeps current work fast by using Codex's bounded local state. To find an older Codex conversation, use the dashboard search box or `agenthail search codex <query>`; selected results are saved locally for later use.

## Let agents hand work to each other

Give useful conversations short names:

```bash
agenthail identify claude:test-session investigator
agenthail identify codex:test-session-23 builder
```

Naming the same conversation again replaces its previous name.

Then connect them:

```bash
agenthail relay add @investigator @builder 'FAIL|NO-SHIP|root cause'
agenthail relay add @investigator @builder 'READY' --once
agenthail relay add @investigator @builder --when goal_completed
```

When the investigation finishes with something the builder needs, Agenthail passes it across. If the builder is busy, the handoff waits. You can see every handoff and cancel anything that should not go out. A handoff to a closed Claude Code session resumes it and delivers once.

`turn_finished` (the default) forwards each successful matching completion. `goal_completed` requires a source with goal support, waits for an explicit completed goal and its final turn, and forwards the latest pending completion once per goal. Active, paused, blocked, missing, or unreadable goals keep the route waiting. The regex filters the selected result; it does not determine whether a goal is complete.

New routes begin after existing provider history. Completion events and provider cursors survive daemon restarts; each route advances only when its queue item and receipt commit together. A busy or offline destination keeps its handoff in the delivery queue, subject to the existing one-hour expiry. Active routes remain until explicitly removed; an inactive route whose Claude Code source stays closed for an hour is removed. `relay list --json`, the dashboard, and native handoff controls show the condition, pending event count, and capture/consumption error. Queued means persisted for delivery, not received by the destination. See [relay recovery](docs/maintainers/durable-relays.md).

For a group that needs the same update:

```bash
agenthail channel create launch
agenthail channel add launch @writer
agenthail channel add launch @builder
agenthail channel send launch "The release date moved to Friday"
```

## Start work without opening another window

An agent or script can start a Codex thread or Claude background session directly:

```bash
agenthail thread create codex "Implement the verified fix" --alias builder --json
agenthail thread create claude "Investigate the build failure" --alias investigator --json
```

The session starts in your current folder unless you choose another project with `--cwd`. Claude background sessions support status, logs, stop and resume. Codex supports forks, its native input queue, reasoning effort, plan mode, service tier and structured output. These controls are available in the CLI and web dashboard; the Mac app offers one-click forks, the native queue, effort and plan mode; see [session operations](docs/maintainers/session-operations.md) for commands and retry behavior.

Notion threads can start the same way:

```bash
agenthail send notion:new:launch-notes "Draft the launch notes" --reply
```

## Stay connected from your phone

The iPhone companion is distributed through TestFlight. [Request an iPhone invite privately](mailto:zainmer@protonmail.com?subject=Agenthail%20iPhone%20TestFlight%20access&body=Please%20invite%20this%20Apple%20Account%20email%3A%20). Publishing the `.ipa` as a normal download would not make it installable on an ordinary iPhone, so TestFlight is the supported path while the app is in beta.

After installing the iPhone app, install Tailscale on your Mac and iPhone, sign both into the same account, then open **Operations** in Agenthail on the Mac.

Turn on **Private phone access** and choose **Pair an iPhone**. The iPhone app can then show current work, open conversations, send or steer messages, and notify you when an agent finishes. Your Mac is not opened to the public internet.

Matching voice-enabled builds also offer **Talk to orchestrator**: a real Codex
Voice conversation that can inspect agents, delegate work, and read replies through
one persistent operator. [Voice setup, flow, and current limits](docs/maintainers/codex-voice.md).

## See agents on your other machines

Pair the Agenthail daemon on another machine in your tailnet and its sessions appear in the same list, app and phone. Run `agenthail hosts invite` there and `agenthail hosts add '<link>'` here, then address its sessions as `forge::@builder`. If a machine goes offline, its sessions stay listed as unavailable, and messages to them wait up to an hour for it to return; after that they expire and show in delivery history. [How paired hosts work](docs/maintainers/hosts.md).

## What Agenthail remembers

Agenthail keeps a local record of messages it moved, work still waiting, retries, failures, and automatic handoffs. The Audit view makes it possible to come back hours later and understand what happened.

```bash
agenthail queue list
agenthail queue list --mine
agenthail queue list --cwd /workspace/agenthail
agenthail queue retry 12
agenthail queue rm 12
agenthail history --json
```

Your full transcripts remain in the apps that created them. Agenthail keeps only the local information it needs to coordinate delivery and show the audit trail.

## Privacy

There is no Agenthail account and no telemetry.

Your conversations and history stay on the machine that runs Agenthail. Phone access is private to your Tailscale network. If you enable iPhone notifications, Agenthail sends only the connected app name and a completion or failure status through Apple. It never puts an agent name, conversation title, reply text, or browser sign-in data in a notification.

Read the full [security and privacy model](SECURITY.md).

## Help and deeper documentation

- `agenthail doctor` checks connection and current-session discovery. Each delivery also checks that the target can accept input immediately before a turn starts.
- [Native Mac and iPhone apps](docs/native-apps.md)
- [Security and privacy](SECURITY.md)
- [Maintainer documentation](docs/maintainers/)

## License

Source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE). Personal, research, educational, nonprofit, and other noncommercial use are permitted under its terms. Commercial use needs a separate license: see [COMMERCIAL.md](COMMERCIAL.md) or contact [zainmer@protonmail.com](mailto:zainmer@protonmail.com).
