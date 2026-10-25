import { backgroundWaiting, limitLabel, statusLabel, waitingLabels, waitingPlace, workingLabel } from "./presence.js";
import { timeAgo } from "./format.js";

export const rawDisplayName = (session) =>
  session?.alias
    ? `@${session.alias}`
    : session?.name || session?.id || "Untitled conversation";

export function displayName(session) {
  const raw = rawDisplayName(session);
  const repository = raw.match(/^https?:\/\/[^/]+\/([^/\s]+)\/([^/\s]+)/);
  return repository ? `${repository[1]} / ${repository[2]}` : raw;
}

export function conversationMeta(session, model = "", pendingModel = session.pendingModel || "") {
  const parts = [session.waitingOn ? waitingLabels[waitingPlace(session)] : session.limitResume || session.usageLimit ? limitLabel(session) : backgroundWaiting(session) ? "Waiting on background work" : session.status === "busy" ? workingLabel(session) : statusLabel(session.status), `${session.queueCount || 0} queued`];
  if (session.status !== "busy") parts.push(`last active ${timeAgo(session.lastActive)}`);
  const label = modelLabel(model, pendingModel);
  if (label) parts.push(label);
  const configRoot = session.claudeConfigRoot;
  if (configRoot?.name && (configRoot.name !== "default" || configRoot.signedInAs)) parts.push(configRoot.signedInAs ? `config folder ${configRoot.name} · currently signed in as ${configRoot.signedInAs}` : `config folder ${configRoot.name}`);
  return parts.join(" · ");
}

export function modelLabel(model = "", pendingModel = "") {
  if (!pendingModel || pendingModel === model) return model;
  return model ? `${model} · ${pendingModel} from next turn` : `${pendingModel} from next turn`;
}
