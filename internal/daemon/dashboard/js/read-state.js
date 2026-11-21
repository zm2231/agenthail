import { app } from "./state.js";
import { action } from "./actions.js";
import { mobileConversationView } from "./navigation.js";

const sent = new Map();
const held = new Set();

export function unread(session) {
  return Boolean(session?.readState?.firstUnread);
}

function lookingAtChat() {
  return document.visibilityState === "visible" && document.hasFocus() && location.hash === "#conversations" && (!mobileConversationView() || app.mobileChatOpen);
}

// Codex takes a conflicting mark only on a fresh action by the person, so a refused or undone mark waits for one.
function markSeen(session, fresh = false) {
  if (fresh) held.delete(session.id);
  if (held.has(session.id) || !unread(session) || sent.get(session.id) === session.readState.revision) return;
  sent.set(session.id, session.readState.revision);
  action("session-read", { revision: session.readState.revision }, crypto.randomUUID(), session.id).catch((error) => {
    if (error.status === 409) held.add(session.id);
  });
}

export function readWhileLooking(fresh = false) {
  if (app.selected && lookingAtChat()) markSeen(app.selected, fresh);
}

export async function markUnread(session) {
  held.add(session.id);
  await action("session-unread", { revision: session.readState.revision }, crypto.randomUUID(), session.id);
}
