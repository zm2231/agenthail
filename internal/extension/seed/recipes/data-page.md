# A page that reads a data source

For a page that shows data from a file, a command, a local service or a
database the person already has.

1. Look at the real source first, read-only. Run the command, open the file or
   request the endpoint, and print a small sample. Redact anything that looks
   like a key, token, password or personal data before it reaches your output.
2. Write down the shape you saw: field names, types, which fields can be
   missing, how big a full answer is. Type the server code from that, never
   from field names you expect the source to have.
3. Create `<id>/extension.json` with a label and only the capabilities you use.
   If the person will open the page often, add
   `"app": {"icon": "<SF Symbol name>"}` so the apps offer it an entry of
   its own.
4. In `server.ts`, read the source inside an action and return only what the
   page shows:

   ```ts
   export default function (ah) {
     ah.action('overview', async () => {
       const rows = await readSource();
       return { rows: rows.slice(0, 200) };
     });
   }
   ```

   A source that is slow or large belongs in a job that writes a table (see
   `collector.md`), with the action reading the table.
5. In `ui.html`, call the action with `import { action } from '/x/_ah.js'`
   and render the result. Show the error when the action throws.
6. Verify against the same source:

   ```
   agenthail extensions call <id> overview
   ```

   Read the output and check it against what you saw in step 1.
