# A background collector

For data that should be gathered over time, or a source too slow to read on
every page load: a job writes rows into a table, and the page reads the table.

1. Look at the real source read-only and print a redacted sample. Type the
   rows from the fields you saw.
2. Create the table with `ah.table`, so undoing the extension removes it:

   ```ts
   export default function (ah) {
     const samples = ah.table('samples');
     ah.db.exec(`CREATE TABLE IF NOT EXISTS ${samples} (at INTEGER NOT NULL, value REAL NOT NULL)`);

     ah.job({
       name: 'collect',
       everySeconds: 300,
       async run() {
         const value = await readSource();
         ah.db.prepare(`INSERT INTO ${samples} (at, value) VALUES (?, ?)`).run(Date.now(), value);
         ah.db.prepare(`DELETE FROM ${samples} WHERE at < ?`).run(Date.now() - 7 * 24 * 3600 * 1000);
       },
     });

     ah.action('recent', () => ah.db.prepare(`SELECT at, value FROM ${samples} ORDER BY at DESC LIMIT 500`).all());
   }
   ```

3. Prune in the same job, so the table stays bounded. Pick the window from
   what the page shows.
4. `everySeconds` is at least 60. A run longer than 10 minutes is stopped.
5. Verify the job and the action against the real source:

   ```
   agenthail extensions run <id> collect
   agenthail extensions call <id> recent
   ```

   Check that the rows look like the sample from step 1.
