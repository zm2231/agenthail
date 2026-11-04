import { $ } from "./dom.js";
import { action, isNetworkFailure } from "./actions.js";
import { app } from "./state.js";
import { returnToSession } from "./send-status.js";
import { resizeComposer, syncComposerAction } from "./composer.js";
import { heldAttachments, holdAttachments, releaseAttachments } from "./composer-attachments.js";
import { selectSession } from "./session-selection.js";

export function canTakeBack(item) {
  return item.status === "pending" && ["", "message"].includes(item.operation || "") && app.state.sessions.some((session) => session.id === item.sessionId);
}

function composerText(sessionId) {
  return app.selected?.id === sessionId ? $("#message").value : app.drafts.get(sessionId) || "";
}

function restore(sessionId, text, added, written) {
  releaseAttachments(sessionId, added);
  if (composerText(sessionId) !== written) return;
  if (app.selected?.id === sessionId) {
    $("#message").value = text;
    resizeComposer();
  }
  app.drafts.set(sessionId, text);
}

export async function takeBackQueued(queueId) {
  const queued = app.state.queue.find((item) => item.id === queueId);
  if (!queued || !canTakeBack(queued)) throw Error("Open this conversation to take back its message.");
  const { sessionId } = queued;
  if (app.selected?.id !== sessionId) await selectSession(sessionId);
  const before = composerText(sessionId);
  const held = new Set(heldAttachments(sessionId).map((attachment) => attachment.id));
  const added = (queued.attachments || []).filter((attachment) => !held.has(attachment.id));
  const saved = returnToSession(sessionId, queued.message, added, { warn: false });
  const written = composerText(sessionId);
  if (!saved) {
    restore(sessionId, before, added, written);
    syncComposerAction();
    throw Error("This browser can't save the message right now, so it stays queued. Free some browser storage and try again.");
  }
  let answer;
  try {
    answer = await action("queue-cancel", { queueId }, crypto.randomUUID(), sessionId);
  } catch (error) {
    syncComposerAction();
    if (isNetworkFailure(error) || error.unknown) throw Error("The take back wasn't confirmed. The message is in the composer and may still be queued too; check the queue before you send it.");
    restore(sessionId, before, added, written);
    syncComposerAction();
    throw error;
  }
  const missing = (answer?.result?.attachments || []).filter((attachment) => attachment.missing);
  if (missing.length) {
    releaseAttachments(sessionId, missing);
    holdAttachments(sessionId, missing);
  }
  syncComposerAction();
  return missing;
}
