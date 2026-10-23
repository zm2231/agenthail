# Catalog bounded freshness design

## Scope

This slice makes catalog freshness truthful across successful and failed discovery, exposes the complete freshness contract to the CLI, and moves dashboard catalog filtering and paging into the registry query. It does not change provider launch/discovery ownership, seed behavior, or live provider interaction.

Schema version 11 adds `catalog_sessions.discovery_failures`; this is the separate failure counter coordinated with the unreleased launcher/runtime schema line. Installer checks assert both the version and column.

## Freshness state machine

`catalog_sessions` remains the durable source of the last observed row. A successful discovery upsert clears both discovery-failure state and the successful-omission miss count; a failed provider `List` keeps every row, resets the successful-omission counter, increments the separate discovery-failure counter, and leaves `observed_at` unchanged. The latter remains the last successful row observation; the surface health record carries the last provider-attempt time. The identity-level `unavailableReason` is not reused for provider health, so a git/workspace identity failure is not overwritten by a later provider failure; freshness staleness is driven by discovery failures or successful omission misses, not that identity diagnostic. The projection generation advances for both a changed successful projection and a failed observation, so the generation in the emitted `session.upserted` event is the generation that was actually observed. Every projection write takes the next value of one host clock (`catalog_state.projection_clock`), so a generation is never reused, even by a row deleted and created again: a session and its generation name exactly one projection. A failed observation never deletes or marks a row fresh. The next successful row upsert advances the generation when it clears stale state and emits the fresh row event.

The presence pass relies on that clock. Each pass reads every local row's session fields and generation, but reads a row's projection text only when the row was written after the clock value the previous pass saw, or when the pass holds no decoding of the row at its generation; a changed host epoch reads every projection again. The pass keeps each row's decoded projection and, once it finds a row's queue count, presence, listing, subagent rollups and archive flags already stored, skips encoding that row until its generation or one of those outputs changes.

Failure and recovery events reuse the persisted projection as their row payload, adding the generated freshness object. This preserves the last truthful session identity and runtime data while making stale state visible to subscribers.

## Discovery cost

A discovery pass runs every 30 seconds on the daemon's catalog goroutine, apart from relay observation. It writes a session row only when that row would change: the projection fingerprint, identity, unavailable reason, stale counters, stored session fields, runtime and Claude transcript ownership are compared inside the read transaction first, and an unchanged row commits nothing. Each successful pass then closes the surface in one transaction (`RecordCatalogDiscovery`) that stamps `observed_at` on every listed row that is not stale and, for a complete listing, counts omission misses. `observed_at` therefore stays the last successful row observation without one write transaction per session. An incomplete listing stamps the rows it saw and counts no misses.

Workspace identity is cached per normalized cwd for two minutes. A change to the checkout's `HEAD` or `index` file invalidates the entry sooner, so branch switches, commits and staging refresh on the next pass; a working tree edit that touches neither shows in `dirty` when the entry expires. Failed identity lookups are not cached. Resolving a checkout runs separate `rev-parse` queries for root, common directory and git directory, then the branch and status queries.

## New sessions between discovery passes

A session started outside Agenthail, such as a `claude` or `codex` in a terminal, first shows up as a new file: Claude writes `<config root>/sessions/<pid>.json` when a process starts, and Codex writes a rollout file under `$CODEX_HOME/sessions/YYYY/MM/DD/`. Each surface names those folders (`surface.CatalogWatcher`): every Claude config root's `sessions/`, and Codex's `sessions/` with its newest year, month and day folders. The catalog goroutine follows them (`filewake.FollowDir`, which wakes on a created or moved-in entry and not on writes to an existing one). A new entry runs a discovery pass of that surface alone, so the session reaches the catalog in about one Claude listing rather than at the next 30-second pass, and a new Claude session never waits on a Codex `thread/list`. The folders are followed before the first pass, so a session that starts while it runs is not left for the next one. They are named again after every pass and before the pass a wake starts, so a new day's folder is followed before its first rollout lands. A folder that does not exist yet, such as Codex's `sessions/` before its first thread, is waited for from its parent, such as `$CODEX_HOME`; a new entry there runs a pass only once the folder itself exists. A folder whose parent is missing too is left to the 30-second pass, since the nearest existing folder may be the home folder, and on macOS a followed folder costs a descriptor for each of its entries. Wakes that arrive before the loop takes them collapse into one pass per surface, so many sessions starting at once cost a pass and at most one more. The 30-second pass stays the backstop for anything the platform does not report, or a folder past the watch budget.

## Status between discovery passes

Every second the catalog goroutine stats the local status files of each session the last successful pass listed (`surface.LocalStatusSource`: the Codex transcript; the Claude peer record and transcript). When a file changed, the adapter re-derives status from those files alone, and a status transition publishes a full-row `session.upserted` through the same queue-validated write as discovery. Other field changes wait for discovery. An adapter's `LocalStatus` applies the same rule as its `List`, so a discovery pass never reverts a status the files still support: a Codex row follows the transcript's latest task lifecycle whenever it is known and the provider status otherwise. File stamps are taken before the provider `List`, so a file that changes while a pass runs is re-read on the next tick. A surface whose `List` failed is not followed until it recovers, so the status pass never publishes a stale row as fresh.

## Bounded catalog page

The registry exposes a page request containing scope, project ID, query, offset, limit, and the current-time inputs needed by the existing `dashboardSessionPresence` rules. It reads the host epoch, latest catalog sequence, filtered ordered rows, and page look-ahead in one SQLite transaction. Filtering uses SQL aliases and JSON identity fields; queue presence is a grouped subquery, not a Go-side full-catalog scan. The stable ordering is busy first, then last-active, updated time, and session ID. The query returns at most `limit+1` session rows for a page and reports the total matching count separately.

The daemon builds the existing dashboard envelope from that bounded catalog result. The page cursor continues to carry the host epoch, catalog sequence, filter, and offset; a changed epoch or sequence rejects continuation. Unbounded state reads retain the existing full snapshot path for non-page consumers.

## Consumer coverage

Registry tests cover failed discovery retention/freshness and SQL page bounds, including project/query/current filters and cursor epoch/sequence inputs. Daemon tests exercise the API page against a large disposable catalog and verify no provider calls. CLI tests decode the daemon catalog metadata and assert generation, observed time, stale, and unavailable reason are preserved.

## Verification boundary

Verification is limited to disposable registry/API/CLI fixtures and Go tests. No daemon restart, live provider command, message delivery, install, push, or parent-worktree mutation is part of this slice.
