# Daemon performance audit — 2026-11-23

Four reproducible opportunities remain in the audited revision: isolate slow session observations, bound source body caches, batch catalog projection reads, and stop recounting a session's journal on every update. The measurements below characterize existing behavior; no production optimization was implemented.

## Scope and evidence

Audited revision: `b8c2c250b3b35afd20dfe5090fe6ae1b3460abe2`, the locally available `origin/main` at the start of this audit. The shared checkout was at `74e34ffd149bca8d90743a9817e6ad296e3ef4a6`; preliminary measurements there were discarded. No remote refresh or claim about the installed daemon's revision is made.

All experiments used temporary synthetic SQLite databases, fake surfaces, and the real daemon/registry code in `.worktrees/daemon-performance-audit`, branch `perf/daemon-audit`. No live provider calls, daemon restart, installation, push, or PR. One analyst; no other agents. Host: Apple M4 Pro, darwin/arm64, Go 1.26.6. These are local microbenchmarks, not production latency or RSS measurements.

The probes are opt-in with `-tags perfaudit`, because two tests intentionally assert the current undesirable behavior. They should be replaced with regression assertions when the corresponding fixes land. See [daemon probes](../../internal/daemon/performance_audit_test.go), [registry probes](../../internal/registry/performance_audit_test.go), and [raw results](daemon-performance-audit-results.txt).

Final-run medians across three repetitions (raw samples and commands are in the results file):

| Probe | Result |
| --- | --- |
| Next independent session after a 250 ms observation | 252.32 ms |
| Finalized body data retained by one active source | 16 MiB for 4,096 items; journal cap 8 MiB |
| Unchanged catalog, 1 / 100 / 1,000 rows | 0.272 / 28.34 / 284.33 ms per pass |
| Unchanged catalog, 1,000 rows | 31,083,912 B and 462,736 allocations per pass |
| Journal update, 1 / 256 / 2,048 retained rows | 45.29 / 58.54 / 139.69 μs |
| Retention read at 2,048 rows, count / no-count control | 91.887 / 2.318 μs |
| Existing queue count, 50,000 delivered rows | 5.775 μs; candidate extra index 8.318 μs |

Benchmarks ran without the audit's index rebuild or race tests running concurrently. Timing varies between runs; these figures are fixture baselines, not service-level promises.

## 1. Slow observations delay other watched sessions

**Observed:** a fake provider taking 250 ms to observe one session delays the first observation of another independently queued session by approximately 252 ms. There is no route between these sessions. `TestAuditObservationHeadOfLine` reproduces this through `scanAndRelay`, with real registry selection and fake provider I/O.

**Mechanism:** `scanAndRelay` iterates watched sessions serially and invokes `observeInScan` synchronously ([observer.go:52–97](../../internal/daemon/observer.go#L52)). The latter waits for `observed.get` before proceeding ([observer.go:166–179](../../internal/daemon/observer.go#L166)); `get` gives the provider a 20-second context ([relay.go:79–87](../../internal/daemon/relay.go#L79), [observer.go:11](../../internal/daemon/observer.go#L11)). This is a timeout budget, not an observed live 20-second stall; a provider must honor context cancellation for it to bound the wait.

The default observer interval is five seconds ([daemon.go:34](../../internal/daemon/daemon.go#L34)). Failure backoff already exists, but `scanAndRelay` bypasses that skip when the session has pending work ([observer.go:91](../../internal/daemon/observer.go#L91), [daemon.go:305–325](../../internal/daemon/daemon.go#L305)). Backoff therefore does not remove the cross-session delay demonstrated by this queued-work fixture.

**Recommended change:** use a bounded observation scheduler with at most one in-flight observation per session and fairness across providers. Keep mutations, delivery, and completion processing ordered for each session. A shared `relayScan` map and its provider completion readers currently assume serial use ([relay.go:111–124](../../internal/daemon/relay.go#L111)); parallelizing the existing loop wholesale would need to address those assumptions.

**Acceptance:** with multiple slow sessions, a ready session starts within one scheduling turn rather than after their cumulative delay; cancellation drains workers; repeated scans never double-deliver; shared completion readers remain race-free. Catalog discovery/status already have separate loops, so this finding concerns the watched-session observer, not a single global daemon loop ([daemon.go:490–543](../../internal/daemon/daemon.go#L490)).

## 2. Source body retention is independent of journal retention

**Observed:** 4,096 finalized 4 KiB upserts leave **16,777,216 bytes of message text**, 4,096 body-cache entries, and 4,096 cursor entries in one source. The first item has already been pruned from the journal. The configured journal budget is 8 MiB and 2,048 entries. This is retained body length alone; map/string overhead, excess slice capacity, and process RSS are not included. `TestAuditSourceCacheRetention` reproduces it.

**Mechanism:** `append` stores every successfully written body and per-key cursor without an eviction step ([session_source_journal.go:21–80](../../internal/daemon/session_source_journal.go#L21)). `normalizeLocked` feeds both full upserts and accumulated appends into that cache ([session_source_payload.go:48–106](../../internal/daemon/session_source_payload.go#L48)); `streamedBody` retains full `text`, including when a durable body reference exists ([session_source_stream_body.go:13–60](../../internal/daemon/session_source_stream_body.go#L13)). Journal pruning only removes database rows and bodies ([session_journal.go:319–369](../../internal/registry/session_journal.go#L319)).

**Boundary:** this is growth during an active source's lifetime, not a demonstrated leak across completed shutdown. Releasing the final holder cancels the source, and the source removes itself when its run returns ([session_source_subscriptions.go:34–73](../../internal/daemon/session_source_subscriptions.go#L34), [session_source_run.go:33–38](../../internal/daemon/session_source_run.go#L33)). A viewer or other holder can keep it active. The fixture isolates retained source state and does not model user viewing duration.

**Recommended change:** define a bounded mutable-body working set. Keep ongoing append state; release finalized, durably recoverable bodies and age cursor deduplication state against an authoritative replay checkpoint. Rehydrate retained bodies when a provider revisits an old key. Merely deleting entries on `Final` is insufficient without checking late append, replay, seed merging, and pruned-body behavior.

**Acceptance:** retained cache size plateaus under many finalized messages while a source remains held; late updates and replay preserve complete text, UTF-8 boundaries, cursor semantics, and retention-gap reporting. Memory growth is verified here; its effect on live GC/latency remains unmeasured.

## 3. Unchanged catalog publication repeats per-row reads and construction

**Observed:** the benchmark pre-registers 1, 100, or 1,000 offline synthetic sessions, publishes once to warm the catalog, and repeatedly publishes identical projections. It excludes provider/Git work and has no leads, work records, tasks, moves, or PRs. The final 1,000-row median is **284 ms**, allocating approximately **31 MB**. Allocation is cumulative per pass, not retained memory. Exact repeated measurements are in the results file. A separate invariant run checks that these passes emit no new catalog events.

**Mechanism:** for each row, `publishCatalogSession` loads stored projection data, all local lead sessions, agent starter, task membership, registration time, work record, PRs, and a move before constructing JSON twice ([catalog_stream.go:694–763](../../internal/daemon/catalog_stream.go#L694)). The all-leads query inside every row adds an N-by-L traversal when both session count N and lead count L grow ([coordinator_leads.go:10–44](../../internal/registry/coordinator_leads.go#L10)). The empty-lead fixture measures none of that additional row volume.

Full discovery invokes this publication per listed session ([catalog_stream.go:470–510](../../internal/daemon/catalog_stream.go#L470)); it runs on a 30-second ticker and earlier on nudges/file wakes ([daemon.go:35](../../internal/daemon/daemon.go#L35), [daemon.go:504–522](../../internal/daemon/daemon.go#L504)). Do not interpret the result as 31 MB allocated every status tick. The registry already has an unchanged fast path that avoids a write transaction ([catalog_sessions.go:198–218](../../internal/registry/catalog_sessions.go#L198)); the remaining cost includes the reads and construction needed to reach it.

**Recommended change:** load publication metadata once per pass, preferably scoped to the relevant session IDs, and reuse it for row construction. Existing bulk APIs provide useful building blocks: `AgentStarters` ([session_origins.go:99](../../internal/registry/session_origins.go#L99)), `SessionWork` ([work_records.go:137](../../internal/registry/work_records.go#L137)), and `SessionPRs` ([session_prs.go:168](../../internal/registry/session_prs.go#L168)). Avoid replacing small scoped reads with an unbounded load of unrelated history. Consider preparing remaining repeated query shapes after measuring the batch path.

**Acceptance:** compare full passes at 1/100/1,000 sessions, including populated metadata and changing rows. Preserve queue-projection validation, hosted runtime ownership, retirement rules, and waiting-lock ordering. Use versioned/pass-scoped data with revalidation where needed, rather than a long-lived stale cache. No speedup percentage is claimed until this is implemented and benchmarked.

## 4. Every journal update still counts retained rows

**Observed:** updating the same tiny provider row takes approximately **45 μs** with one retained row versus **140 μs** with 2,048 rows, a **3.08×** difference. A second benchmark isolates the retention-read SQL: approximately **92 μs** with the count versus **2.3 μs** for a control that reads the stored byte total without counting rows. The control deliberately does not provide a real count and is not a safe production replacement.

**Mechanism:** `journalRetainedSQL` includes `COUNT(*) WHERE session_id=?` ([journal_statements.go:15](../../internal/registry/journal_statements.go#L15)); `trimSessionJournal` executes it on each changed journal write, even when no pruning is needed ([session_journal.go:319–327](../../internal/registry/session_journal.go#L319)). Prepared statements and transactionally maintained byte totals already exist. The remaining row count still scales with the retained row set.

**Recommended change:** maintain retained row count alongside retained bytes in the same transaction, or establish an equally sound fast path for replacements that cannot increase row count. All inserts, replacements, pruning, journal resets, and migrations must preserve the invariant. The isolated read comparison identifies avoidable work; it is not an end-to-end daemon speedup estimate.

**Acceptance:** insert/upsert/replace/reset/prune sequences maintain exact count and bytes; body-tail writes and replay behavior stay correct; the 1/256/2,048-row update benchmark loses its count-dependent slope. This is a smaller absolute per-call cost than the observation bottleneck, but is on the stream update path.

## Candidates rejected or already addressed

- **Another active-queue index:** the current `message_queue_open` partial index already excludes delivered history ([registry_migrations.go:581](../../internal/registry/registry_migrations.go#L581)). `EXPLAIN QUERY PLAN` and the 0/10,000/50,000-history-row benchmark confirm stable current lookup cost. An additional `(session_id,status)` partial index did not improve this fixture and slowed the single-session count. Do not add it on this evidence.
- **Git work per session on every warm discovery:** `CatalogIdentityCache.Identity` reuses the cached path identity and schedules refreshes for stale entries ([catalog_identity.go:194–216](../../internal/projectresolve/catalog_identity.go#L194)). The old-checkout finding does not describe this revision.
- **A single combined discovery/observation loop:** discovery and status run separately from the observer in the audited revision ([daemon.go:411–415](../../internal/daemon/daemon.go#L411), [daemon.go:490–543](../../internal/daemon/daemon.go#L490)). The narrower observer bottleneck remains.
- **Full byte summation on each journal write:** retained bytes are already maintained transactionally ([journal_statements.go:14](../../internal/registry/journal_statements.go#L14)). The unresolved operation is counting rows.

## Reproduction and review limits

From the audit worktree:

```sh
go test -tags perfaudit ./internal/registry ./internal/daemon -run '^TestAudit' -v -bench '^BenchmarkAudit' -benchtime=300ms -count=3 -benchmem
go test -tags perfaudit -race ./internal/registry ./internal/daemon -run '^(TestAudit|TestSessionJournal|TestSessionSource|TestAReplyStreamedPastTheInlineBodyReadsBackWhole)' -count=1 -timeout=3m
```

The optional catalog profile was collected with `-run '^$' -bench '^BenchmarkAuditCatalogPublish/sessions_1000$' -benchtime=3s -cpuprofile ... -memprofile ...`, then inspected with `go tool pprof -top` and `-alloc_space`. It includes fixture setup and benchmark calibration: it is supporting attribution, not a production CPU breakdown. Allocation stacks point to SQLite query/row materialization and projection construction; batching is a hypothesis to test, not an already measured fix.

The repeated audit run passed in both packages. The selected journal/source tests and audit probes also passed under the race detector: registry 4.791 s, daemon 39.880 s. These checks validate the harness and selected existing behavior, not proposed fixes. Raw verification output is retained with the measurements.

GitNexus was rebuilt for this checkout and used to trace callers and impact. It reports lower-bound coverage for some Go receiver calls; source paths and executable probes establish these findings. Shared journal/body paths received CRITICAL impact classifications, while catalog publication and the observer received LOW with coverage limitations. These classifications are review cues, not defect severity or proof a change is safe. Production functions were not edited. Before implementing concurrency or retention changes, review their consumers and replay/delivery invariants explicitly.

Pre-commit `detect_changes(scope="all")` completed without reported partial/truncated flags, but returned a CRITICAL aggregate classification, including an empty-ID entry for the added test struct. Its separate symbol context identifies only the audit test as a caller. This broad aggregate is retained as a tool limitation, not treated as a clean graph verdict. `git diff --cached --name-only` contains only this report, its results, and the two opt-in test files; normal `go list -json` places both test files in `IgnoredGoFiles`. The index rebuild also warned that full-text search was unavailable; graph queries and source reads were used.

This audit does not cover native UI performance, live provider latency, production database cardinalities, Linux timing, or whole-repository correctness. No whole-package release validation was performed because the deliverable is an opt-in audit harness and this report, not a release.
