# agenthail

I kept ending up with the same problem: Claude was working in one session, Codex in another, and something useful was sitting in Notion. Each agent could do the work. None of them could reach the others, so I was spending half my time copying messages between windows and trying to remember which agent was busy.

agenthail gives those sessions one address book.

You can hail an agent by name, send work to whichever surface it already lives in, queue the next instruction while it is busy, or wire two agents together so one picks up when the other finishes. Claude/Codex/Notion stay where they are. agenthail handles the connection between them.

```bash
agenthail send claude:test-session "check the failing tests" --reply
agenthail send codex:test-session-23 "implement the fix" --reply
agenthail send @reviewer "review what Codex changed"
```

## What this solves

Most agent tooling assumes you are starting a new agent inside its own system. That falls apart once the useful work is already spread across a few real sessions with their own context, permissions, files, and history.

agenthail works with the sessions you already have open:

- Name a Claude, Codex, or Notion session once and reach it as `@writer`, `@reviewer`, or whatever makes sense to you.
- Send a message and wait for the actual new reply, instead of accidentally reading the last thing the agent said.
- Keep working while an agent is active. The next message waits in a durable queue and goes out when the session is idle.
- Use `steer` when the current turn needs guidance now.
- Group agents into a channel and hail the whole team.
- Relay a completed answer into another session, including regex-filtered handoffs like `FAIL|NO-SHIP`.

The part I care about is that the agents start feeling like one system. You keep the Claude session with its project context/the Codex session attached to the app/the Notion thread where the research lives, while agenthail gives you one way to communicate across all three.

## A small example

Say Claude is investigating a bug and Codex is handling implementation.

```bash
# Give the sessions names
agenthail identify claude:test-session investigator
agenthail identify codex:test-session-23 builder

# Send work directly
agenthail send @investigator "find the root cause" --reply

# Hand every completed Claude answer to Codex
agenthail relay add @investigator @builder
agenthail daemon install
```

If Codex is already mid-turn, the relay waits. If you want to change what Codex is doing right now, steer it:

```bash
agenthail steer @builder "focus on the registry path first"
```

You can also keep the handoff narrow:

```bash
agenthail relay add @investigator @builder 'FAIL|NO-SHIP|root cause'
```

The route remembers which completed turn it already delivered, including across restarts. Old answers stay old.

## Install

agenthail currently targets macOS. You need Go, Node.js, Python 3.10 or newer, Chrome, and the desktop/web apps for the surfaces you want to use. The installer selects a supported Python interpreter, installs sidecar dependencies with that exact interpreter, and pins its absolute path into the wrapper and daemon service.

```bash
git clone https://github.com/zm2231/agenthail.git
cd agenthail
./install.sh
agenthail doctor
```

The installer packages the Go binary, Python/Node helpers, and the AgentHail operations skill under `~/.local/share/agenthail`, then puts the `agenthail` wrapper in the first writable standard command directory (`/opt/homebrew/bin`, `/usr/local/bin`, or `~/.local/bin`). Running `./install.sh` again upgrades the installation without nesting dependencies. If the daemon is running, the installer restarts it on the new binary. The packaged skill lives at `~/.local/share/agenthail/skills/agenthail-operations/SKILL.md` for agent setups that load local operator skills.

Release archives produced by `scripts/package-release.sh` contain the binary, so the same installer works without a Go toolchain after extraction. The script requires a clean worktree, embeds the exact revision/build time, and writes a SHA-256 checksum next to the archive.

For queues and relays that survive logouts/reboots:

```bash
agenthail daemon install
agenthail daemon status
```

That installs a launchd service with restart-on-crash. If you prefer to run it only when needed, use `agenthail daemon start` and `agenthail daemon stop`.

## Finding your sessions

```bash
agenthail list
agenthail list --all
agenthail list --all --json
```

For scripts, qualify the surface so there is no guessing:

```text
claude:test-session
codex:test-session-23
notion:3978aba0-0606-80ac-a1ae-00a9eb229fc0
```

For daily use, aliases are nicer:

```bash
agenthail identify claude:test-session writer
agenthail send @writer "give me the short version" --reply
```

Ambiguous names fail and show the candidates. agenthail will ask you to use `surface:target` rather than picking a random session.

## The commands I actually use

```bash
# Send and get the completed reply
agenthail send @writer "draft the explanation" --reply

# Slow/long generations can choose their own wait boundary
agenthail send @writer "produce the full report" --reply --timeout 5m

# Watch a supported response as it arrives
agenthail send @writer "walk through the issue" --stream

# Read without sending
agenthail reply @writer
agenthail last @writer 5 --full

# Busy agent: wait for idle
agenthail queue @writer "after that, tighten the intro"

# Busy agent: affect the turn already running
agenthail steer @writer "keep the example, cut the setup"

# See what is waiting or failed
agenthail queue list
agenthail queue retry 12

# Stop or compact a supported session
agenthail interrupt @writer
agenthail compact @writer
```

Long prompts can go through stdin:

```bash
agenthail send @writer - < prompt.txt
generate-prompt | agenthail queue @writer -
```

When `send` hits an active agent, it queues the message and tells you when `steer` is the better fit. When the daemon is down, the queued message stays in SQLite and the CLI tells you exactly what to start.

Use `agenthail send <target> "message" --no-queue` when the caller requires immediate delivery. If the target is active, the command fails without creating a queue row.

## Channels

Channels are useful when two or three sessions need the same context.

```bash
agenthail channel create launch
agenthail channel add launch @writer
agenthail channel add launch @builder
agenthail channel send launch "new requirement: keep the old API working" --from zain
```

Every member gets a result. A partial failure exits nonzero so a script can catch it; busy members go to the queue.

## What works today

| | Claude | Codex | Notion |
|---|---:|---:|---:|
| Find existing sessions | yes | yes | yes |
| Send and read replies | yes | yes | yes |
| Stream a turn | yes | yes |  |
| Steer / interrupt | yes | yes |  |
| Compact | yes | yes |  |
| Session model switch | yes | yes |  |
| Per-message model |  | yes | yes |
| Goal tracking |  | yes |  |

Claude model switching goes through the session's `/model` command. agenthail waits for Claude's local confirmation, so an unknown model returns an error instead of looking successful.

Notion works with existing threads, including a known thread UUID that has fallen outside the 50 most recent results, and can create a persistent thread directly:

```bash
agenthail send notion:new "Start a research thread" --reply
agenthail send notion:new:launch-notes "Draft the launch notes" --reply
```

The delivery receipt returns the persisted thread UUID, which AgentHail registers for replies and follow-up messages. In `new:launch-notes`, `launch-notes` becomes a durable local AgentHail alias; Notion still generates the visible thread title from the first message.

## A few useful guarantees

Queues preserve the full message and its model choice. Delivery stays ordered per session. A rejected/busy delivery remains queued, known pre-dispatch failures retry with a bounded backoff, and repeated failures become visible dead letters instead of disappearing. If the daemon dies—or an external response fails after dispatch—in the window where a message may have reached the agent but was not acknowledged locally, agenthail marks the outcome unknown instead of silently sending the instruction twice. You decide whether to retry it.

Replies are tied to a new completed turn. Streams are tied to the session/turn that started them, so an old Codex event from another thread cannot leak into the output. Relay delivery is recorded before the next poll and survives daemon restarts.

The sidecar reads fresh Chrome cookies without printing them. If the cookie bridge fails, the request stops there. Upstream responses are read in chunks with a 16 MiB default ceiling, and long Claude transcript records get an explicit error at 32 MiB rather than silent truncation.

## Surface setup

Claude and Notion use the Chrome profile where you are already signed in. The default is `Default`:

```bash
AGENTHAIL_CHROME_PROFILE="Profile 2" agenthail doctor
```

Codex needs a local bridge into the Desktop app. Launch it once through AgentHail:

```bash
agenthail launch codex
```

Fresh launches expose only Chromium's renderer debugger on loopback. AgentHail then asks the renderer to use Desktop's own app-server connection, so messages and turns stay visible in the existing app. It does not write to the app-server child process or launch Codex with Node's crash-prone `--inspect` flag.

The renderer bridge uses port `9231` by default. To choose another local port:

```bash
AGENTHAIL_CODEX_REMOTE=9331 agenthail launch codex
AGENTHAIL_CODEX_REMOTE=9331 agenthail doctor
```

If Codex is already open without the renderer bridge, `agenthail launch codex` can activate a delayed compatibility connection. The next clean launch should still go through AgentHail so it can use the safer renderer-only path. `AGENTHAIL_CODEX_INSPECT` remains a deprecated alias for one release cycle.

The remaining environment variables are mostly escape hatches:

| Variable | Use |
|---|---|
| `AGENTHAIL_CHROME_PROFILE` | Chrome profile for Claude/Notion cookies |
| `AGENTHAIL_PYTHON` | Absolute Python 3.10+ interpreter used by the sidecar and installer |
| `AGENTHAIL_CODEX_REMOTE` | Loopback Codex renderer-debugging port, default `9231` |
| `AGENTHAIL_CODEX_INSPECT` | Deprecated alias for `AGENTHAIL_CODEX_REMOTE` |
| `AGENTHAIL_NOTION_SPACE` | Pin a Notion space when auto-detection is ambiguous |
| `AGENTHAIL_NOTION_USER` | Pin a Notion user when auto-detection is ambiguous |
| `AGENTHAIL_NOTION_TZ` | Timezone sent with Notion messages |
| `AGENTHAIL_MAX_RESPONSE_BYTES` | Sidecar response limit, default 16 MiB |
| `AGENTHAIL_DEBUG=1` | Safe sidecar diagnostics without cookie values |

## Build / verify from source

```bash
go test ./... -race -count=1
go vet ./...
scripts/package-release.sh
scripts/test-install-upgrade.sh
bash -n install.sh
python3 -m py_compile sidecar/sidecar.py
```

Runtime state lives in `~/.agenthail` (`registry.db`, daemon lock/PID/log). `agenthail doctor --json`, `list --json`, `send --json`, and the queue commands return stable JSON documents for anything you want to script around.

Claude/Codex/Notion all expose integration surfaces that can change underneath us. `agenthail doctor` is the first command I run after one of those apps updates. Then I try the real thing, because the system only counts as working when one agent can actually reach the next one.
