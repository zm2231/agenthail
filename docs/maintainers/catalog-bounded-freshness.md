# Catalog bounded freshness design

## Scope

This slice makes catalog freshness truthful across successful and failed discovery, exposes the complete freshness contract to the CLI, and moves dashboard catalog filtering and paging into the registry query. It does not change provider launch/discovery ownership, seed behavior, or live provider interaction.

Schema version 11 adds `catalog_sessions.discovery_failures`; this is the separate failure counter coordinated with the unreleased launcher/runtime schema line. Installer checks assert both the version and column.

## Freshness state machine

`catalog_sessions` remains the durable source of the last observed row. A successful discovery upsert clears both discovery-failure state and the successful-omission miss count; a failed provider `List` keeps every row, resets the successful-omission counter, increments the separate discovery-failure counter, and leaves `observed_at` unchanged. The latter remains the last successful row observation; the surface health record carries the last provider-attempt time. The identity-level `unavailableReason` is not reused for provider health, so a git/workspace identity failure is not overwritten by a later provider failure; freshness staleness is driven by discovery failures or successful omission misses, not that identity diagnostic. The projection generation advances for both a changed successful projection and a failed observation, so the generation in the emitted `session.upserted` event is the generation that was actually observed. A failed observation never deletes or marks a row fresh. The next successful row upsert advances the generation when it clears stale state and emits the fresh row event.

Failure and recovery events reuse the persisted projection as their row payload, adding the generated freshness object. This preserves the last truthful session identity and runtime data while making stale state visible to subscribers.

## Bounded catalog page

The registry exposes a page request containing scope, project ID, query, offset, limit, and the current-time inputs needed by the existing `dashboardSessionPresence` rules. It reads the host epoch, latest catalog sequence, filtered ordered rows, and page look-ahead in one SQLite transaction. Filtering uses SQL aliases and JSON identity fields; queue presence is a grouped subquery, not a Go-side full-catalog scan. The stable ordering is busy first, then last-active, updated time, and session ID. The query returns at most `limit+1` session rows for a page and reports the total matching count separately.

The daemon builds the existing dashboard envelope from that bounded catalog result. The page cursor continues to carry the host epoch, catalog sequence, filter, and offset; a changed epoch or sequence rejects continuation. Unbounded state reads retain the existing full snapshot path for non-page consumers.

## Consumer coverage

Registry tests cover failed discovery retention/freshness and SQL page bounds, including project/query/current filters and cursor epoch/sequence inputs. Daemon tests exercise the API page against a large disposable catalog and verify no provider calls. CLI tests decode the daemon catalog metadata and assert generation, observed time, stale, and unavailable reason are preserved.

## Verification boundary

Verification is limited to disposable registry/API/CLI fixtures and Go tests. No daemon restart, live provider command, message delivery, install, push, or parent-worktree mutation is part of this slice.
