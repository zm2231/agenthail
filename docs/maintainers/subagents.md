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
and `spawnDepth`. The directory is derived from the session's transcript path,
never from its ID, which also covers a resumed transcript in another project. Each transcript's first identifying record
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
provider observes without listing them as sessions. It counts live state
only: subagents that work or were active within `surface.ActiveWindow`
(24 hours). Claude discovery sets it for every live session with such
subagents. A catalog row's `subagents` adds `liveHiddenSubagents`, the hidden
subagent rows under it that work or are current; `hiddenSubagents` counts
every hidden subagent row, so clients know there are rows to fetch. Catalog rows (`/api/v1/snapshot`,
`/api/v1/catalog-events`) carry both fields as `subagent` and `subagents`.
`/api/v1/session-metadata` lists a Claude session's subagents in
`claudeSubagents` with `agentType`, `description`, `toolUseId`, `depth`,
`working`, `lastActive`, and `transcriptPath`: the working ones first, then the
eight most recently active. `claudeSubagentsTotal` counts every subagent the
session has, and `subagents=all` returns all of them in the same order. A paired
host forwards the query to the host that runs the session. A host from before
this paging returns its whole list and no total; clients then take the list's
length as the total and collapse it themselves.

The rollup's `count` is a snapshot of the subagents active within the last 24
hours as of the session's last read, and an idle session is not read again when
that window moves. Clients therefore show only `working` on session rows; the
inspectors show the total from the metadata they just fetched.

Clients compute a Codex parent's rollup from the subagent rows they hold plus
the row's `subagents`: every listed descendant counts, and a busy descendant
counts as working. Counts of
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

`@lead/<child>[/<child>...]` walks the subagents of the caller's project
coordinator, on whichever host runs it. `expandLead` rewrites it to
`@<coordinator-session-id>/...`, or `<host>::@<id>/...` for a coordinator on a
paired host, so the parent is exact and is never a session that merely has
`lead` in its name. A coordinator without a conversation yet has no
subagents, and the path is refused.

Commands that deliver (`send`, `steer`, `queue`, the destination of
`relay add`, `channel add`) refuse name and cwd fragments, including as the
`<parent>` of a path; see `resolveDeliveryTarget` in `internal/cli`. Reads
and every other command keep fragment matching.

```bash
agenthail send @lead/Ada "status?"
agenthail send @lead/Ada/Euclid "status?"
```

## Clients

`agenthail list` prints each subagent below its parent with its nickname and
names a row's working subagents, and `--json` rows carry `subagent` and the
computed `subagents` rollup. The list limit counts families. The Mac sidebar
and the iOS session list nest Codex subagents under their parent, collapsed by
default, with the working count rolled into the parent row. The Mac and iPhone
inspectors and the web dashboard list a Claude session's subagents by
description (else type, else ID), working ones first, then the recent ones, with
the rest behind Show earlier.
A subagent shown outside its family reads as `<Parent name> > <nickname>`.
