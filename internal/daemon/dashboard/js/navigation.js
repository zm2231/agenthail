import { app } from "./state.js";
import { defaultSession } from "./session-scope.js";
import { selectSession } from "./session-selection.js";
import { startLiveStream, stopLiveStream } from "./session-stream.js";
import { loadSettings } from "./settings.js";
import { refreshShownProject } from "./project-pane.js";
import { listOrderScopes, orderReady } from "./session-order.js";
import { ensureSessionOrder } from "./dashboard-state.js";
import { loadExtensions } from "./extensions.js";

export function showView(name) {
  const allowed = ["overview", "conversations", "operations", "extensions"];
  const view = allowed.includes(name) ? name : "overview";
  app.view = view;
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
  if (view === "conversations" && app.hasState && !app.selected && !mobileConversationView() && orderReady(listOrderScopes())) {
    const first = defaultSession();
    if (first) selectSession(first.id);
  }
  ensureSessionOrder();
  syncConversationLayout();
  refreshShownProject();
  if (view === "conversations") startLiveStream();
  else stopLiveStream();
  if (view === "operations" && app.operationsTab === "settings") loadSettings();
  if (view === "extensions") loadExtensions();
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
