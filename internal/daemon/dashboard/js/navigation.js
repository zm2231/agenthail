import { app } from "./state.js";
import { viewSessions } from "./session-view.js";
import { selectSession } from "./session-selection.js";
import { startLiveStream, stopLiveStream } from "./session-stream.js";
import { loadSettings } from "./settings.js";

export function showView(name) {
  const allowed = ["overview", "conversations", "operations"];
  const view = allowed.includes(name) ? name : "overview";
  document
    .querySelectorAll("[data-view-panel]")
    .forEach((panel) =>
      panel.classList.toggle("hidden", panel.dataset.viewPanel !== view),
    );
  document
    .querySelectorAll("[data-view]")
    .forEach((link) =>
      link.classList.toggle("active", link.dataset.view === view),
    );
  document.documentElement.toggleAttribute("data-session-open", view === "conversations");
  if (location.hash !== `#${view}`) history.replaceState(null, "", `#${view}`);
  if (view === "conversations" && app.hasState && !app.selected && !mobileConversationView()) {
    const first = viewSessions(app.sessionView)[0];
    if (first) selectSession(first.id);
  }
  syncConversationLayout();
  if (view === "conversations") startLiveStream();
  else stopLiveStream();
  if (view === "operations" && app.operationsTab === "settings") loadSettings();
}

export const mobileConversationView = () => window.matchMedia("(max-width: 760px)").matches;

export function syncConversationLayout() {
  const conversations = document.querySelector('[data-view-panel="conversations"]');
  const active =
    mobileConversationView() &&
    app.mobileChatOpen &&
    conversations &&
    !conversations.classList.contains("hidden");
  document.querySelector(".conversation-layout")?.classList.toggle("mobile-chat-open", active);
  document.body.classList.toggle("mobile-chat-active", active);
}
