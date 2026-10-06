# Subagents

A subagent is a session that another session spawned. Agenthail groups a root
session and every subagent below it into one family. The root is the family's
single destination; subagents stay individually viewable.

## Producers

Codex subagents are real threads. The app-server `Thread` carries
`parentThreadId`, `agentNickname`, `agentRole`, and the spawn depth in
`source.subAgent.thread_spawn.depth`. It does not carry the family root, so for
a subagent at depth 2 or more Agenthail reads `session_id` from the first line
(`session_meta`) of the rollout at `Thread.path`, once per path. That record
spells the source key `subagent`, not `subAgent`. `thread/list` returns only
interactive sources by default, so subagents enter the catalog through
`thread/loaded/list`; `thread/search` likewise never matches them.

Claude Code subagents are read-only observations. Claude Code writes them
beside the parent transcript: `<project>/<transcript-id>.jsonl` and
`<project>/<transcript-id>/subagents/agent-<agent-id>.jsonl`, with
`agent-<agent-id>.meta.json` holding `agentType`, `description`, `toolUseId`,
and `spawnDepth`. The transcript ID is not the Agenthail session ID for bridge
sessions (`session_...`), so the directory is derived from the session's
transcript path, never from its ID. Each transcript's first identifying record
must name the same transcript ID and agent ID as its path.

A Claude subagent is working while its last assistant record has no terminal
stop reason (`end_turn` or an interruption) and no later user record has
reopened it, and its transcript changed in the last 15 minutes. The time bound
covers subagents killed before writing a terminal record.

## Cost

No request path scans the subagent tree. `claudeSubagentObserver`
(`internal/surface/surfaces/claude_subagents.go`) keeps one cache entry per
subagents directory. Each observation stats the directory and lists it again
only when its mtime changed, then stats each agent transcript and reads only
the bytes appended since the last observation (the first read of a transcript
is bounded to its last 256 KiB). Discovery observes only live Claude sessions.
`agenthail runs` is the one caller that walks every project, and it is a
user-invoked diagnostic.

## Model and API

`surface.Session.Subagent` (`parentId`, `rootId`, `depth`, `nickname`,
`role`) marks a subagent session. The registry persists it in
`sessions.parent_session_id`, `root_session_id`, `subagent_depth`,
`agent_nickname`, and `agent_role`; a later registration without subagent
identity keeps the stored one. `rootId` is empty only when a nested Codex
subagent's rollout could not be read; clients then walk `parentId`.

`surface.Session.Subagents` (`count`, `working`) is the rollup of subagents a
provider observes without listing them as sessions. Claude discovery sets it
for every live session with subagents. Catalog rows (`/api/v1/snapshot`,
`/api/v1/catalog-events`) carry both fields as `subagent` and `subagents`.
`/api/v1/session-metadata` lists a Claude session's subagents in
`claudeSubagents` with `agentType`, `description`, `toolUseId`, `depth`,
`working`, and `lastActive`.

Clients compute a Codex parent's rollup from the subagent rows they hold:
every descendant counts, and a busy descendant counts as working. Counts of
working agents count families: a family is working when its root or any
subagent is working.

## Targets

Name and cwd fragments match only family roots, so a fragment that matches a
root and its subagents resolves to the root. Aliases, exact IDs, and unique ID
prefixes still address a subagent directly.

`@<parent>/<child>[/<child>...]` walks subagents. `<parent>` is any other
target form (alias, ID, ID prefix, name or cwd fragment). Each `<child>`
matches the direct subagents of the previous step by exact ID first, then by
nickname (case-insensitive), then by unique ID prefix. Two siblings that share
a nickname make the step ambiguous, and the error lists their IDs. Claude
subagents are not sessions and cannot be targeted.

```bash
agenthail send @lead/Ada "status?"
agenthail send @lead/Ada/Euclid "status?"
```

## Clients

`agenthail list` prints each subagent below its parent with its nickname, and
`--json` rows carry `subagent` and the computed `subagents` rollup. The list
limit counts families. The Mac sidebar and the iOS session list nest Codex
subagents under their parent, collapsed by default, with the working count
rolled into the parent row; the Mac inspector lists a session's subagents.
A subagent shown outside its family reads as `<Parent name> > <nickname>`.
