import { displayText } from "./session-display.js";

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
  const parts = [displayText(session)];
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
