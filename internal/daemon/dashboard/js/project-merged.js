import { app } from "./state.js";
import { escape } from "./format.js";

export function mergedWorktree(session) {
  const work = session.worktree;
  return Boolean(work && work.state === "settled" && work.settledReason === "merged" && work.cleanup !== "removed");
}

// worktreeOffer is what a merged row lets the person do: remove it, check
// again after fixing what kept it, or delete the ignored files it names.
export function worktreeOffer(work) {
  if (work.cleanup !== "kept") return { kind: "remove" };
  if (work.cleanupReason === "ignored" && work.keptPaths?.length) return { kind: "ignored", paths: work.keptPaths };
  return { kind: "check" };
}

const button = (style, label, attributes) => `<button class="${style}" type="button" ${attributes}>${escape(label)}</button>`;

function why(work) {
  const hint = work.hint;
  if (!hint) return "";
  return `<p class="project-note">${escape(hint.reason)}</p>${hint.command ? `<code class="project-worktree-command">${escape(hint.command)}</code>` : ""}`;
}

function confirmIgnored(id, paths) {
  const names = paths.map((path) => `<li><code>${escape(path)}</code></li>`).join("");
  const count = paths.length === 1 ? "this ignored file" : `these ${paths.length} ignored files`;
  return `<div class="project-worktree-confirm" role="group"><p class="project-note">Remove anyway deletes ${count} with the worktree:</p><ul class="project-worktree-paths">${names}</ul><div class="project-actions">${button("primary-button", "Delete and remove", `data-work-remove-ignored="${id}" data-paths="${escape(JSON.stringify(paths))}"`)}${button("soft-button", "Cancel", `data-work-confirm-cancel="${id}"`)}</div></div>`;
}

export function renderMergedWorktree(session) {
  const work = session.worktree;
  const id = escape(session.id);
  const offer = worktreeOffer(work);
  const failure = app.worktreeFailure?.sessionId === session.id ? `<p class="project-note" role="alert">${escape(app.worktreeFailure.message)}</p>` : "";
  const branch = `<p class="project-worktree-branch"><code>${escape(work.branch || work.path)}</code></p>`;
  if (offer.kind === "remove") {
    return `<div class="project-worktree">${branch}<p class="project-note">Remove deletes its folder and branch if nothing in them would be lost.</p><div class="project-actions">${button("soft-button", "Remove", `data-work-clean="${id}"`)}</div>${failure}</div>`;
  }
  const actions = [button("soft-button", "Check again", `data-work-clean="${id}"`)];
  if (offer.kind === "ignored") actions.push(button("soft-button", "Remove anyway…", `data-work-confirm="${id}"`));
  const confirm = offer.kind === "ignored" && app.worktreeConfirm === session.id ? confirmIgnored(id, offer.paths) : "";
  return `<div class="project-worktree">${branch}${why(work)}${confirm || `<div class="project-actions">${actions.join("")}</div>`}${failure}</div>`;
}

// cleanWorktree asks the daemon to remove a session's worktree. The daemon
// checks again before removing anything, and deletes ignored files only when
// they are exactly removeIgnored; otherwise it keeps the worktree and says why.
export async function cleanWorktree(sessionId, removeIgnored, post) {
  if (app.worktreeCleaning) return "";
  app.worktreeCleaning = true;
  app.worktreeFailure = null;
  app.worktreeConfirm = null;
  try {
    const wire = { action: "work-clean", sessionId };
    if (removeIgnored.length) wire.removeIgnored = removeIgnored;
    const answer = await post(wire, crypto.randomUUID());
    const work = answer?.worktree;
    if (work?.cleanup === "removed") return `Removed ${work.branch || "the worktree"}.`;
    return work?.hint ? `Kept: ${work.hint.reason}` : "Kept.";
  } catch (error) {
    app.worktreeFailure = { sessionId, message: error.message };
    return "";
  } finally {
    app.worktreeCleaning = false;
  }
}
