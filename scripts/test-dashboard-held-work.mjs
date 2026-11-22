import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = {
  app: { selected: null },
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
  labels: { claude: "Claude Code" },
  rawDisplayName: (session) => session.id,
  displayName: (session) => session.id,
  presenceTone: () => "idle",
  presenceLabel: () => "Ready",
  hostBadge: () => "",
};
await dashboardModules(["presence"], context);
await dashboardModules(["holders", "session-row"], context);

const row = (heldWork) => context.sessionButton({ id: "worker", surface: "claude", heldWork });
assert.doesNotMatch(row(undefined), /session-work/, "a session holding nothing shows no work line");
const logged = row([{ itemId: "app-W7", text: "fix the login", state: "in_progress", projectKey: "repo:github.com/acme/app", log: { type: "decision", text: "kept the <old> token", at: "2026-11-22T01:02:03Z" } }]);
assert.match(logged, /<span class="session-work-id">app-W7<\/span><span class="session-work-log">decision: kept the &#60;old&#62; token<\/span>/, "the row names the item and its newest log line, escaped");
const unlogged = row([{ itemId: "app-W7", text: "fix the login", state: "in_progress", projectKey: "k" }, { itemId: "app-W9", text: "add the test", state: "blocked", projectKey: "k" }]);
assert.match(unlogged, /app-W7 \+1<\/span><span class="session-work-log">fix the login</, "an item with no log shows its title, and more items show as a count");
