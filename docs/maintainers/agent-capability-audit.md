# Unused agent capabilities

Historical snapshot observed 2026-09-07, before the session-operations implementation. Claude background creation/lifecycle and Codex forks, native queues, turn controls and structured output have since been implemented; see [current session operations](session-operations.md). The method counts and tables below preserve the pre-implementation evidence.

Observed against the Claude peer worktree. The biggest next opportunities are native Claude session creation, richer Codex turn controls and queues, and adapters for Pi, Hermes and ZCode. This is an implementation inventory and proposed order, not a claim that all discovered interfaces have passed live integration tests.

## Evidence boundary

Agenthail instantiates three adapters: Claude, Codex and Notion (`cmd/agenthail/main.go:50`). I inspected their discovery, send, start, observation and control paths. Current local evidence includes Claude Code 2.1.263 help and extracted JavaScript; Codex CLI 0.153.4 help and its generated experimental JSON schema; Pi 0.64.0 help and RPC declarations; Hermes help and ACP handlers; ZCode 3.11.2's installed bundle. No inference, cloud creation, service restart, installation, permission change or credential redemption was exercised for this audit.

The [complete Codex method inventory](codex-capability-inventory.json) records all **154 client-request methods**, with production-Go literal references for **17**. This is a reproducible index, not a percentage of product capability used. Experimental methods, auth/admin utilities and test methods are included; notifications and server-to-client requests are outside that denominator. Methods without references are candidates for inspection, not automatically useful work.

Reproduce with `codex app-server generate-json-schema --experimental --out .research/codex-schema`. The inventory includes the source hash and installed version. The [official app-server documentation](https://learn.chatgpt.com/docs/app-server) describes the supported integration model; the installed schema is the version-specific contract used here.

## Already connected

| Agent | Agenthail currently consumes | Limits that matter |
|---|---|---|
| Claude Code | Local/bridge discovery, native peer text/replies from this change, transcript reads; bridge-dependent interrupt and model control | Native socket acceptance is not a completed turn; peer policy can hold/deny. No native session creation API in this adapter. |
| Codex | Managed session creation; Desktop/managed discovery; text turns, reply/events, goals, compact, model, interrupt, steer; paginated history and search | Creation is already implemented. Desktop ownership is distinct from the managed daemon. Most optional turn/start fields are unused. |
| Notion | Recent 50 chats, resolve, new/existing inference threads, per-message model choice, persisted replies/observations | No exposed streaming or custom-agent workflow selection; browser-session transport. |

Sources: `internal/surface/surfaces/claude.go:List,Send,Model,Interrupt`, `codex.go:List,startSession,SendWithOptions,GoalSet,Steer`, `codex_thread.go:readThread`, `notion.go:List,SendWithOptions,Capabilities`. The Claude limitations are also tested in `internal/daemon/claude_peers_test.go`.

## Claude: available but unused

| Capability | Current evidence | Agenthail opportunity and verification needed |
|---|---|---|
| Background creation and lifecycle | Installed `claude --help`: `--bg`, `agents`, `attach`, `logs`, `stop`, `rm`, `respawn` | Implement `SessionStarter` for Claude. Capture the returned ID, discover the actual native registry entry, expose starting/failed/exited states. Test stop/resume independently of message completion. |
| Isolated creation, resume, fork | `--worktree`, `--resume`, `--fork-session`, `--agent`, `--agents`, `--name` in installed help | Offer cwd/worktree, model, named-agent and fork options through a shared creation contract. Preserve worktree ownership and permissions. |
| Structured host-controlled runs | `--print`, stream-json input/output, partial messages, hook events, JSON schema, effort and budget options | A separate managed-run transport can give correlated events and structured results. Do not pretend the existing peer socket provides this contract. |
| Idle subscription | Extracted 2.1.263 `chunk-ejjkr3qb.js`, `SendMessageTool`, `notify_when_idle` | Subscribe to readiness without sending a prompt. Requires actual idle control-frame support and disconnect/re-subscription tests. Current helpers do not advertise this feature. |
| File transfer | `chunk-qvnte9zp.js`: `Ean` spools files and hashes content; `Khr` verifies size/hash and materializes attachments | Add typed attachment references, size limits and verified spool cleanup. Current messaging is text only and does not advertise file-transfer support. |
| Peer status/policy feedback | `chunk-ejjkr3qb.js` SendMessage routing and native status controls | Existing change logs receipts. Next expose held/denied/received status as separate structured delivery states, while retaining unknown completion. |
| Remote/cloud addressing and creation | Installed `--cloud`, `--environment`, `--teleport`; extracted `ListAgentsTool` and SendMessage routing | Add distinct remote/cloud session kinds and reachability. Current source explicitly describes cloud peer delivery as one-way; validate account access and reply semantics before offering a reply promise. |
| Agent teams and resumable teammates | `chunk-ejjkr3qb.js` in-process teammate resume and mailbox branches; installed custom-agent flags | Expose existing child/team topology before building orchestration. In-process teammates are not independent PID sockets. |
| Cloud review | Installed `claude ultrareview --help` entry in root help | An explicit review action, with separate job status and cost authorization, rather than a general message. Not exercised. |

Claude's [agent-view documentation](https://code.claude.com/docs/en/agent-view) also documents background session management. Native spawning is a concrete next increment; it does not require reverse-engineering the consumer web chat API.

## Codex: available but unused or partially used

The method names below are in the generated installed schema. The production call paths above consume only a subset of their fields. Presence in schema is not proof that a particular Desktop build, account or host enables the method.

| Area | Installed protocol | Gap/opportunity |
|---|---|---|
| Fork and lifecycle | `thread/fork`, archive/unarchive/delete, name/set, rollback, revert | Fork existing work with lineage; expose non-destructive archive first. Revert and delete need distinct semantics and explicit operator action. |
| Native queued input | `thread/queue/add,list,update,delete,reorder,start` | Agenthail currently owns its own delivery queue. Surface Codex's actual pending input and avoid two independently authoritative queues. Map accepted/enqueued/started/completed explicitly. |
| Turn configuration | `turn/start`: effort, collaborationMode, multiAgentMode, outputSchema, serviceTier, permissions, additionalContext, clientUserMessageId | Add typed options through CLI, persistence, API and UI. Effort/plan mode and structured output are high-value early additions. Do not silently change permission policy. |
| Persistent settings | `thread/settings/update`, `turn/settings/update`, memoryMode/set | Explicit session settings instead of relying on model-only resume. Scope thread defaults versus one-turn overrides. |
| Rich input | `turn/start.input`, thread/start dynamicTools and selectedCapabilityRoots | Typed images and other supported inputs, selected skills/apps, host tool dispatch. Inspect each input variant and server request before declaring support. |
| Review | `review/start` | Typed code-review action, target and result tracking. Ordinary sending is not a replacement for native review mode. |
| Usage and model availability | `model/list`, `modelProvider/capabilities/read`, `account/usage/read`, `account/rateLimits/read`, permissionProfile/list | Model enumeration already exists; provider capability/usage/rate-limit visibility does not. Add read-only inspection before any automatic routing. Credit consumption is a separate explicitly authorized action. |
| Tools and integrations | skills/list, hooks/list, plugin/app catalogs, MCP status/resources/tools/event streams | Show which tools the target can actually use. Auth, install and tool execution are separate actions; do not infer access from a catalog entry. |
| Projects and organization | project/*, threadSection/*, section/move, metadata/update | Keep native project and task organization visible. Agenthail aliases are not native task renames. |
| Better observation | thread/timeline/list, searchOccurrences, diagnostics, server requests/notifications | Build structured progress and approval/elicitation handling. Preserve ownership and completion IDs; do not count a tool event as a completed turn. |
| Terminal/file operations | command/exec*, process/*, fs/*, backgroundTerminals/* | Native terminal inspection, process lifecycle and file watch support. This is an explicit operational surface, not implicit model permission. |
| Remote execution | environment/*, remoteControl/*, standalone exec-server | Multi-host discovery and routing. Keep Desktop, managed and remote owners distinct; the existing transport ownership guard must remain. |
| Realtime | thread/realtime/start, appendAudio/Text/Speech, listVoices, stop | Potential voice control, but requires audio lifecycle, permissions and UI. Lower priority for a text session broker. |
| Configuration/admin | config/*, externalAgentConfig/*, account/login*, marketplace/* | Useful diagnostics and deliberate setup. Not a reason to give routine delivery broad account mutation authority. |

The full inventory includes remaining methods such as memory reset, feedback and experimental test entries; these are intentionally not promoted as product priorities. Codex desktop app tools visible in this working environment additionally include task handoff, recurring automations, sharing, sidebar organization and usage inspection. Those are app-level integrations, not evidence that the generic app-server provides identical methods. Agenthail has no bridge to those app tools in `cmd/agenthail/main.go`.

## Other installed agents and researched sources

| Source | Freshly inspected evidence | Gap and next step |
|---|---|---|
| Pi | `/opt/homebrew/lib/node_modules/@mariozechner/pi-coding-agent/dist/modes/rpc/rpc-types.d.ts`: prompt, steer, follow_up, abort, new/switch/fork session, get_state/messages/stats, model/thinking, compact, export, commands | No Pi adapter. Launch the user's chosen Pi executable in `--mode rpc`; keep its extension/provider configuration. Default `pi` reports 0.64.0 and extension import errors for missing `@earendil-works/pi-ai`. Resolve the chosen installation before integration. |
| Hermes | `hermes acp --help`, `hermes serve --help`; installed `acp_adapter/server.py` new/load/fork/list/prompt/cancel/model/mode handlers | No Hermes adapter. ACP or the authenticated JSON-RPC/WebSocket backend are credible entry points. Choose one owner; session history alone does not establish writable control. |
| ZCode | Installed 3.11.2 bundle `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs` contains session create/list/read/send/fork/goal/compact/stop/usage/subagents/subscription and workspace methods | No ZCode adapter. Method presence is confirmed; current wire framing and existing-session ownership still need executable probes. Do not carry forward the older research's framing or quota claims as current fact. |
| Claude Desktop / Cowork | Installed app 1.46388.4; repository research targets older 1.20186.1 | Research candidates: desktop-managed Code sessions, Cowork schedules, artifacts, remote devices. Current frontend/API contract was not re-extracted or tested; keep these unverified. Native Code sockets do not imply access to all Cowork/web chats. |
| ChatGPT / Codex Desktop | Installed ChatGPT.app 26.901.41123; older `.research/chatgpt-app` targets 26.707 | Agenthail's Codex adapter is present. General ChatGPT conversations, app-level automation/handoff and other consumer features need their own contracts; do not treat older CDP notes as current transport proof. |
| Notion custom agents | Current `notion.go:SendWithOptions` explicitly sets `isCustomAgent:false`, `enableCustomAgents:false`; prior `.research/notion-surface.md` records workflow IDs and custom-agent config | Confirmed omission in Agenthail; current remote support for those old fields is unverified. Refresh against current frontend/request schemas before implementing workflow selection, integrations or schedules. |

The older Pi research describes an experimental 0.84.2 CBOR remote-session stack. The default binary inspected here is a different 0.64.0 installation with JSON-lines RPC. That mismatch is evidence to choose and verify a specific Pi setup, not to implement from the research version by assumption.

## Suggested order

1. **Claude native creation and lifecycle.** Reuse `surface.SessionStarter`; add model/effort/cwd/name/worktree options, then test create → discover → message → observe → stop/resume on an explicitly authorized disposable session.
2. **Codex typed options and native state.** Start with effort/plan mode, structured output, fork and native pending-input visibility. Trace options through durable retries and all operator surfaces.
3. **Capability discovery per session.** Replace overly broad surface booleans with observed transport/owner capabilities and explicit unavailable reasons. Include tools, models and read-only usage diagnostics.
4. **Pi and Hermes adapters.** Verify the selected installation and ownership, then implement the common discover/send/observe contract. ZCode follows after a current transport probe.
5. **Claude attachments and idle notifications.** Extend the peer protocol only when receipt and advertised-feature behavior are verified.
6. **Remote/cloud and consumer-app expansion.** Separate adapters and explicit source freshness checks for Cowork, general chats and Notion custom agents.

This ordering is an engineering recommendation based on the inspected interfaces and current Agenthail boundaries. None of these follow-on implementations were added to the Claude peer change.
