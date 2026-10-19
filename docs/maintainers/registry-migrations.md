# Registry migrations

`registry.Open` brings `~/.agenthail/registry.db` to the current schema in one transaction (`migrate` in `internal/registry/registry.go`). The connection uses `_txlock=immediate`, so the migration holds the write lock from its first statement. `migrateTx` reads `PRAGMA user_version`, runs every step on that transaction, and writes `user_version` once at the end. If any step fails, the database keeps its previous schema and version.

A new database runs the same path with version 0. Because the `schema` constant declares every table and column, a new database needs no `ALTER TABLE`.

## Adding a schema change

1. **Version.** Raise `schemaVersion` to the next unused number N. If another branch takes N on `main` first, merge `main`, move to N+1, and update your gate and tests to match.

2. **New table.** Add `CREATE TABLE IF NOT EXISTS` to the `schema` constant. The constant runs on every open, so existing databases get the table too. Indexes on the new table also go in the constant.

3. **New column on an existing table.** Do both:
   - add the column to that table's `CREATE TABLE` in the `schema` constant;
   - add `ensureColumn(tx, table, column, declaration)` in `migrateTx` with the same declaration.

   The default must be a constant. `ALTER TABLE` cannot add a column whose default is an expression such as `datetime('now')`.

4. **Index on a new column of an existing table.** Put `CREATE INDEX IF NOT EXISTS` in `migrateTx` after that column's `ensureColumn`, without a version gate. Do not put it in the `schema` constant. The constant runs before `ensureColumn`, so on an older database the column does not exist yet and the index would fail.

5. **Data step** (backfill, rewrite, dropping a column). Write a function that takes `tx *sql.Tx`. Call it from `migrateTx` inside `if version < N { ... }`, after any `ensureColumn` it depends on. The step:
   - runs only on `tx`: never `r.db`, never its own `Begin` or `Commit`, never `PRAGMA user_version`;
   - must be correct on a new database, where version 0 runs every gated step against empty tables;
   - should be safe to repeat. For example, `dropQueueExpiry` checks with `columnExists` before it drops anything.

   A step without a gate runs on every open (`recordRuntimeKinds` is one). Use that only when the step is idempotent and cheap.

6. **Version gate.** Gates compare with `version < N`, never equality, so a database several versions behind runs every step it missed, in order. A database stamped newer than `schemaVersion` skips all gates and keeps its version.

## Tests

- `TestSchemaDeclaresEveryColumn` builds a database from the `schema` constant alone and compares its columns with a registry from `Open`. It fails when a column reaches new databases only through `ensureColumn`.
- `TestOpenMigratesHistoricalSchemas` upgrades every fixture in `internal/registry/testdata/migrations` (written by `generate.sh`). `assertSchemaMatchesFreshRegistry` then requires the same columns, declarations and indexes as a new registry.
- Each data step needs a test that seeds a registry at version N-1 with the old shape and rows, opens it, and checks the result. Existing examples are `TestSchema14RekeysClaudeRowsToTheirConversation` with `seedVersion13`, and `TestOpenRetiresQueueExpiryLeftOnARegistryAtTheVersionBefore`.
