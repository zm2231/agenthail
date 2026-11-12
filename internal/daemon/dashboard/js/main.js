import { $ } from "./dom.js";
import { load } from "./dashboard-state.js";
import { renderSessions } from "./sessions.js";
import { bindComposerEvents, resizeComposer } from "./composer.js";
import { showView, syncConversationLayout } from "./navigation.js";
import { startLiveStream, stopLiveStream } from "./session-stream.js";
import { selectSession } from "./session-selection.js";
import { bindVoiceEvents } from "./voice.js";
import { bindConversationEvents } from "./conversation-events.js";
import { bindChatEvents } from "./chat-controls.js";
import { bindOperationEvents } from "./operations.js";
import { bindRequestEvents } from "./requests.js";
import { bindTiles } from "./project-tiles-view.js";
import { bindDecisionCard } from "./decision-card.js";
import { refreshDecisions } from "./inbox.js";
import { bindSessionEvents } from "./session-controls.js";
import { bindStartEvents } from "./start-controls.js";
import { bindSettingsEvents } from "./settings-controls.js";
import { bindPushEvents } from "./browser-push.js";
import { bindClaudeSettingsEvents } from "./claude-settings.js";
import { bindSigninEvents } from "./signin.js";
import { bindDeviceEvents } from "./devices.js";
import { bindAuditEvents } from "./audit.js";
import { bindExtensions } from "./extensions.js";
import { bindProjectEvents } from "./project-dialog.js";
import { bindSessionOperationEvents } from "./session-operations.js";
import { bindTurnSettingEvents } from "./turn-settings.js";
import { start as startUsage } from "./usage.js";
import { toast } from "./feedback.js";
import { app } from "./state.js";
import { startLiveOrder } from "./live-order.js";
import { rereadSoon } from "./order-refresh.js";

bindVoiceEvents();
bindConversationEvents();
bindComposerEvents();
bindChatEvents();
bindOperationEvents();
bindRequestEvents();
bindTiles();
bindExtensions();
bindDecisionCard(() => refreshDecisions());
bindSessionEvents();
bindStartEvents();
bindSettingsEvents();
bindPushEvents();
bindClaudeSettingsEvents();
bindSigninEvents();
bindDeviceEvents();
bindAuditEvents();
bindProjectEvents();
bindSessionOperationEvents();
bindTurnSettingEvents();
startUsage({ document, sessions: () => app.state.sessions, open: (id) => selectSession(id, true), notify: toast });

$("#refresh").addEventListener("click", async () => {
  await load(true);
});

window.addEventListener("resize", () => {
  renderSessions();
  resizeComposer();
  syncConversationLayout();
});

window.addEventListener("hashchange", () => showView(location.hash.slice(1)));
window.addEventListener("focus", () => startLiveStream());

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    rereadSoon(0);
    startLiveStream();
  } else {
    stopLiveStream();
  }
});

showView(location.hash.slice(1) || "overview");
startLiveOrder();

const pushedSession = new URLSearchParams(location.search).get("session");

if (pushedSession) {
  history.replaceState(null, "", "/#conversations");
  load(false, false).then(() => selectSession(pushedSession, true));
} else {
  load();
}

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.addEventListener("message", (event) => {
    if (event.data?.type === "open-session" && event.data.sessionId) load(false, false).then(() => selectSession(event.data.sessionId, true));
  });
  navigator.serviceWorker.startMessages();
}
