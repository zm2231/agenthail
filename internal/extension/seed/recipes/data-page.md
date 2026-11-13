# A page that shows data

For a page that shows data from a file, a command, a local service, an API or
a database the person already has. The page draws at once and each part fills
in as its source is collected, so nothing on the page waits for the slowest.

1. Look at the real source first, read-only. Run the command, open the file or
   request the endpoint, and print a small sample. Redact anything that looks
   like a key, token, password or personal data before it reaches your output.
2. Write down the shape you saw: field names, types, which fields can be
   missing, how big a full answer is. Type the server code from that, never
   from field names you expect the source to have.
3. Create `<id>/extension.json` with a label and only the capabilities you use.
4. In `server.ts`, give each thing that can be slow or fail on its own its own
   source, and return only what the page shows:

   ```ts
   export default function (ah) {
     ah.source({ name: 'disks', everySeconds: 30, collect: async ({ signal }) => {
       const rows = await readDisks({ signal });
       return rows.slice(0, 200);
     } });
     ah.source({ name: 'backups', everySeconds: 300, collect: () => readBackups() });
   }
   ```

   Pick `everySeconds` from how fast the data changes (at least 10). Pass
   `signal` to `fetch` and to child processes, so a run that times out stops.
   Throw when the source fails; the page shows the error beside the last good
   value.
5. Write `ui.html` from this template. Every card is in the HTML, so the page
   has its layout before any data arrives:

   ```html
   <!doctype html>
   <html lang="en">
   <head>
   <meta charset="utf-8">
   <meta name="viewport" content="width=device-width, initial-scale=1">
   <title>Disks</title>
   <style>
     body { font: 14px system-ui, sans-serif; margin: 16px; }
     .card { border: 1px solid #8884; border-radius: 8px; padding: 12px; margin-bottom: 12px; }
     .age { color: #888; font-weight: normal; font-size: 12px; margin-left: 8px; }
     .card[data-state="failed"] .age { color: #c33; }
   </style>
   </head>
   <body>
   <section class="card" id="disks"><h2>Disks<span class="age"></span></h2><div class="body">collecting</div></section>
   <section class="card" id="backups"><h2>Backups<span class="age"></span></h2><div class="body">collecting</div></section>
   <script type="module">
     import { sources, age } from '/x/_ah.js';

     const card = (id, draw) => (state) => {
       const el = document.getElementById(id);
       el.dataset.state = state.state;
       el.querySelector('.age').textContent = age(state);
       if (state.value !== undefined) el.querySelector('.body').replaceChildren(draw(state.value));
     };

     function table(rows, columns) {
       const t = document.createElement('table');
       for (const row of rows) {
         const tr = t.insertRow();
         for (const column of columns) tr.insertCell().textContent = String(row[column] ?? '');
       }
       return t;
     }

     sources({
       disks: card('disks', (rows) => table(rows, ['mount', 'used', 'size'])),
       backups: card('backups', (rows) => table(rows, ['name', 'finished'])),
     });
   </script>
   </body>
   </html>
   ```

   Set text with `textContent`, never `innerHTML` with data from a source.
6. Verify against the same source:

   ```
   agenthail extensions run <id> disks
   agenthail extensions run <id> backups
   ```

   Then open the page and check each card against what you saw in step 1.
