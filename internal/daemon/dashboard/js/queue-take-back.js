import { action } from "./actions.js";
import { app } from "./state.js";
import { returnToSession } from "./send-status.js";
import { syncComposerAction } from "./composer.js";
import { canKeepAttachments } from "./composer-attachments.js";

export async function takeBackQueued(queueId, sessionId) {
  const queued = app.state?.queue?.find((item) => item.id === queueId);
  if (queued?.attachments?.length && !canKeepAttachments())
    throw Error("This browser can't save the attached files right now, so taking them back could lose them. Free some browser storage and try again.");
  const answer = await action("queue-cancel", { queueId });
  const { message = "", attachments = [] } = answer?.result || {};
  returnToSession(sessionId, message, attachments);
  syncComposerAction();
  return attachments.filter((attachment) => attachment.missing);
}
