# A background collector

For history: data gathered over time, such as a chart of the last day. A job
writes rows into a table and a source reads the table for the page. For the
current value of something, a source alone is enough (see `data-page.md`).

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

     ah.source({ name: 'recent', everySeconds: 60, collect: () => ah.db.prepare(`SELECT at, value FROM ${samples} ORDER BY at DESC LIMIT 500`).all() });
   }
   ```

3. Prune in the same job, so the table stays bounded. Pick the window from
   what the page shows.
4. `everySeconds` is at least 60. A run longer than 10 minutes is stopped.
5. Verify the job and the action against the real source:

   ```
   agenthail extensions run <id> collect
   agenthail extensions run <id> recent
   ```

   Check that the rows look like the sample from step 1.
