import { invalidatePresentation } from "./catalog-updates.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, labels } from "./format.js";
import { mobileConversationView } from "./navigation.js";
import { renderChat } from "./chat.js";
import { renderContextUsage } from "./context-usage.js";
import { applyStreamedRow, exchangesFromItems } from "./stream-items.js";
import { selectSession } from "./session-selection.js";

export function stopLiveStream(clear = false) {
  if (app.liveSource) app.liveSource.close();
  app.liveSource = null;
  app.liveSessionID = "";
  if (clear) {
    app.liveText = "";
    app.liveTools = [];
    $("#live-turn")?.remove();
  }
}

export function renderLiveTurn() {
  const body = $("#chat-body");
  if (!body || (!app.liveText && app.liveTools.length === 0)) return;
  const wasPinned = body.scrollHeight - body.scrollTop - body.clientHeight <= 24;
  let turn = $("#live-turn");
  if (!turn) {
    turn = document.createElement("article");
    turn.id = "live-turn";
    turn.className = "turn agent live-turn";
    turn.innerHTML = `<header class="turn-header"><span class="turn-label">${escape(labels[app.selected.surface] || app.selected.surface)} · live</span></header><div class="turn-content live-turn-content"></div><div class="live-tools"></div>`;
    body.append(turn);
  }
  turn.querySelector(".live-turn-content").textContent = app.liveText;
  turn.querySelector(".live-tools").textContent = app.liveTools.length ? `Working with ${app.liveTools.join(", ")}` : "";
  if (wasPinned) body.scrollTop = body.scrollHeight;
}

export function startLiveStream() {
  const session = app.selected;
  const canStream = app.history?.capabilities?.stream;
  const viewingChat = location.hash === "#conversations" && (!mobileConversationView() || app.mobileChatOpen);
  if (!session || !canStream || document.hidden || !viewingChat) {
    stopLiveStream();
    return;
  }
  if (app.liveSource && app.liveSessionID === session.id) return;
  stopLiveStream(true);
  app.liveSessionID = session.id;
  const source = new EventSource(`/api/session-stream?id=${encodeURIComponent(session.id)}&after=${app.history.journalSeq || 0}`);
  app.liveSource = source;
  source.addEventListener("item", (event) => {
    if (app.liveSessionID !== session.id || app.selected?.id !== session.id) return;
    const envelope = JSON.parse(event.data);
    const item = envelope.data;
    if (["status", "waiting", "activity", "state", "metadata", "done"].includes(item.kind)) invalidatePresentation();
    app.history.journalSeq = Math.max(app.history.journalSeq || 0, envelope.seq || 0);
    if (item.kind === "source-error") { app.history.transcriptWarning = item.reason || "Live updates are unavailable."; renderChat(); return; }
    if (item.kind === "context" && item.context) { app.history.context = item.context; renderContextUsage(item.context); return; }
    if (item.kind === "goal") { app.history.goal = item.goal || null; renderChat(); return; }
    if (item.kind === "commands") return;
    if (!item.itemId || item.op === "reset") return;
    app.history.transcriptWarning = "";
    const items = applyStreamedRow(app.history.timeline?.items || [], envelope);
    app.history.timeline = { ...(app.history.timeline || {}), items };
    app.history.exchanges = exchangesFromItems(items);
    renderChat();
  });
  source.addEventListener("error", () => {
    if (source.readyState !== 2 || app.liveSource !== source || app.selected?.id !== session.id) return;
    stopLiveStream();
    selectSession(session.id);
  });
}
