import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const merged = (work) => ({ id: "fix", name: "fix", worktree: { path: "/src/.worktrees/fix", branch: "fix/typo", base: "origin/main", state: "settled", settledReason: "merged", ...work } });
const context = vm.createContext({ app: { state: { sessions: [], projects: [] } }, crypto: { randomUUID: () => "key" } });
await dashboardModules(["project-merged"], context);
const run = (name) => vm.runInContext(name, context);
const html = (work) => run("renderMergedWorktree")(merged(work));

assert.equal(run("mergedWorktree")(merged({ cleanup: "pending" })), true);
assert.equal(run("mergedWorktree")(merged({ cleanup: "removed" })), false, "a removed worktree is no longer listed");
assert.equal(run("mergedWorktree")(merged({ settledReason: "reported" })), false, "only merged work is listed");
assert.equal(run("mergedWorktree")({ id: "plain" }), false);

assert.match(html({ cleanup: "pending" }), /<code>fix\/typo<\/code>.*if nothing in them would be lost\..*data-work-clean="fix"[^>]*>Remove<\/button>/s, "Remove says it removes only what loses nothing");

const dirty = html({ cleanup: "kept", cleanupReason: "dirty", hint: { reason: "It has changes that are not committed.", command: "git -C /src/.worktrees/fix status" } });
assert.match(dirty, /It has changes that are not committed\.<\/p><code class="project-worktree-command">git -C \/src\/.worktrees\/fix status<\/code>/, "kept work says why and how to see it");
assert.match(dirty, /data-work-clean="fix"[^>]*>Check again<\/button>/, "kept work is checked again on click");
assert.doesNotMatch(dirty, /Remove anyway/);

const paths = ["local.env", "notes/<draft>.md"];
const ignored = { cleanup: "kept", cleanupReason: "ignored", keptPaths: paths, hint: { reason: "It holds ignored files outside build and package folders.", command: "git -C /src/.worktrees/fix status --ignored" } };
assert.match(html(ignored), /data-work-confirm="fix"[^>]*>Remove anyway…<\/button>/);
assert.doesNotMatch(html(ignored), /Delete and remove/, "nothing is deleted without the confirm step");
context.app.worktreeConfirm = "fix";
const confirm = html(ignored);
assert.match(confirm, /deletes these 2 ignored files with the worktree:<\/p><ul class="project-worktree-paths"><li><code>local\.env<\/code><\/li><li><code>notes\/&lt;draft&gt;\.md<\/code><\/li><\/ul>/, "the confirm step names every file it deletes");
const sent = confirm.match(/data-paths="([^"]*)"/)[1].replaceAll("&quot;", '"').replaceAll("&lt;", "<").replaceAll("&gt;", ">").replaceAll("&amp;", "&");
assert.deepEqual(JSON.parse(sent), paths, "the button sends exactly the files shown");
assert.doesNotMatch(html({ ...ignored, cleanupReason: "ignored-many", hint: { reason: "It holds more ignored files outside build and package folders than can be listed here.", command: "git -C /src/.worktrees/fix status --ignored" } }), /Remove anyway|Delete and remove/, "too many ignored files to name are never removed from here");
context.app.worktreeConfirm = null;

const posts = [];
const post = (answer) => async (wire) => { posts.push(JSON.parse(JSON.stringify(wire))); return answer; };
assert.equal(await run("cleanWorktree")("fix", [], post({ ok: true, worktree: { cleanup: "removed", branch: "fix/typo" } })), "Removed fix/typo.");
assert.equal(await run("cleanWorktree")("fix", paths, post({ ok: true, worktree: { cleanup: "kept", cleanupReason: "ignored", keptPaths: [...paths, "new.env"], hint: ignored.hint } })), "Kept: It holds ignored files outside build and package folders.", "files that changed since the confirm keep the worktree");
assert.deepEqual(posts, [{ action: "work-clean", sessionId: "fix" }, { action: "work-clean", sessionId: "fix", removeIgnored: paths }]);
assert.equal(await run("cleanWorktree")("fix", [], async () => { throw new Error("the computer that holds it is not reachable"); }), "");
assert.match(html({ cleanup: "pending" }), /role="alert">the computer that holds it is not reachable</, "a failed remove says why on the row");

console.log("dashboard project merged ok");
