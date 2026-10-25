import { app } from "./state.js";
import { $ } from "./dom.js";
import { stopLiveStream } from "./session-stream.js";
import { syncSessionOperationFields } from "./session-operations.js";
import { mobileConversationView, showView, syncConversationLayout } from "./navigation.js";
import { renderSessions } from "./sessions.js";
import { escape, labels } from "./format.js";
import { conversationMeta, displayName } from "./session-labels.js";
import { resizeComposer, syncComposerAction } from "./composer.js";
import { readSessionDetail, acceptSelectedPresentation } from "./session-detail.js";
import { load } from "./dashboard-state.js";
import { renderChat } from "./chat.js";
import { loadSessionMetadata } from "./session-metadata.js";

export async function selectSession(id, focus = false) {
  if (app.selected?.id !== id) { for (const selector of ["#turn-effort", "#turn-mode", "#turn-tier", "#turn-schema"]) $(selector).value = ""; $("#session-operation-result").textContent = ""; $("#session-operation-form").reset(); }
  const request = (app.selectionRequest || 0) + 1;
  app.selectionRequest = request;
  let detail;
  try { detail = await readSessionDetail(id); }
  catch (error) { if (app.selectionRequest === request) $("#chat-body").innerHTML = `<div class="empty-state">${escape(error.message)}</div>`; return; }
  if (app.selectionRequest !== request) return;
  const session = detail.session;
  if (app.selected?.id !== id) stopLiveStream(true);
  if (app.selected) app.drafts.set(app.selected.id, $("#message").value);
  if (!acceptSelectedPresentation(detail)) return;
  syncSessionOperationFields();
  app.history = null;
  app.transcriptSignature = null;
  app.pendingEntryScroll = true;
  app.mobileChatOpen = mobileConversationView();
  renderSessions();
  showView("conversations");
  syncConversationLayout();
  $("#chat-surface").textContent = labels[session.surface] || session.surface;
  $("#chat-title").textContent = displayName(session);
  $("#chat-subtitle").textContent = conversationMeta(session);
  $("#message").disabled = Boolean(session.readOnly);
  $("#message").value = app.drafts.get(session.id) || "";
  resizeComposer();
  $("#send").disabled = Boolean(session.readOnly);
  $("#message").placeholder = session.readOnly ? "Read only" : `Message ${displayName(session)}`;
  $("#composer-note").textContent =
    session.readOnly ? session.readOnlyReason : "Ready to send. If this agent is working, the message waits until it is ready.";
  syncComposerAction();
  $("#chat-body").innerHTML =
    '<div class="empty-state">Loading recent messages…</div>';
  $("#chat-actions").innerHTML = "";
  try {
    app.history = detail;
    renderChat();
    app.allSubagents = false;
    loadSessionMetadata(session.id, detail);
    load();
    if (focus && mobileConversationView())
      requestAnimationFrame(() => window.scrollTo(0, 0));
  } catch (error) {
    $("#chat-body").innerHTML =
      `<div class="empty-state"><h2>Could not load this conversation</h2><p>${escape(error.message)}</p></div>`;
  }
}
