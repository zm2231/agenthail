import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = {};
await dashboardModules(["signin", "http"], context);
const notion = context.signinOffHint("Notion: browser sign-in is turned off; to turn it back on, run `agenthail signin use notion auto` or open Settings > Browser Sign-in");
assert.match(notion, /^Browser sign-in is off, so Notion reads no cookies\./);
assert(notion.includes("Settings > Browser Sign-in"));
assert(notion.includes("agenthail signin use notion auto"));
const claude = context.signinOffHint("Claude Remote Control: browser sign-in is turned off");
assert.match(claude, /Claude sessions reached over Remote Control read no cookies/);
assert(claude.includes("agenthail signin use claude auto"));
assert.equal(context.signinResolved({ mode: "automatic", browser: "chrome", browserLabel: "Google Chrome", profile: "Profile 2", profileName: "Work" }), "Automatic: Google Chrome · Profile 2 (Work), the system default browser.");
assert.equal(context.signinResolved({ mode: "automatic", browser: "safari", browserLabel: "Safari", profile: "Default", profileName: "Safari" }), "Automatic: Safari, the system default browser.");
assert.equal(context.signinResolved({ mode: "automatic" }), "Uses the system default browser.");
assert.equal(context.signinResolved({ mode: "profile", browser: "chrome", browserLabel: "Google Chrome", profile: "Profile 2", profileName: "Work Chrome" }), "Work Chrome: Google Chrome · Profile 2.");
assert.equal(context.signinResolved({ mode: "off" }), "Off. Reads no cookies.");
console.log("dashboard sign-in tests passed");
