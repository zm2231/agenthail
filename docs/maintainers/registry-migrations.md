# Registry migrations

`registry.Open` first reads `PRAGMA user_version` and `PRAGMA journal_mode` on a read connection. A database at `schemaVersion` in WAL mode is opened as it is, without the write lock: every CLI command opens the registry, and one that queued for the write lock behind the daemon and other commands could wait past the 15 s `busy_timeout` and fail with `database is locked`. Under contention from 128 processes, each opening and writing 15 times next to a process that holds 40 ms write transactions, opening through the migration failed this way, while the read-only open ran every command. A database at `schemaVersion` therefore has to hold exactly the schema and data a build of that version produces; see step 1 below.

Any other database is brought to the current schema in one transaction (`migrate` in `internal/registry/registry.go`). The connection uses `_txlock=immediate`, so the migration holds the write lock from its first statement. `migrate` reads `user_version` again under the lock and leaves a database another opener has just brought to `schemaVersion` as it is. Otherwise `migrateTx` runs every step on that transaction, and `user_version=schemaVersion` is written once, as the last statement. If any step fails, the database keeps its previous schema and version.

Before migrating a database that is already below `schemaVersion` (version 1 or more), `migrate` copies it to `registry-before-v<N>.db` beside it, where N is the schema it is about to reach. The copy runs under the migration's write lock: `migrate` reads `user_version` on its transaction, then `VACUUM INTO` a mode-0600 temporary file on a separate connection (VACUUM cannot run inside the transaction), checks the copy's `user_version` matches, and renames it into place. A concurrent opener therefore cannot name an already-migrated copy "before". If the copy fails, the migration is refused and the database keeps its schema. Only the two newest copies are kept. On a 344 MB registry the copy is 337 MB and the whole upgrade open takes about 0.4 s on an SSD (about 1.1 s with a cold page cache).

A database stamped newer than `schemaVersion` was migrated by a newer agenthail. `migrate` reads the stamp first, under the write lock, and `Open` returns `*NewerSchemaError` ("registry is schema N; this agenthail supports M; install a newer agenthail") without writing to the file. When `registry-before-v<N>.db` exists beside it with a schema this build supports, the error also gives the steps to go back to it, as shell commands with every path single-quoted: stop the daemon, move the newer registry aside as `registry-v<N>.db`, delete its `-wal` and `-shm`, copy the backup into place, start the daemon. The connection pragmas set before it change nothing on disk; WAL mode, which does, is set only after `migrate` accepts the schema, so a refused registry keeps its journal mode. The CLI and `daemon-run` exit with that error instead of running against tables this build does not know.

The CLI opens the registry only when a command first needs it (`App.registry` in `internal/cli/cli.go`), so `--help`, `version` and argument errors never create or migrate one. `cmd/agenthail` opens the registry in the home directory with `registry.OpenDefault(mayUpgradeRegistry(version))`. A build without a stamped version (`go run` or a plain `go build`, where `main.version` is `dev`) creates a new registry but refuses to migrate an existing one with `*DevUpgradeError`, because the installed agenthail would then refuse the branch schema. Run a branch build with a scratch `HOME`, or set `AGENTHAIL_ALLOW_DEV_MIGRATION=1` to migrate anyway (the copy above is taken first). `registry.Open` with an explicit path, as tests use, always migrates. Release packages and `install.sh` stamp a version (`install.sh` uses `git describe`, or `source` outside a Git checkout), so installed builds migrate.

A new database runs the same path with version 0. Because the `schema` constant declares every table and column, a new database needs no `ALTER TABLE`.

## Adding a schema change

1. **Version.** Every change to the `schema` constant or to `migrateTx`, including a column, an index, a trigger or a data step, raises `schemaVersion` to the next unused number N. A database already at a version is never migrated again, so a change shipped without a new version never reaches it. If another branch takes N on `main` first, merge `main`, move to N+1, and update your gate and tests to match. Then regenerate `internal/registry/testdata/schema.sql` with `AGENTHAIL_UPDATE_SCHEMA=1 go test ./internal/registry -run TestTheSchemaChangesOnlyWithItsVersion`; never edit it by hand.

2. **New table.** Add `CREATE TABLE IF NOT EXISTS` to the `schema` constant. The constant runs on every migration, so existing databases get the table too. Indexes on the new table also go in the constant.

3. **New column on an existing table.** Do both:
   - add the column to that table's `CREATE TABLE` in the `schema` constant;
   - add `ensureColumn(tx, table, column, declaration)` in `migrateTx` with the same declaration.

   The default must be a constant. `ALTER TABLE` cannot add a column whose default is an expression such as `datetime('now')`.

4. **Index on a new column of an existing table.** Put `CREATE INDEX IF NOT EXISTS` in `migrateTx` after that column's `ensureColumn`, without a version gate, so a database several versions behind gets it too. Do not put it in the `schema` constant. The constant runs before `ensureColumn`, so on an older database the column does not exist yet and the index would fail. If the index depends on values a data step fills in (a unique index over a backfilled column, for example), put it after that step: before the backfill, existing rows all hold the column's constant default and a unique index would fail.

5. **Data step** (backfill, rewrite, dropping a column). Write a function that takes `tx *sql.Tx`. Call it from `migrateTx` inside `if version < N { ... }`, after any `ensureColumn` it depends on. The step:
   - runs only on `tx`: never `r.db`, never its own `Begin` or `Commit`, never `PRAGMA user_version`;
   - must be correct on a new database, where version 0 runs every gated step against empty tables;
   - should be safe to repeat. For example, `dropQueueExpiry` checks with `columnExists` before it drops anything.

   A step without a gate runs on every migration (`recordRuntimeKinds` is one). Use that only when the step is idempotent and cheap. No step runs on an open of a current database, so an invariant the running code needs must be kept by the code that writes the rows, not repaired by a later open.

6. **Version gate.** Gates compare with `version < N`, never equality, so a database several versions behind runs every step it missed, in order. A database stamped newer than `schemaVersion` never reaches the gates: `Open` refuses it first.

## Tests

- `TestTheSchemaChangesOnlyWithItsVersion` compares the schema of a new registry with `testdata/schema.sql`, which records it for one `schemaVersion`. It fails when the schema changes without a new version, and when the version changes without regenerating the file.
- `TestOpeningACurrentRegistryWaitsForNoWriter` holds the write lock on another connection and requires `Open` of a current registry to return without waiting for it.
- `TestSchemaDeclaresEveryColumn` builds a database from the `schema` constant alone and compares its columns with a registry from `Open`. It fails when a column reaches new databases only through `ensureColumn`.
- `TestOpenRefusesARegistryANewerAgenthailMigrated` stamps a registry past `schemaVersion`, in WAL and in rollback-journal mode, and requires `Open` to return `*NewerSchemaError` and leave the database and its journals byte for byte unchanged. `TestOpenWaitingOnANewerAgenthailsMigrationRefusesItsSchema` has a newer build stamp the registry while `Open` waits on its lock.
- `TestAMigrationKeepsACopyOfTheRegistryFromBeforeIt`, `TestARegistryThatCannotBeBackedUpIsNotMigrated`, `TestANewerSchemaRefusalNamesTheCopyFromBeforeItAndHowToRestoreIt` (which runs the printed restore steps through `/bin/sh` in a directory whose name has spaces, quotes, `$` and a backslash) and `TestADevelopmentBuildCreatesButDoesNotUpgradeTheDefaultRegistry` cover the copy, its refusal and the development-build guard.
- `TestOpenMigratesHistoricalSchemas` upgrades every fixture in `internal/registry/testdata/migrations` (written by `generate.sh`). `assertSchemaMatchesFreshRegistry` then requires the same columns, declarations and indexes as a new registry.
- Each data step needs a test that seeds a registry at version N-1 with the old shape and rows, opens it, and checks the result. Existing examples are `TestSchema14RekeysClaudeRowsToTheirConversation` with `seedVersion13`, and `TestOpenRetiresQueueExpiryLeftOnARegistryAtTheVersionBefore`.
