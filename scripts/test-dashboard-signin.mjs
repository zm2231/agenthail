import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function signinHint(");
const end = source.indexOf("async function signinRequest", start);
assert(start >= 0 && end > start);
const context = {};
vm.runInNewContext(source.slice(start, end) + "\nthis.signinHint = signinHint;", context);
const notion = context.signinHint("Notion: choose a browser sign-in profile; run `agenthail signin use notion <profile>` (see `agenthail signin list`) or open Settings > Browser Sign-in");
assert.match(notion, /^Notion needs a browser sign-in profile\./);
assert(notion.includes("Settings > Browser Sign-in"));
assert(notion.includes("agenthail signin use notion <profile>"));
const claude = context.signinHint("Claude Remote Control: choose a browser sign-in profile; run `agenthail signin use claude <profile>`");
assert.match(claude, /^Claude sessions reached over Remote Control need/);
assert(claude.includes("agenthail signin use claude <profile>"));
console.log("dashboard sign-in hint tests passed");
