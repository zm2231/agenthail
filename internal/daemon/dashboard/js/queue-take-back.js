import { action } from "./actions.js";
import { returnToComposer } from "./send-status.js";
import { syncComposerAction } from "./composer.js";

export async function takeBackQueued(queueId, sessionId) {
  const answer = await action("queue-cancel", { queueId });
  const { message = "", attachments = [] } = answer?.result || {};
  returnToComposer({ sessionId, text: message, attachments });
  syncComposerAction();
  return attachments.filter((attachment) => attachment.missing);
}
