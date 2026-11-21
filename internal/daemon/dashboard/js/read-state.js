import { app } from "./state.js";
import { action } from "./actions.js";
import { mobileConversationView } from "./navigation.js";

const dwellMs = 1000;
const sent = new Map();
const held = new Set();
let pending = null;

export function unread(session) {
  return Boolean(session?.readState?.firstUnread);
}

function lookingAtChat() {
  return document.visibilityState === "visible" && document.hasFocus() && location.hash === "#conversations" && (!mobileConversationView() || app.mobileChatOpen);
}

// Codex takes a conflicting mark only on a fresh action by the person, so a refused or undone mark waits for one.
function markSeen(session, fresh) {
  if (fresh) held.delete(session.id);
  if (!session.readState || held.has(session.id) || !(fresh || unread(session)) || sent.get(session.id) === session.readState.revision) return;
  sent.set(session.id, session.readState.revision);
  action("session-read", { revision: session.readState.revision }, crypto.randomUUID(), session.id).catch((error) => {
    if (error.status === 409) held.add(session.id);
  });
}

// The second counts from the open or refocus; a render while it runs keeps its deadline.
export function readWhileLooking(fresh = false) {
  const id = app.selected?.id;
  const looking = Boolean(id) && lookingAtChat();
  if (looking && !fresh && pending?.id === id) return;
  const opened = fresh || Boolean(pending?.fresh && pending.id === id);
  if (pending) clearTimeout(pending.timer);
  pending = null;
  if (!looking) return;
  const timer = setTimeout(() => {
    pending = null;
    if (app.selected?.id === id && lookingAtChat()) markSeen(app.selected, opened);
  }, dwellMs);
  pending = { id, fresh: opened, timer };
}

export async function markUnread(session) {
  held.add(session.id);
  await action("session-unread", { revision: session.readState.revision }, crypto.randomUUID(), session.id);
}
