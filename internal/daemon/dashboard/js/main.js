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
import { bindSessionEvents } from "./session-controls.js";
import { bindStartEvents } from "./start-controls.js";
import { bindSettingsEvents } from "./settings-controls.js";
import { bindPushEvents } from "./browser-push.js";
import { bindClaudeSettingsEvents } from "./claude-settings.js";
import { bindSigninEvents } from "./signin.js";
import { bindDeviceEvents } from "./devices.js";
import { bindAuditEvents } from "./audit.js";
import { bindProjectEvents } from "./project-dialog.js";
import { bindSessionOperationEvents } from "./session-operations.js";
import { start as startUsage } from "./usage.js";
import { toast } from "./feedback.js";
import { app } from "./state.js";

bindVoiceEvents();
bindConversationEvents();
bindComposerEvents();
bindChatEvents();
bindOperationEvents();
bindRequestEvents();
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

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    load();
    startLiveStream();
  } else {
    stopLiveStream();
  }
});

showView(location.hash.slice(1) || "overview");

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

setInterval(() => {
  if (document.visibilityState === "visible") load();
}, 30000);
