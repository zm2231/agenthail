import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const elements = new Map();
const element = (selector) => {
  if (!elements.has(selector)) elements.set(selector, { innerHTML: "", value: "" });
  return elements.get(selector);
};
const minutesAgo = (minutes) => new Date(Date.now() - minutes * 60e3).toISOString();
const holds = [
  { sessionId: "s-old", session: "@old", root: "/home/me/.claude", contextTokens: 210000, reason: "a background task is running", at: minutesAgo(90) },
  ...Array.from({ length: 6 }, (_, i) => ({ sessionId: `s-${i}`, session: `@work-${i}`, root: "/home/me/.claude", contextTokens: 250000, reason: "the session is busy, not idle", at: minutesAgo(10 - i) })),
  { sessionId: "s-other", session: "@other", root: "/home/me/.work/.claude", contextTokens: 300000, reason: "the account under this root is at a usage limit", at: minutesAgo(3) },
];
const context = {
  app: {},
  $: element,
  escape: (value) => String(value ?? "").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;"),
  fetch: async () => ({
    ok: true,
    json: async () => ({
      roots: [
        { root: "/home/me/.claude", settingsPath: "/home/me/.claude/settings.json", agenthailIdleCompaction: { enabled: true, minContextTokens: 200000 } },
        { root: "/home/me/.work/.claude", settingsPath: "/home/me/.work/.claude/settings.json", agenthailIdleCompaction: { enabled: true, minContextTokens: 200000 } },
      ],
      idleCompactionHolds: holds,
    }),
  }),
};
await dashboardModules(["claude-settings"], context);
await context.loadClaudeCompact();

const [primary, work] = element("#claude-compact-roots").innerHTML.split("</article>");
const shown = [...primary.matchAll(/Not compacted: (@[\w-]+)/g)].map((match) => match[1]);
assert.deepEqual(shown, ["@work-5", "@work-4", "@work-3", "@work-2", "@work-1"], "a root shows its five latest holds, newest first");
assert(primary.includes("at 250,000 tokens") && primary.includes("the session is busy, not idle"), "a hold names its size and reason");
assert(!primary.includes("@other") && work.includes("Not compacted: @other") && work.includes("usage limit"), "each hold shows under its own root");
console.log("dashboard compact holds: ok");
