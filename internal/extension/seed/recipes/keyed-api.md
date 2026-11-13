# An API that needs a key

For a service that needs a key or token. The key never enters this folder,
the store, a log or a page: Agenthail reads it from where the person keeps it
and adds it to each request.

1. Find out from the person, or from the service's docs, where the key lives:
   a file, an environment variable, the macOS keychain or a command that
   prints it. Don't read or print the key yourself.
2. Look at the API's shape without the key where you can (public docs, an
   endpoint that needs none). If you can only see it with the key, use the
   mount once the person names it (step 5), and print redacted samples only.
3. Declare a mount for the API's origin in `extension.json`:

   ```json
   {"label": "Billing", "mounts": [{"name": "api", "label": "Billing API", "upstream": "https://api.example.com"}]}
   ```

4. Call it from the server with `ah.fetch`. Agenthail adds
   `Authorization: Bearer <key>`:

   ```ts
   export default function (ah) {
     ah.source({ name: 'invoices', everySeconds: 300, collect: async () => {
       const response = await ah.fetch('api', '/v1/invoices?limit=20');
       if (!response.ok) throw new Error(`the API answered ${response.status}`);
       return response.json();
     } });
   }
   ```

   The page shows it with `source('invoices', ...)` from the template in
   `data-page.md`.

   Paths only: no absolute URLs and no `Authorization`, `Cookie` or `Host`
   headers.
5. Tell the person exactly what to run, with the place they told you:

   ```
   agenthail extensions approve <id>
   agenthail extensions key <id> api --key-file ~/path/to/key
   ```

   (`--key-env NAME`, `--keychain-service S --keychain-account A` or
   `--key-command -- <argv>` for the other places.) Don't run these yourself.
6. For an app the page opens through the mount that keeps its key in the
   browser, add `"signin": "assets/signin.js"` to the mount and make the
   script store the placeholder `agenthail-key` where the app looks for its
   key, for example:

   ```js
   if (!localStorage.getItem('apiKey')) localStorage.setItem('apiKey', 'agenthail-key');
   ```

7. Once the person has approved it and named the key, verify:

   ```
   agenthail extensions run <id> invoices
   ```
