# Putting an existing web app in the dashboard

For a web app that already runs, such as a service's admin console, that the
person wants on a dashboard tab.

1. Find the app's origin (`http://host:port`) and the page to open first.
   Request it read-only and check that it answers. Print only the status and
   the page title, not the body or headers.
2. Add a mount to `<id>/extension.json`:

   ```json
   {
     "label": "Proxy",
     "mounts": [{"name": "console", "label": "Proxy console", "upstream": "http://127.0.0.1:8317", "entry": "/management.html"}]
   }
   ```

   `upstream` is an origin with no path. `entry` is the path the dashboard
   opens. Add `"app": {"icon": "<SF Symbol name>"}` to the mount if the
   person will open it often, so the apps offer it an entry of its own.
3. An extension needs `server.ts` even if it does nothing:

   ```ts
   export default function () {}
   ```

4. The mount serves nothing until the person approves it. Tell them:
   "Approve <id> on the Extensions tab, or run `agenthail extensions approve
   <id>`: it mounts <upstream>." Don't approve it yourself.
5. If the app needs a key, follow `keyed-api.md` for the signin script and ask
   the person to name the key.
6. Once approved, `agenthail extensions list` shows the mount's port and
   state. Request `http://127.0.0.1:<port><entry>` to check it serves; it
   needs the dashboard login, so a 401 means the mount is up.
