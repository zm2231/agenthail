const $ = (selector) => document.querySelector(selector);
let tools;
let toolsLoading = null;
// A failed import is retried on the next load rather than leaving the page without its renderer.
function loadTools() {
  toolsLoading ||= import("/js/tool-activity.js").then(
    (module) => { tools = module; },
    (error) => { toolsLoading = null; throw error; },
  );
  return toolsLoading;
}
const app = {
  state: {
    sessions: [],
    surfaces: [],
    queue: [],
    channels: [],
    relays: [],
    history: [],
    attention: [],
    deliveryProblems: [],
  },
  selected: null,
  history: null,
  filters: { surface: "all", status: "all" },
  inboxMode: "current",
  showArchived: false,
  expandedProjects: new Set(),
  openArchivedProjects: false,
  operationsTab: "queue",
  audit: { items: [], kinds: [], nextBefore: 0, hasMore: true, loaded: false, loading: false, kind: "all", query: "", searchTimer: null },
  sessionLimit: 0,
  sessionViewport: null,
  mobileChatOpen: false,
  slashCommands: [],
  drafts: new Map(),
  pendingIdempotency: new Map(),
  transcriptSignature: null,
  pendingEntryScroll: false,
  liveSource: null,
  liveSessionID: "",
  liveText: "",
  liveTools: [],
  settings: null,
  remoteQRVisible: false,
  remoteQRHideTimer: null,
  startModels: {},
  startHost: "",
  startOptions: null,
  launchers: [],
  codexSearch: { query: "", results: [], loading: false, error: "", timer: null, controller: null },
  rekeyed: {},
  voice: { state: null, attemptId: "", localAudio: false, connection: false, channel: false, script: null, requestedTarget: null, poll: null, appliedSDP: "" },
};
const labels = { claude: "Claude Code", codex: "Codex", notion: "Notion" };
globalThis.escape = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (char) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#039;",
      })[char],
  );
function inlineMarkdown(value) {
  return value
    .replace(
      /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
      '<a href="$2" target="_blank" rel="noreferrer">$1</a>',
    )
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/__([^_]+)__/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>");
}
function markdown(value) {
  const blocks = [];
  let text = escape(value).replace(
    /```(?:[\w+-]+)?\n?([\s\S]*?)```/g,
    (_, code) => {
      const id = blocks.length;
      blocks.push(`<pre><code>${code.trimEnd()}</code></pre>`);
      return `\u0000CODE${id}\u0000`;
    },
  );
  text = text.replace(
    /(^\|.*\|\r?\n^\|(?:\s*:?-+:?\s*\|)+\r?\n(?:^\|.*\|(?:\r?\n|$))+)/gm,
    (table) => {
      const rows = table
        .trim()
        .split(/\r?\n/)
        .map((row) =>
          row
            .slice(1, -1)
            .split("|")
            .map((cell) => cell.trim()),
        );
      const headers = rows[0];
      const body = rows.slice(2);
      const html = `<div class="table-wrap"><table><thead><tr>${headers.map((cell) => `<th>${inlineMarkdown(cell)}</th>`).join("")}</tr></thead><tbody>${body.map((row) => `<tr>${row.map((cell) => `<td>${inlineMarkdown(cell)}</td>`).join("")}</tr>`).join("")}</tbody></table></div>`;
      const id = blocks.length;
      blocks.push(html);
      return `\u0000CODE${id}\u0000`;
    },
  );
  const output = [];
  let paragraph = [];
  let list = null;
  const flushParagraph = () => {
    if (paragraph.length) {
      output.push(`<p>${paragraph.map(inlineMarkdown).join("<br>")}</p>`);
      paragraph = [];
    }
  };
  const closeList = () => {
    if (list) {
      output.push(`</${list}>`);
      list = null;
    }
  };
  for (const line of text.split(/\r?\n/)) {
    const code = line.match(/^\u0000CODE(\d+)\u0000$/);
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    const unordered = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+\.\s+(.+)$/);
    const quote = line.match(/^\s*>\s?(.+)$/);
    if (!line.trim()) {
      flushParagraph();
      closeList();
      continue;
    }
    if (code) {
      flushParagraph();
      closeList();
      output.push(blocks[Number(code[1])]);
      continue;
    }
    if (heading) {
      flushParagraph();
      closeList();
      const level = Math.min(4, heading[1].length + 1);
      output.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`);
      continue;
    }
    if (unordered || ordered) {
      flushParagraph();
      const next = unordered ? "ul" : "ol";
      if (list !== next) {
        closeList();
        output.push(`<${next}>`);
        list = next;
      }
      output.push(`<li>${inlineMarkdown((unordered || ordered)[1])}</li>`);
      continue;
    }
    if (quote) {
      flushParagraph();
      closeList();
      output.push(`<blockquote>${inlineMarkdown(quote[1])}</blockquote>`);
      continue;
    }
    flushParagraph();
    paragraph.push(line);
  }
  flushParagraph();
  closeList();
  return output.join("");
}
const rawDisplayName = (session) =>
  session?.alias
    ? `@${session.alias}`
    : session?.name || session?.id || "Untitled conversation";
function displayName(session) {
  const raw = rawDisplayName(session);
  const repository = raw.match(/^https?:\/\/[^/]+\/([^/\s]+)\/([^/\s]+)/);
  return repository ? `${repository[1]} / ${repository[2]}` : raw;
}
const sessionBatchSize = () =>
  window.matchMedia("(max-width: 800px)").matches ? 8 : 80;
const timeAgo = (value) => {
  const then = new Date(value).getTime();
  if (!then) return "recently";
  const minutes = Math.max(0, Math.round((Date.now() - then) / 60000));
  return minutes < 2
    ? "just now"
    : minutes < 60
      ? `${minutes}m ago`
      : minutes < 1440
        ? `${Math.round(minutes / 60)}h ago`
        : `${Math.round(minutes / 1440)}d ago`;
};
function handoffMessage(value) {
  let text = String(value || "");
  let label = "Conversation input";
  let match;
  while ((match = text.match(/^\[from\s+([^\]]+)\]\s*/i))) {
    label = `From ${match[1]}`;
    text = text.slice(match[0].length);
  }
  return { label, text };
}
function exchangePrompt(exchange) {
  const message = handoffMessage(exchange.user);
  if (exchange.origin === "peer") return { ...message, label: exchange.sender ? `From ${exchange.sender}` : "From an agent" };
  if (exchange.origin === "task-notification") return { ...message, label: "Task notification" };
  if (exchange.origin === "auto-continuation") return { ...message, label: "Continued" };
  if (exchange.origin === "agenthail-notice") return { ...message, label: "Agenthail" };
  if (exchange.origin) return { ...message, label: exchange.origin };
  return message;
}
// The footer line carries the turn's time and its actions, so revealing
// Copy on hover never adds a line or moves the text.
function renderMessage(text, kind, label, turnKey, files = [], footer = "") {
  const safeText = String(text || "");
  const editable = kind === "user" && label === "Conversation input";
  const edit = editable ? `<button class="turn-action" type="button" data-edit-message="${escape(safeText)}">Edit</button>` : "";
  const content = safeText ? `<div class="turn-content">${markdown(safeText)}</div>` : "";
  const actions = safeText ? `<span class="turn-actions"><button class="turn-action" type="button" data-copy-text="${escape(safeText)}">Copy</button>${edit}</span>` : "";
  const status = footer ? `<span class="turn-status">${escape(footer)}</span>` : "";
  const footerLine = status || actions ? `<footer class="turn-footer">${status}${actions}</footer>` : "";
  return `<article class="turn ${kind}" data-turn-key="${escape(turnKey)}"><header class="turn-header"><span class="turn-label">${escape(label)}</span></header>${content}${renderAttachedFiles(files)}${footerLine}</article>`;
}
function fileSize(bytes) {
  if (!bytes) return "";
  const units = ["bytes", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit += 1;
  }
  return unit === 0 ? `${value} ${units[0]}` : `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`;
}
// A sent file lives on the host that owns the session; the chip copies the
// path the agent read it from.
function renderAttachedFiles(files) {
  if (!files?.length) return "";
  return `<div class="attached-files">${files.map(file => {
    const size = fileSize(file.bytes);
    return `<button class="attached-file" type="button" data-copy-text="${escape(file.path || "")}" data-copy-done="Path copied." title="${escape(file.path || "")}"><span class="attached-file-name">${escape(file.name || "File")}</span>${size ? `<span class="attached-file-size">${escape(size)}</span>` : ""}</button>`;
  }).join("")}</div>`;
}
function stopLiveStream(clear = false) {
  if (app.liveSource) app.liveSource.close();
  app.liveSource = null;
  app.liveSessionID = "";
  if (clear) {
    app.liveText = "";
    app.liveTools = [];
    $("#live-turn")?.remove();
  }
}
function renderLiveTurn() {
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
function alignTranscriptTop(container) {
  const top = container.getBoundingClientRect().top;
  const crossing = [...container.querySelectorAll("tr, pre, blockquote, li, h2, h3, h4, p")].find((element) => {
    const bounds = element.getBoundingClientRect();
    return bounds.top < top && bounds.bottom > top;
  });
  if (!crossing) return;
  const bounds = crossing.getBoundingClientRect();
  if (crossing.matches("p, li")) {
    const lineHeight = Number.parseFloat(getComputedStyle(crossing).lineHeight);
    if (Number.isFinite(lineHeight) && lineHeight > 0) {
      const lines = Math.floor((top - bounds.top) / lineHeight);
      container.scrollTop += bounds.top + lines * lineHeight - top;
      return;
    }
  }
  container.scrollTop += bounds.top - top;
}
function startLiveStream() {
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
// applyStreamedRow applies a session-stream row as a session page reads it. The
// daemon sends the page's own item; a host that predates it sends only the raw
// row. An updated row moves to the end, as its journal entry does.
function applyStreamedRow(items, envelope) {
  const row = envelope.data;
  const rest = items.filter((value) => value.id !== row.itemId);
  if (row.op === "remove") return rest;
  const item = envelope.item || { id: row.itemId, kind: row.kind, role: row.role, origin: row.origin, sender: row.sender, title: row.title || row.kind, text: row.body || "", timestamp: row.ts, status: row.status, resetsAt: row.resetsAt, truncated: row.truncated, bodyRef: row.bodyRef, attachment: row.attachment, files: row.files, callId: row.callId, turnId: row.turnId };
  return [...rest, item];
}
function promptOrigin(value) {
  if (value.role === "peer") return "peer";
  return value.origin || "";
}
function exchangesFromItems(items) {
  const exchanges = [];
  let turn = "";
  let open = false;
  for (const value of items) {
    if (value.kind === "done") {
      if (open && (!value.turnId || value.turnId === turn)) open = false;
      continue;
    }
    if (!value.text) continue;
    const prompt = (["message", "text"].includes(value.kind) && ["user", "peer"].includes(value.role)) || (value.kind === "event" && Boolean(value.origin));
    if (prompt) {
      if (open && turn && value.turnId === turn) {
        const last = exchanges.at(-1);
        if (!last.user) Object.assign(last, { user: value.text, origin: promptOrigin(value), sender: value.sender || "" });
        else if (last.origin === promptOrigin(value) && last.sender === (value.sender || "")) last.user += `\n\n${value.text}`;
        continue;
      }
      exchanges.push({ user: value.text, origin: promptOrigin(value), sender: value.sender || "", timestamp: value.timestamp });
      turn = value.turnId || "";
      open = true;
      continue;
    }
    const reply = (["message", "assistant"].includes(value.kind) && value.role === "assistant") || (value.kind === "text" && (!value.role || value.role === "assistant"));
    if (!reply) continue;
    if (!open || (turn && value.turnId && value.turnId !== turn)) {
      exchanges.push({ timestamp: value.timestamp });
      turn = value.turnId || "";
      open = true;
    }
    if (!turn) turn = value.turnId || "";
    exchanges.at(-1).assistant = value.text;
  }
  return exchanges;
}
function resizeComposer() {
  const input = $("#message");
  if (!input) return;
  const maxHeight = 160;
  input.style.height = "auto";
  const height = Math.min(input.scrollHeight, maxHeight);
  input.style.height = `${height}px`;
  input.style.overflowY = input.scrollHeight > maxHeight ? "auto" : "hidden";
}
function syncComposerAction() {
  const input = $("#message");
  const sendButton = $("#send");
  const steerButton = $("#steer-message");
  const capabilities = app.history?.capabilities || {};
  const busy = app.selected?.status === "busy";
  const hasMessage = Boolean(input.value.trim());
  const readOnly = Boolean(app.history?.readOnly || app.selected?.readOnly);
  const turnNotice = (app.history ? capabilities.turnNotice : app.selected?.capabilities?.turnNotice) || "";
  const stopping = busy && capabilities.interrupt && !hasMessage && !readOnly;
  sendButton.dataset.mode = stopping ? "interrupt" : "send";
  sendButton.querySelector("span:first-child").textContent = stopping ? "Stop" : "Send";
  sendButton.querySelector("span:last-child").textContent = stopping ? "■" : "↑";
  sendButton.disabled = readOnly || (!hasMessage && !stopping);
  steerButton.hidden = !(busy && !app.selected?.waitingOn && capabilities.steer && hasMessage && !readOnly);
  steerButton.disabled = steerButton.hidden;
  $("#composer").classList.toggle("is-stopping", stopping);
  $("#composer").classList.toggle("has-steer", !steerButton.hidden);
  renderHolderPicker();
  if (readOnly) return;
  $("#composer-note").textContent = app.selected?.waitingOn
    ? "This agent is waiting on you. Messages queue until you answer it."
    : turnNotice
    ? turnNotice
    : busy
    ? hasMessage
      ? "Send queues this for next. Steer now changes the turn in progress."
      : "This agent is working. Stop ends the current turn."
    : "Ready to send.";
}
function friendlyError(error) {
  return /failed to fetch|networkerror/i.test(String(error))
    ? "Agenthail is not connected. Restart it from the menu bar, then refresh this page."
    : String(error);
}
function toast(message) {
  const node = $("#toast");
  node.textContent = message;
  node.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    node.hidden = true;
  }, 5000);
}
function showView(name) {
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
    const first = scopedSessions()[0] || app.state.sessions[0];
    if (first) selectSession(first.id);
  }
  syncConversationLayout();
  if (view === "conversations") startLiveStream();
  else stopLiveStream();
  if (view === "operations" && app.operationsTab === "settings") loadSettings();
}
const mobileConversationView = () => window.matchMedia("(max-width: 760px)").matches;
function syncConversationLayout() {
  const conversations = document.querySelector('[data-view-panel="conversations"]');
  const active =
    mobileConversationView() &&
    app.mobileChatOpen &&
    conversations &&
    !conversations.classList.contains("hidden");
  document.querySelector(".conversation-layout")?.classList.toggle("mobile-chat-open", active);
  document.body.classList.toggle("mobile-chat-active", active);
}
function statusPill(status) {
  return `<span class="status-pill ${escape(status)}">${escape(statusLabel(status))}</span>`;
}
function statusLabel(status) {
  return (
    {
      busy: "Working",
      idle: "Ready",
      offline: "Offline",
      unknown: "Unavailable",
      notLoaded: "Unavailable",
    }[status] || status
  );
}
// A session between turns whose background tasks still run has not finished.
function backgroundWaiting(session) {
  return session.status !== "busy" && Boolean(session.backgroundTasks?.running);
}
// Where a session's wait is answered: here, in its terminal, or wherever else
// it runs. A hosted or background session's prompt that Agenthail does not
// hold has nowhere to answer it; stopping and resuming the session clears it.
function waitingPlace(session) {
  const waiting = session?.waitingOn;
  if (!waiting) return "";
  if (waiting.answerable) return "agenthail";
  if (waiting.kind === "dialog") return "dialog";
  const lifecycle = session.capabilities?.lifecycle || {};
  if (lifecycle.stop && lifecycle.resume) return "restart";
  if (session.runtimeKind === "claude-cli") return "terminal";
  return "elsewhere";
}
const waitingLabels = {
  agenthail: "Waiting on you",
  dialog: "Dialog open in its terminal",
  terminal: "Waiting in its terminal",
  restart: "Prompt out of reach",
  elsewhere: "Waiting on you",
};
function usageLimitLabel(session) {
  const resets = Date.parse(session.usageLimit?.resetsAt || "");
  return resets ? `Usage limit, resets ${new Date(resets).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}` : "Usage limit reached";
}
function limitResumeLabel(session) {
  const stop = session.limitResume;
  if (stop.state !== "waiting") return "Stopped by usage limit";
  const resets = Date.parse(stop.resetsAt || "");
  if (!resets) return "Usage limit, resumes when it lifts";
  const at = new Date(resets);
  const sameDay = at.toDateString() === new Date().toDateString();
  return `Usage limit, resumes ${at.toLocaleString([], sameDay ? { hour: "numeric", minute: "2-digit" } : { weekday: "short", hour: "numeric", minute: "2-digit" })}`;
}
function limitLabel(session) {
  return session.limitResume ? limitResumeLabel(session) : session.usageLimit ? usageLimitLabel(session) : "";
}
// How long a working turn stays quiet before its row says so.
const quietAfterMs = 10 * 60 * 1000;
// "no activity for 12 min · running Bash since 10:02" once a working turn has
// been quiet for quietAfterMs. It never changes the session's status.
function quietNote(activity, now = Date.now()) {
  const last = Date.parse(activity?.lastProgressAt || activity?.busySince || "");
  if (!last || now - last < quietAfterMs) return "";
  const parts = [`no activity for ${quietDuration(now - last)}`];
  if (activity.noProcess) {
    parts.push("no Claude process");
  } else if (activity.tool?.name) {
    const started = Date.parse(activity.tool.startedAt || "");
    parts.push(`running ${activity.tool.name}${started ? ` since ${new Date(started).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}` : ""}`);
  }
  return parts.join(" · ");
}
function quietDuration(ms) {
  const minutes = Math.floor(ms / 60000);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 ? `${hours} hr ${minutes % 60} min` : `${hours} hr`;
}
function workingLabel(session, now = Date.now()) {
  const note = quietNote(session.activity, now);
  return note ? `Working · ${note}` : "Working";
}
// The task list's progress while the session is open and has steps left. The
// row keeps the plan after the session closes; a session that stopped part
// way shows no badge.
function livePlan(session) {
  const plan = session.plan;
  if (!session.open || session.status === "offline" || session.unavailableReason || !plan?.total || plan.done >= plan.total) return null;
  return plan;
}
function planDetail(plan) {
  const done = `${plan.done} of ${plan.total} tasks done`;
  return plan.current ? `${done} · ${plan.current}` : done;
}
function presenceLabel(session, now = Date.now()) {
  if (session.waitingOn) return waitingLabels[waitingPlace(session)];
  if (limitLabel(session)) return limitLabel(session);
  if (backgroundWaiting(session)) return "Waiting on background work";
  if (session.status === "busy") return workingLabel(session, now);
  return (
    {
      working: "Working",
      queued: "Queued",
      open: "Open",
      recent: "Recent",
    }[session.currentReason] || statusLabel(session.status)
  );
}

function presenceTone(session) {
  if (backgroundWaiting(session)) return "background";
  if (session.status === "busy" || ["working", "open"].includes(session.currentReason)) return "active";
  if (session.status === "idle" || ["queued", "blocked"].includes(session.currentReason)) return "idle";
  return session.status;
}
function conversationMeta(session, model = "", pendingModel = session.pendingModel || "") {
  const parts = [session.waitingOn ? waitingLabels[waitingPlace(session)] : session.limitResume || session.usageLimit ? limitLabel(session) : backgroundWaiting(session) ? "Waiting on background work" : session.status === "busy" ? workingLabel(session) : statusLabel(session.status), `${session.queueCount || 0} queued`];
  if (session.status !== "busy") parts.push(`last active ${timeAgo(session.lastActive)}`);
  const label = modelLabel(model, pendingModel);
  if (label) parts.push(label);
  const configRoot = session.claudeConfigRoot;
  if (configRoot?.name && (configRoot.name !== "default" || configRoot.signedInAs)) parts.push(configRoot.signedInAs ? `config folder ${configRoot.name} · currently signed in as ${configRoot.signedInAs}` : `config folder ${configRoot.name}`);
  return parts.join(" · ");
}
function modelLabel(model = "", pendingModel = "") {
  if (!pendingModel || pendingModel === model) return model;
  return model ? `${model} · ${pendingModel} from next turn` : `${pendingModel} from next turn`;
}
function surfaceIcon(name) {
  return name === "claude" ? "✦" : name === "codex" ? "◈" : "N";
}
function renderOverview() {
  const { surfaces, sessions, attention } = app.state;
  const connected = surfaces.filter((surface) => surface.connected).length;
  const working = sessions.filter(
    (session) => session.currentReason === "working",
  ).length;
  const queued = sessions.reduce(
    (total, session) => total + session.queueCount,
    0,
  );
  const surfaceStatus =
    connected === 0
      ? "No surfaces connected"
      : `${connected} surface${connected === 1 ? "" : "s"} connected`;
  const activityStatus = [
    working > 0
      ? `${working} agent${working === 1 ? "" : "s"} working`
      : "",
    queued > 0
      ? `${queued} message${queued === 1 ? "" : "s"} waiting`
      : "",
  ].filter(Boolean);
  $("#daemon-status").textContent = app.state.daemon?.running
    ? "Connected"
    : "Not running";
  $("#daemon-detail").textContent = app.state.daemon?.stale
    ? `Showing cached data. ${app.state.daemon.refreshError || "Surface refresh is temporarily unavailable"}`
    : app.state.daemon?.running
      ? [surfaceStatus, ...activityStatus].join(" · ") || "Nothing needs delivery"
      : "Start Agenthail to deliver work";
  $("#surface-cards").innerHTML =
    surfaces
      .map((surface) => {
        const owned = sessions.filter(
          (session) => session.surface === surface.name,
        );
        const active = owned.filter(
          (session) => session.currentReason === "working",
        ).length;
        const present = owned.filter((session) =>
          surface.name === "claude"
            ? session.open
            : session.currentReason === "recent",
        ).length;
        const queued = owned.reduce(
          (total, session) => total + session.queueCount,
          0,
        );
        const name = labels[surface.name] || surface.name;
        const presenceWindow =
          surface.name === "claude"
            ? "Open now"
            : `Past ${app.state.codexRecentHours || 5}h`;
        const healthLabel = surface.health === "degraded"
          ? "Needs attention"
          : surface.health === "signin_off"
            ? "Sign-in off"
            : surface.health === "signin_unavailable"
            ? "No browser here"
            : surface.connected
            ? "Connected"
            : "Not connected";
        return `<button class="surface-row" type="button" data-surface="${escape(surface.name)}"><div class="surface-identity"><span class="surface-logo ${escape(surface.name)}">${surfaceIcon(surface.name)}</span><div><div class="surface-name">${escape(name)}</div><div class="connection-line ${escape(surface.health || (surface.connected ? "healthy" : "unavailable"))}" title="${escape(surface.healthDetail || surface.error || "")}"><i></i>${escape(healthLabel)}</div></div></div><div class="surface-stat"><span>Working</span><strong>${active}</strong></div><div class="surface-stat"><span>Current</span><strong>${present}</strong><small>${presenceWindow}</small></div><div class="surface-stat"><span>Queued</span><strong>${queued}</strong></div></button>`;
      })
      .join("") ||
    '<div class="empty-state compact">No surfaces are connected yet.</div>';
  const recent = [...sessions]
    .filter(sessionIsCurrent)
    .sort((a, b) => {
      const rank = { working: 0, queued: 1, open: 2, recent: 3 };
      const reasonOrder =
        (rank[a.currentReason] ?? 9) - (rank[b.currentReason] ?? 9);
      return (
        reasonOrder || new Date(b.lastActive || 0) - new Date(a.lastActive || 0)
      );
    })
    .slice(0, 8);
  $("#recent-activity").innerHTML =
    recent
      .map(
        (session) =>
          `<button class="activity-item" type="button" data-session="${escape(session.id)}"><div class="activity-main"><div class="activity-name"><i class="dot ${escape(presenceTone(session))}"></i><span>${escape(displayName(session))}</span></div><div class="activity-detail">${escape(labels[session.surface] || session.surface)} · ${session.status === "busy" ? "working now" : timeAgo(session.lastActive)}</div></div><span class="status-pill ${escape(backgroundWaiting(session) ? "background" : session.currentReason || session.status)}">${escape(presenceLabel(session))}</span></button>`,
      )
      .join("") ||
    '<p class="empty-inline">No conversations are active right now.</p>';
  const attentionPanel = $("#attention-panel");
  attentionPanel.hidden = attention.length === 0;
  $("#attention-list").innerHTML = attention
    .map(
      (item) =>
        `<article class="attention-item"><div><div class="attention-target"><i></i>${escape(item.target)}</div><strong>${escape(item.reason)}</strong><p>${escape(item.requestedAction)} · ${timeAgo(item.createdAt)}</p></div><div class="operation-actions"><button class="soft-button" data-retry="${item.queueId}" type="button">Retry</button><button class="soft-button" data-cancel="${item.queueId}" type="button">Cancel</button></div></article>`,
    )
    .join("");
  renderDeliveryProblems();
}
function renderDeliveryProblems() {
  const problems = app.state.deliveryProblems || [];
  $("#delivery-problems-panel").hidden = problems.length === 0;
  $("#delivery-problems-list").innerHTML = problems.map(item => {
    const session = app.state.sessions.find(session => session.id === item.sessionId);
    const target = session ? displayName(session) : item.sessionId;
    return `<article class="attention-item"><div><div class="attention-target">${escape(target)}</div><strong>${escape(item.reason)}</strong><p>${escape(item.message)}</p><p>${escape(item.status)} · ${timeAgo(item.at)}</p></div><div class="operation-actions"><button class="soft-button" data-delivery-dismiss="${escape(item.deliveryId)}" type="button">Dismiss</button></div></article>`;
  }).join("");
}
function sessionIsCurrent(session) {
  return session.current === true && !session.archived;
}
function scopedSessions() {
  if (app.inboxMode === "current")
    return app.state.sessions.filter(sessionIsCurrent);
  if (app.inboxMode === "projects")
    return app.state.sessions.filter((session) => session.project && !session.archived);
  return app.state.sessions.filter((session) => app.showArchived || !session.archived);
}
const collapsedProjectSessions = 5;
function sessionNeedsYou(session) {
  return session.status === "busy" || (session.subagents?.working || 0) > 0 || session.queueCount > 0 || (app.state.attention || []).some((item) => item.sessionId === session.id);
}
function sharedHolders(session) {
  return session?.surface === "claude" && (session.holders || []).length > 1 ? session.holders : [];
}
function holderLabel(holder) {
  return [`pid ${holder.pid}`, holder.tty, holder.status].filter(Boolean).join(", ");
}
// A Claude conversation open in several terminals takes a write only for
// the terminal chosen here; the daemon refuses one that names none.
function renderHolderPicker() {
  const holders = sharedHolders(app.selected);
  const picker = $("#holder-picker");
  const select = $("#holder-pid");
  picker.hidden = holders.length === 0;
  if (holders.length === 0) {
    select.innerHTML = "";
    return;
  }
  const chosen = select.dataset.session === app.selected.id ? select.value : "";
  $("#holder-summary").textContent = `Open in ${holders.length} terminals. Send to`;
  select.innerHTML = `<option value="">Choose a terminal</option>${holders.map((holder) => `<option value="${escape(String(holder.pid))}">${escape(holderLabel(holder))}</option>`).join("")}`;
  select.value = holders.some((holder) => String(holder.pid) === chosen) ? chosen : "";
  select.dataset.session = app.selected.id;
}
function chosenHolder(sessionID) {
  const select = $("#holder-pid");
  if (sessionID !== app.selected?.id || sharedHolders(app.selected).length === 0 || select.dataset.session !== sessionID) return 0;
  return Number(select.value) || 0;
}
function withHolder(sessionID, payload) {
  const pid = chosenHolder(sessionID);
  if (!pid) return payload;
  const holder = sharedHolders(app.selected).find((candidate) => candidate.pid === pid);
  const startedAt = Date.parse(holder?.startedAt || "");
  return Number.isFinite(startedAt) ? { ...payload, pid, pidStartedAt: startedAt } : { ...payload, pid };
}
function sessionButton(session) {
  const holders = sharedHolders(session);
  const plan = livePlan(session);
  return `<button class="session ${app.selected?.id === session.id ? "selected" : ""}${session.archived ? " archived" : ""}" title="${escape(rawDisplayName(session))}" type="button" data-session="${escape(session.id)}"><div class="session-name"><i class="dot ${escape(presenceTone(session))}"></i><span>${escape(displayName(session))}</span></div><div class="session-detail">${hostBadge(session)}<span>${escape(labels[session.surface] || session.surface)}</span><span>${escape(presenceLabel(session))}</span>${plan ? `<span title="${escape(planDetail(plan))}"><span aria-hidden="true">${plan.done}/${plan.total}</span><span class="sr-only">${escape(planDetail(plan))}</span></span>` : ""}${session.queueCount ? `<span>${session.queueCount} queued</span>` : ""}${holders.length ? `<span title="${escape(holders.map(holderLabel).join("; "))}">${holders.length} terminals</span>` : ""}${session.archived ? "<span>Archived</span>" : ""}</div></button>`;
}
function projectSection(project, sessions) {
  const expanded = app.expandedProjects.has(project.id);
  const ordered = [...sessions].sort((a, b) => Date.parse(b.lastActive || 0) - Date.parse(a.lastActive || 0));
  const shown = expanded ? ordered : ordered.filter((session, index) => index < collapsedProjectSessions || sessionNeedsYou(session));
  const hidden = ordered.length - shown.length;
  const host = (project.checkouts || []).filter((checkout) => checkout.host.state !== "local").map((checkout) => `<span class="host-badge" title="${escape(`On ${checkout.host.name}${checkout.archived ? " (archived there)" : ""}`)}">${escape(shortHostName(checkout.host.name))}</span>`).join("");
  const toggle = project.archived ? "project-unarchive" : "project-archive";
  return `<section class="project-group${project.archived ? " archived" : ""}"><header class="project-header"><span class="project-name">${escape(project.displayName)}</span><span class="project-count" title="${ordered.length} ${ordered.length === 1 ? "session" : "sessions"}">${ordered.length}</span>${host}${coordinatorChip(project.coordinators || [])}${project.archived ? "" : `<button class="link-button" type="button" data-project-message="${escape(project.id)}" data-project-name="${escape(project.displayName)}" title="${escape(`Send one message to every active conversation in ${project.displayName}`)}">Message</button>`}<button class="link-button" type="button" data-project-action="${toggle}" data-project-id="${escape(project.id)}">${project.archived ? "Unarchive" : "Archive"}</button>${projectMenu(project)}</header>${shown.map(sessionButton).join("")}${hidden > 0 || expanded && ordered.length > collapsedProjectSessions ? `<button class="link-button project-more" type="button" data-project-expand="${escape(project.id)}">${expanded ? "Show less" : `Show ${hidden} more`}</button>` : ""}</section>`;
}
// projectMenu lists what a person can change about a project: its name once
// it was added on some host, a new conversation and the lead in one of its
// checkouts, and removing each added checkout. A checkout is named by its
// host when the project is on several, never by its folder.
function projectMenu(project) {
  const checkouts = project.checkouts || [];
  const several = new Set(checkouts.map((checkout) => checkout.host?.id)).size > 1;
  const where = (checkout) => (several || checkout.host?.state !== "local" ? ` on ${shortHostName(checkout.host?.name)}` : "");
  const id = escape(project.id);
  const items = [];
  if (checkouts.some((checkout) => checkout.added)) items.push(`<button type="button" data-project-rename="${id}">Rename…</button>`);
  if (!project.archived) {
    checkouts.forEach((checkout, index) => {
      if (!checkout.archived) items.push(`<button type="button" data-project-start="${id}" data-checkout="${index}">New conversation${escape(where(checkout))}</button>`);
    });
    items.push(`<button type="button" data-project-lead="${id}">${(project.coordinators || []).length ? "Change lead…" : "Set lead…"}</button>`);
    if ((project.coordinators || []).length === 1) items.push(`<button type="button" data-project-clear-lead="${id}">Clear lead</button>`);
  }
  checkouts.forEach((checkout) => {
    if (checkout.added) items.push(`<button type="button" data-project-action="project-remove" data-project-id="${escape(checkout.projectId)}">Remove${escape(where(checkout))}</button>`);
  });
  return `<details class="project-menu"><summary class="link-button" aria-label="${escape(`More for ${project.displayName}`)}">…</summary><div class="project-menu-items" role="menu">${items.join("")}</div></details>`;
}
// checkoutHost is the host an action on a checkout names: empty for this
// machine.
function checkoutHost(checkout) {
  return checkout?.host && checkout.host.state !== "local" ? checkout.host.id : "";
}
// projectFolderSuggestions are the folders a host offers for a new project:
// its repositories that are not projects yet, then the folders its recent
// sessions ran in.
function projectFolderSuggestions(options) {
  const roots = (options?.projects || []).filter((project) => !project.added).map((project) => project.rootPath);
  return [...new Set([...roots, ...(options?.folders || [])])];
}
// leadRequest starts a new lead conversation in one of a project's
// checkouts, on that checkout's host.
function leadRequest(checkout, values, replace) {
  const request = { surface: values.surface, cwd: checkout.rootPath };
  const host = checkoutHost(checkout);
  if (host) request.host = host;
  if (values.name.trim()) request.name = values.name.trim();
  if (replace) request.replace = true;
  return request;
}
function coordinatorRef(coordinator) {
  const name = `@${coordinator.name}`;
  return coordinator.host && coordinator.host.state !== "local" ? `${shortHostName(coordinator.host.name)}::${name}` : name;
}
function coordinatorChip(coordinators) {
  if (coordinators.length === 0) return "";
  if (coordinators.length > 1) {
    const refs = coordinators.map(coordinatorRef).join(", ");
    return `<span class="lead-chip conflict" title="${escape(`Several hosts each have a coordinator: ${refs}. Change the lead to keep one.`)}">${coordinators.length} leads</span>`;
  }
  const ref = coordinatorRef(coordinators[0]);
  if (!coordinators[0].sessionId) return `<span class="lead-chip idle" title="${escape(`${ref} starts on its first message`)}">${escape(ref)}</span>`;
  return `<button class="lead-chip" type="button" data-session="${escape(coordinators[0].sessionId)}" title="${escape(`Open ${ref}, the project coordinator`)}">${escape(ref)}</button>`;
}
function renderProjects(query) {
  const byProject = new Map();
  const projectArchived = new Map((app.state.projects || []).map((project) => [project.id, project.archived]));
  for (const session of app.state.sessions) {
    if (!session.project) continue;
    if (session.archived && !projectArchived.get(session.project.id)) continue;
    if (query && !`${displayName(session)} ${session.project.displayName} ${remoteHostName(session)}`.toLowerCase().includes(query)) continue;
    if (!byProject.has(session.project.id)) byProject.set(session.project.id, []);
    byProject.get(session.project.id).push(session);
  }
  const projects = app.state.projects || [];
  const matches = (project) => !query || byProject.has(project.id) || project.displayName.toLowerCase().includes(query);
  const active = projects.filter((project) => !project.archived && matches(project));
  const archived = projects.filter((project) => project.archived && matches(project));
  const sections = active.map((project) => projectSection(project, byProject.get(project.id) || [])).join("");
  const archivedSections = archived.length
    ? `<details class="archived-projects" ${app.openArchivedProjects ? "open" : ""}><summary>Archived (${archived.length})</summary>${archived.map((project) => projectSection(project, byProject.get(project.id) || [])).join("")}</details>`
    : "";
  return { html: '<div class="projects-toolbar"><button class="link-button" type="button" data-project-new>Add project…</button></div>' + sections + archivedSections, count: active.length };
}
function remoteHostName(session) {
  return session?.host && session.host.state !== "local" ? session.host.name : "";
}
function shortHostName(name) {
  return String(name || "").split(".")[0];
}
function hostBadge(session) {
  const host = remoteHostName(session);
  if (!host) return "";
  const unavailable = Boolean(session.unavailableReason);
  const short = shortHostName(host);
  return `<span class="host-badge${unavailable ? " unavailable" : ""}" title="${escape(session.unavailableReason || `On ${host}`)}">${escape(unavailable ? `${short} unavailable` : short)}</span>`;
}
function renderSessions() {
  const mobileViewport = window.matchMedia("(max-width: 800px)").matches;
  if (app.sessionViewport !== mobileViewport) {
    app.sessionViewport = mobileViewport;
    app.sessionLimit = sessionBatchSize();
  }
  if (!app.sessionLimit) app.sessionLimit = sessionBatchSize();
  const query = $("#session-search").value.trim().toLowerCase();
  const counts = app.state.counts || {};
  document.querySelectorAll("[data-inbox-count]").forEach((badge) => {
    const value = badge.dataset.inboxCount === "projects" ? (app.state.projects || []).filter((project) => !project.archived).length : counts[badge.dataset.inboxCount];
    badge.textContent = value === undefined ? "" : ` ${value}`;
  });
  if (app.inboxMode === "projects") {
    const projects = renderProjects(query);
    $("#conversation-count").textContent = `${projects.count}`;
    $("#session-tools").classList.remove("hidden");
    $("#session-filters").hidden = true;
    $("#show-archived-row").hidden = true;
    syncInboxModeButtons();
    $("#filter-summary").textContent = projects.count ? `${projects.count} ${projects.count === 1 ? "project" : "projects"}, most recent first` : "No projects yet";
    $("#session-list").innerHTML = projects.html || '<div class="empty-state compact"><p>Sessions in a Git repository or an added folder appear here.</p></div>';
    $("#session-more").hidden = true;
    return;
  }
  $("#session-filters").hidden = false;
  $("#show-archived-row").hidden = false;
  const sessions = scopedSessions().filter((session) => {
    const matchesQuery =
      !query ||
      `${displayName(session)} ${session.surface} ${session.name} ${remoteHostName(session)}`
        .toLowerCase()
        .includes(query);
    const matchesSurface =
      app.filters.surface === "all" || session.surface === app.filters.surface;
    const matchesStatus =
      app.filters.status === "all" ||
      session.status === app.filters.status ||
      (app.filters.status === "background" && backgroundWaiting(session)) ||
      (app.filters.status === "unavailable" &&
        ["unknown", "notLoaded"].includes(session.status));
    return matchesQuery && matchesSurface && matchesStatus;
  });
  const visible = sessions.slice(0, app.sessionLimit);
  const historyResults = app.codexSearch.query === query ? app.codexSearch.results : [];
  $("#conversation-count").textContent = `${sessions.length}`;
  $("#session-tools").classList.toggle("hidden", app.inboxMode !== "all");
  syncInboxModeButtons();
  $("#filter-summary").textContent =
    app.inboxMode === "current"
      ? sessions.length
        ? `Open Claude sessions, working or queued work, and recent sessions (Codex ${app.state.codexRecentHours || 5}-hour window)`
        : "Nothing current right now"
      : sessions.length > visible.length
        ? `Showing ${visible.length} of ${sessions.length}`
        : `${sessions.length} conversations`;
  const sessionItems = visible.map(sessionButton).join("");
  const historyItems = historyResults
    .map(
      (result) =>
        `<button class="session" title="${escape(rawDisplayName(result))}" type="button" data-session="${escape(result.id)}"><div class="session-name"><i class="dot recent"></i><span>${escape(displayName(result))}</span></div><div class="session-detail"><span>Codex history</span><span>${escape(result.snippet || "Matched conversation")}</span></div></button>`,
    )
    .join("");
  const searchStatus = app.codexSearch.loading
    ? '<div class="filter-summary">Searching Codex history…</div>'
    : app.codexSearch.error
      ? `<div class="filter-summary">${escape(app.codexSearch.error)}</div>`
      : historyItems
        ? '<div class="filter-summary">Codex history matches</div>'
        : "";
  $("#session-list").innerHTML =
    sessionItems || historyItems || searchStatus
      ? `${sessionItems}${searchStatus}${historyItems}`
      : `<div class="empty-state compact"><p>${app.inboxMode === "current" ? "No current work. Search to find an older Codex conversation." : "No conversations in this view."}</p></div>`;
  const more = $("#session-more");
  more.hidden = app.inboxMode !== "all" || visible.length >= sessions.length;
  more.textContent = `Show more (${sessions.length - visible.length})`;
}
function syncInboxModeButtons() {
  document.querySelectorAll("[data-inbox-mode]").forEach((button) => {
    const active = button.dataset.inboxMode === app.inboxMode;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}
function renderSurfaceFilter() {
  const select = $("#session-surface-filter");
  const current = app.filters.surface;
  const options = [
    '<option value="all">All surfaces</option>',
    ...app.state.surfaces.map(
      (surface) =>
        `<option value="${escape(surface.name)}">${escape(labels[surface.name] || surface.name)}</option>`,
    ),
  ];
  select.innerHTML = options.join("");
  select.value = app.state.surfaces.some((surface) => surface.name === current)
    ? current
    : "all";
  app.filters.surface = select.value;
  $("#session-status-filter").value = app.filters.status;
}
function queueReason(item) {
	if (item.evidence === "transport_accepted") return "Transport accepted; receiver completion is pending";
	if (item.evidence === "unknown") return item.historical ? "Delivery was never confirmed" : "Submitted";
	if (item.evidence === "failed") return "Delivery failed";
	if (item.status !== "pending") return statusLabel(item.status);
  if (item.heldBy === "hostUnreachable")
    return "Held: this agent's host is unreachable";
  const target = app.state.sessions.find(
    (session) => session.id === item.sessionId,
  );
  if (target?.readOnly) return target.readOnlyReason || "Target is not writable";
  if (target?.surface === "codex" && target?.status === "notLoaded") {
    return target.transport === "desktop"
      ? "Loading this Codex conversation before delivery"
      : "Codex conversation is not available to load";
  }
  return (
    {
      waiting: "Held: this agent is waiting on you",
      busy: "Held: this agent is working",
      offline: "Held: this agent is offline",
      unknown: "Held: this agent's state is unknown",
      refused: "Held: this agent refused to start",
      retrying: "Held: waiting to retry",
      behind: "Held: behind an earlier message",
      notLoaded: "Held: this conversation is not loaded",
      hostUnreachable: "Held: this agent's host is unreachable",
    }[item.heldBy] || "Delivers on the next scan"
  );
}
function renderOperations() {
  const { queue, channels, relays, history = [] } = app.state;
  $("#operations-queue-count").textContent = queue.length;
  $("#operations-relay-count").textContent = relays.length;
  $("#operations-history-count").textContent = history.length === 50 ? "50+" : history.length;
  $("#queue-list").innerHTML =
    queue
      .map(
        (item) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title"><i class="operation-dot ${escape(item.status)}"></i>${escape(item.target)}</div><div class="operation-detail queue-message">${escape(item.message)}${item.lastError ? `<br><span class="operation-error">${escape(item.lastError)}</span>` : ""}</div><div class="operation-meta">${escape(queueReason(item))} · queued ${timeAgo(item.queuedAt)}</div></div><div class="operation-actions">${statusPill(item.status)}${item.status === "dead" ? `<button class="button quiet" data-retry="${item.id}" type="button">Retry</button>` : ""}${item.status === "pending" ? `<button class="button quiet" data-cancel="${item.id}" type="button">Cancel</button>` : ""}</div></article>`,
      )
      .join("") ||
    '<div class="empty-card">Nothing is waiting to be delivered.</div>';
  const outcomes = history
    .filter((entry) => entry.evidence)
    .slice(0, 8);
  $("#delivery-history").innerHTML =
    outcomes
      .map((entry) => {
        const label = {
          queued: "Queued",
          transport_accepted: "Transport accepted",
          held: "Held by the receiver",
          delivered: "Delivered",
          reply_observed: "Reply observed",
          failed: "Failed",
          unknown: "Never confirmed",
          canceled: "Canceled",
        }[entry.evidence] || auditKindLabel(entry.kind);
        const detail = entry.error || entry.message || entry.result || "No details recorded";
        return `<article class="delivery-event"><div class="delivery-event-heading"><strong>${escape(label)} · ${escape(entry.target || "Agenthail")}</strong><time>${timeAgo(entry.createdAt)}</time></div><p>${escape(detail)}</p></article>`;
      })
      .join("") ||
    '<div class="empty-card">No delivery outcomes have been recorded yet.</div>';
  $("#channel-list").innerHTML =
    channels
      .map(
        (channel) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">#${escape(channel.name)}</div><div class="operation-detail">${escape(channel.members.join(" · ") || "No members yet")}</div></div><button class="button quiet" data-network-action="channel-delete" data-channel="${escape(channel.name)}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No shared channels yet.</div>';
  $("#channel-actions").hidden = channels.length === 0;
  $("#relay-list").innerHTML =
    relays
      .map(
        (relay) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(relay.from)} <span class="route-arrow">→</span> ${escape(relay.to)}</div><div class="operation-detail">${relay.handoff === "goal_completed" ? "Goal completed" : "Turn finished"} · Matches /${escape(relay.pattern)}/ · ${relay.once ? "once" : "persistent"} · ${relay.active ? "active" : "complete"} · queued ${relay.fireCount || 0} · pending ${relay.pendingCount || 0}${relay.lastError ? ` · ${escape(relay.lastError)}` : ""}${relay.lastFiredAt ? ` · last ${escape(relay.lastFiredAt)}` : ""}</div></div><button class="button quiet" data-network-action="relay-remove" data-relay-id="${relay.id}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No automatic handoffs yet.</div>';
  renderAudit();
  renderSettings();
  document
    .querySelectorAll("[data-operations-panel]")
    .forEach((panel) =>
      panel.classList.toggle(
        "hidden",
        panel.dataset.operationsPanel !== app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.classList.toggle(
        "active",
        tab.dataset.operationsTab === app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.setAttribute(
        "aria-pressed",
        String(tab.dataset.operationsTab === app.operationsTab),
      ),
    );
}
function auditKindLabel(kind) {
  const words = String(kind || "event").replace(/[_-]+/g, " ").trim().toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}
function auditHeading(entry) {
  const operation = entry.operation && entry.operation !== "message" ? auditKindLabel(entry.operation) : "";
  return operation ? `${auditKindLabel(entry.kind)} · ${operation}` : auditKindLabel(entry.kind);
}
function auditRoute(entry) {
  const target = entry.target || entry.sessionId || "Agenthail";
  return entry.source && entry.source !== target ? `${escape(entry.source)} <span class="route-arrow">→</span> ${escape(target)}` : escape(target);
}
function signinOffHint(detail) {
  const claude = /^Claude Remote Control/.test(detail);
  const consumer = claude ? "Claude sessions reached over Remote Control read" : "Notion reads";
  return `Browser sign-in is off, so ${consumer} no cookies. Turn it on in Settings > Browser Sign-in, or run agenthail signin use ${claude ? "claude" : "notion"} auto.`;
}
function signinResolved(consumer) {
  if (consumer.mode === "off") return "Off. Reads no cookies.";
  if (!consumer.browserLabel) return "Uses the system default browser.";
  const name = consumer.profileName && consumer.profileName !== consumer.profile ? ` (${consumer.profileName})` : "";
  const where = consumer.browser === "safari" ? "Safari" : `${consumer.browserLabel} · ${consumer.profile}${name}`;
  if (consumer.mode === "automatic") return `Automatic: ${where}, the system default browser.`;
  return `${consumer.profileName}: ${consumer.browserLabel} · ${consumer.profile}.`;
}
async function signinRequest(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { "content-type": "application/json", ...(options.headers || {}) } });
  const text = await response.text();
  let body = {};
  try {
    body = text ? JSON.parse(text) : {};
  } catch {
    body = {};
  }
  if (!response.ok) throw Error(body.error?.message || text || `HTTP ${response.status}`);
  return body;
}
async function loadSignin() {
  try {
    app.signin = await signinRequest("/api/v1/signin");
    renderSignin();
  } catch (error) {
    $("#signin-consumers").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}
function signinProfileLabel(profile) {
  const browser = (app.signin.browsers || []).find((item) => item.browser === profile.browser);
  return `${browser?.label || profile.browser} · ${profile.profile}`;
}
function renderSignin() {
  const signin = app.signin;
  if (!signin) return;
  const profiles = signin.profiles || [];
  $("#signin-consumers").innerHTML = (signin.consumers || []).map((consumer) => {
    const options = [
      `<option value="auto"${consumer.mode === "automatic" ? " selected" : ""}>Automatic (system default browser)</option>`,
      ...profiles.map((profile) => `<option value="${escape(profile.id)}"${consumer.profileId === profile.id ? " selected" : ""}>${escape(profile.label)}</option>`),
      `<option value="none"${consumer.mode === "off" ? " selected" : ""}>Off</option>`,
    ].join("");
    const state = consumer.mode === "off" ? '<span class="health-state signin_off">Off</span>' : consumer.unavailable ? '<span class="health-state signin_off">No browser here</span>' : consumer.problem ? '<span class="health-state unavailable">Cannot read</span>' : "";
    const problem = consumer.unavailable ? `<p class="signin-resolved">${escape(consumer.unavailable)}</p>` : consumer.problem ? `<p class="signin-problem">${escape(consumer.problem)}</p>` : "";
    return `<div class="signin-consumer"><div><strong>${escape(consumer.label)}</strong>${state}<p>${escape(consumer.purpose)}</p><p class="signin-resolved">${escape(signinResolved(consumer))}</p>${problem}</div><label><span class="sr-only">Sign-in profile for ${escape(consumer.label)}</span><select data-signin-consumer="${escape(consumer.id)}">${options}</select></label></div>`;
  }).join("");
  $("#signin-profiles").innerHTML = profiles.map((profile) =>
    `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(profile.label)}</div><div class="operation-detail">${escape(signinProfileLabel(profile))}</div></div><button class="button quiet" data-signin-remove="${escape(profile.id)}" type="button">Remove</button></article>`,
  ).join("") || '<div class="empty-card">No pinned profiles. Add one below to use a profile other than the default browser\'s.</div>';
  const available = (signin.browsers || []).filter((browser) => browser.available && browser.profiles.some((profile) => !profile.profileId));
  const browserSelect = $("#signin-browser");
  const previous = browserSelect.value;
  browserSelect.innerHTML = available.map((browser) => `<option value="${escape(browser.browser)}">${escape(browser.label)}</option>`).join("") || '<option value="">No browser profiles to add</option>';
  if (available.some((browser) => browser.browser === previous)) browserSelect.value = previous;
  renderSigninProfileChoices();
  const unavailable = (signin.browsers || []).filter((browser) => !browser.available);
  const note = $("#signin-unavailable");
  note.hidden = unavailable.length === 0;
  note.textContent = unavailable.map((browser) => `${browser.label} is unavailable: ${browser.detail}.`).join(" ");
}
function renderSigninProfileChoices() {
  const browser = (app.signin?.browsers || []).find((item) => item.browser === $("#signin-browser").value);
  const choices = (browser?.profiles || []).filter((profile) => !profile.profileId);
  $("#signin-profile").innerHTML = choices.map((profile) => `<option value="${escape(profile.profile)}">${escape(profile.name)}${profile.name === profile.profile ? "" : ` (${escape(profile.profile)})`}</option>`).join("");
  $("#signin-add-form button[type=submit]").disabled = choices.length === 0;
}
// Browser notifications come from a service worker scoped to /js/, which
// controls no page, so its registration is looked up by that scope rather
// than through navigator.serviceWorker.ready.
const pushWorkerScope = "/js/";
function browserPushUnavailable() {
  if (!window.isSecureContext) return "Notifications need a secure address. Open the dashboard at localhost on this computer, or through agenthail dashboard remote.";
  const appleTouch = /iPhone|iPad/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  if (appleTouch && !navigator.standalone) return "On iPhone and iPad, add Agenthail to your Home Screen with Share, then turn this on from there.";
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return "This browser cannot show notifications from a web page.";
  return "";
}
async function pushWorker() {
  const registration = await navigator.serviceWorker.register("/js/push-worker.js", { scope: pushWorkerScope });
  if (registration.active) return registration;
  const worker = registration.installing || registration.waiting;
  await new Promise((resolve, reject) => {
    worker.addEventListener("statechange", () => {
      if (worker.state === "activated") resolve();
      if (worker.state === "redundant") reject(Error("The notification worker could not start."));
    });
  });
  return registration;
}
function pushKeyBytes(key) {
  const base64 = key.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(key.length / 4) * 4, "=");
  return Uint8Array.from(atob(base64), (character) => character.charCodeAt(0));
}
function sameKey(subscription, key) {
  const current = subscription.options?.applicationServerKey;
  if (!current) return true;
  const left = new Uint8Array(current);
  return left.length === key.length && left.every((byte, index) => byte === key[index]);
}
function renderBrowserPush(on, note) {
  const toggle = $("#browser-push-toggle");
  toggle.checked = on;
  toggle.disabled = Boolean(app.browserPush.unavailable) || app.browserPush.busy;
  $("#browser-push-note").textContent = note;
}
async function loadBrowserPush() {
  app.browserPush = { unavailable: browserPushUnavailable(), busy: false };
  if (app.browserPush.unavailable) return renderBrowserPush(false, app.browserPush.unavailable);
  try {
    const status = await signinRequest("/api/v1/browser/push");
    const registration = await navigator.serviceWorker.getRegistration(pushWorkerScope);
    const subscription = await registration?.pushManager.getSubscription();
    const key = pushKeyBytes(status.publicKey);
    if (status.subscribed && subscription && sameKey(subscription, key)) {
      await signinRequest("/api/v1/browser/push", { method: "PUT", body: JSON.stringify(subscription.toJSON()) });
      registration.update().catch(() => {});
      return renderBrowserPush(true, "On. This browser is notified while it is signed in.");
    }
    if (status.subscribed) await signinRequest("/api/v1/browser/push", { method: "DELETE" });
    if (Notification.permission === "denied") return renderBrowserPush(false, "Notifications are blocked for this site in the browser's settings.");
    renderBrowserPush(false, "Off.");
  } catch (error) {
    renderBrowserPush(false, error.message);
  }
}
async function setBrowserPush(on) {
  app.browserPush.busy = true;
  renderBrowserPush(on, on ? "Turning on." : "Turning off.");
  try {
    if (on) {
      if ((await Notification.requestPermission()) !== "granted") throw Error("Notifications are blocked for this site in the browser's settings.");
      const { publicKey } = await signinRequest("/api/v1/browser/push");
      const key = pushKeyBytes(publicKey);
      const registration = await pushWorker();
      let subscription = await registration.pushManager.getSubscription();
      if (subscription && !sameKey(subscription, key)) {
        await subscription.unsubscribe();
        subscription = null;
      }
      subscription ||= await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
      await signinRequest("/api/v1/browser/push", { method: "PUT", body: JSON.stringify(subscription.toJSON()) });
    } else {
      await signinRequest("/api/v1/browser/push", { method: "DELETE" });
      const registration = await navigator.serviceWorker.getRegistration(pushWorkerScope);
      await (await registration?.pushManager.getSubscription())?.unsubscribe();
    }
  } catch (error) {
    toast(error.message);
  } finally {
    app.browserPush.busy = false;
    await loadBrowserPush();
  }
}
async function loadDevices() {
  try {
    const { devices } = await signinRequest("/api/v1/devices");
    app.devices = (devices || []).filter((device) => !(device.scopes || []).includes("host"));
  } catch (error) {
    app.devices = null;
    app.devicesError = error.message;
  }
  renderDevices();
}
function renderDevices() {
  const pairing = app.devicePairing;
  $("#device-pairing").innerHTML = pairing
    ? `<div class="remote-ready"><div class="remote-qr"><img src="${escape(pairing.qr)}" alt="Pairing code for the Agenthail iPhone app"></div><div><span class="status-pill busy">Waiting for the iPhone</span><p>Scan this with the iPhone's camera, or with Scan pairing code in Agenthail, before ${escape(new Date(pairing.expiresAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }))}.</p><div class="remote-actions"><a class="primary-button" href="${escape(pairing.pairingURL)}">Open in Agenthail</a><button class="soft-button" data-device-pair-cancel type="button">Cancel</button></div><small>On this iPhone, tap Open in Agenthail instead.</small></div></div>`
    : `<div class="remote-actions"><button class="primary-button" data-device-pair type="button">Pair an iPhone</button></div>${app.devicePairingError ? `<p class="signin-note">${escape(app.devicePairingError)}</p>` : ""}`;
  if (!app.devices) {
    $("#devices-list").innerHTML = `<div class="empty-card">${escape(app.devicesError || "Loading devices.")}</div>`;
    return;
  }
  $("#devices-list").innerHTML = app.devices.map((device) =>
    `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(device.name)}</div><div class="operation-detail">${device.lastSeenAt ? `Last seen ${escape(new Date(device.lastSeenAt).toLocaleString())}` : "Not seen yet"} · Notifications ${device.pushEnabled ? "on" : "off"}</div></div><button class="button quiet" data-device-remove="${escape(device.id)}" type="button">Remove</button></article>`,
  ).join("") || '<div class="empty-card">No iPhone is paired with this computer.</div>';
}
async function startDevicePairing() {
  if (app.devicePairingStarting || app.devicePairing) return;
  app.devicePairingStarting = true;
  app.devicePairingError = "";
  try {
    const pairing = await signinRequest("/api/v1/pairings", { method: "POST", body: JSON.stringify({ name: "", scopes: ["read", "control"], qr: true }) });
    app.devicePairing = pairing;
    renderDevices();
    watchDevicePairing(pairing.id);
  } catch (error) {
    app.devicePairingError = error.message;
    renderDevices();
  } finally {
    app.devicePairingStarting = false;
  }
}
function cancelDevicePairing() {
  app.devicePairing = null;
  renderDevices();
}
function expireDevicePairing() {
  app.devicePairing = null;
  app.devicePairingError = "The pairing code expired. Pair again to show a new one.";
  renderDevices();
}
async function watchDevicePairing(id) {
  while (app.devicePairing?.id === id) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    if (app.devicePairing?.id !== id) return;
    let status;
    try {
      status = await signinRequest(`/api/v1/pairings?id=${encodeURIComponent(id)}`);
    } catch {
      if (app.devicePairing?.id === id && Date.now() >= new Date(app.devicePairing.expiresAt).getTime()) expireDevicePairing();
      continue;
    }
    if (app.devicePairing?.id !== id) return;
    if (status.state === "paired") {
      app.devicePairing = null;
      toast(`Paired ${status.device?.name || "the iPhone"}.`);
      await loadDevices();
      return;
    }
    if (status.state === "expired") {
      expireDevicePairing();
      return;
    }
  }
}
async function removeDevice(button) {
  const device = (app.devices || []).find((item) => item.id === button.dataset.deviceRemove);
  if (!device || !confirm(`Disconnect ${device.name} from this computer?`)) return;
  button.disabled = true;
  try {
    await signinRequest("/api/v1/devices", { method: "DELETE", body: JSON.stringify({ id: device.id }) });
    toast(`Removed ${device.name}.`);
    await loadDevices();
  } catch (error) {
    toast(error.message);
    button.disabled = false;
  }
}
async function loadClaudeCompact() {
  try {
    const response = await fetch("/api/claude/compact-settings");
    if (!response.ok) throw Error(await response.text());
    app.claudeCompact = (await response.json()).roots || [];
    renderClaudeCompact();
  } catch (error) {
    $("#claude-compact-roots").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}
function claudeCompactWindowLabel(window) {
  if (!window) return "Claude's default";
  if (window.auto) return "auto";
  if (window.ignored) return `${window.ignored} (Claude ignores it)`;
  return `${Number(window.tokens).toLocaleString()} tokens`;
}
function claudeSettingLabel(value) {
  return value === undefined || value === null ? "not set" : value ? "on" : "off";
}
function renderClaudeCompact() {
  const roots = app.claudeCompact || [];
  $("#claude-compact-roots").innerHTML = roots.map((root) => {
    const models = Object.entries(root.modelWindows || {}).map(([model, window]) => `<p>${escape(model)}: ${escape(claudeCompactWindowLabel(window))}</p>`).join("");
    const overrides = Object.entries(root.overrides || {}).map(([name, value]) => `<p>${escape(name)}=${escape(value)} is set where Agenthail runs and takes precedence over this file.</p>`).join("");
    return `<article class="setting-row"><div><strong>${escape(root.settingsPath)}</strong><p>Window: ${escape(claudeCompactWindowLabel(root.autoCompactWindow))}</p>${models}<p>Autocompact: ${escape(claudeSettingLabel(root.autoCompactEnabled))} · Idle compaction: ${escape(claudeSettingLabel(root.idleCompaction))}</p>${overrides}</div></article>`;
  }).join("") || '<div class="empty-card">No Claude config folders.</div>';
  const select = $("#claude-compact-root");
  const chosen = select.value;
  select.innerHTML = roots.map((root) => `<option value="${escape(root.root)}">${escape(root.root)}</option>`).join("");
  if (roots.some((root) => root.root === chosen)) select.value = chosen;
}
async function loadSettings() {
  loadSignin();
  loadDevices();
  loadBrowserPush();
  loadClaudeCompact();
  try {
    const response = await fetch("/api/settings");
    if (!response.ok) throw Error(await response.text());
    app.settings = await response.json();
    renderSettings();
  } catch (error) {
    $("#remote-access-content").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}
function renderSettings() {
  if (!app.settings) return;
  const { dashboard, remoteAccess } = app.settings;
  $("#settings-codex-hours").value = String(dashboard.codexRecentHours);
  $("#settings-busy-delivery").value = String(dashboard.busyDelivery || "queue");
  $("#settings-resume-after-limit").checked = dashboard.resumeAfterLimit !== false;
  $("#settings-listener").textContent = dashboard.listen;
  renderSurfaceHealth();
  const remoteQR = app.remoteQRVisible
    ? `<div class="remote-qr"><img src="/api/settings/remote-qr" alt="QR code for this Agenthail dashboard"><button class="soft-button remote-qr-button" data-hide-remote-qr type="button">Hide QR</button></div>`
    : `<button class="remote-qr-cover" data-reveal-remote-qr type="button"><span>QR hidden</span><strong>Reveal phone QR</strong></button>`;
  const remoteName = app.remoteQRVisible
    ? `<p>${escape(remoteAccess.dnsName)}</p>`
    : `<p>Phone link hidden</p>`;
  $("#remote-access-content").innerHTML = remoteAccess.enabled
    ? `<div class="remote-ready">${remoteQR}<div><span class="status-pill idle">Connected</span>${remoteName}<div class="remote-actions"><button class="primary-button" data-copy-remote type="button">Copy phone link</button><button class="soft-button" data-remote-action="remote-disable" type="button">Turn off</button></div><small>On iPhone, open the link, tap Share, then Add to Home Screen.</small></div></div>`
    : `<div class="remote-off"><p>${escape(remoteAccess.error || "Use Tailscale to reach this dashboard from your own devices.")}</p><button class="primary-button" data-remote-action="remote-enable" type="button">Enable phone access</button></div>`;
}
function renderSurfaceHealth() {
  const surfaces = app.state.surfaces || [];
  $("#surface-health-list").innerHTML = surfaces.map((surface) => {
    const name = labels[surface.name] || surface.name;
    const health = surface.health || (surface.connected ? "healthy" : "unavailable");
    const state = health === "healthy" ? "Healthy" : health === "degraded" ? "Needs attention" : health === "signin_off" ? "Sign-in off" : health === "signin_unavailable" ? "No browser here" : "Unavailable";
    const rawDetail = surface.healthDetail || surface.error || "Ready";
    const detail = health === "signin_off"
      ? signinOffHint(rawDetail)
      : health === "signin_unavailable"
      ? rawDetail.replace(/^[^:]*: /, "")
      : /cookie bridge/i.test(rawDetail)
      ? "Claude Code needs to reconnect to your signed-in account."
      : /notion.*(?:401|context)/i.test(rawDetail)
        ? "Notion is not signed in on this computer."
        : /reachable but not supervised/i.test(rawDetail)
          ? "The Codex background service will stop after a restart."
          : rawDetail;
    const runtime = surface.runtime?.name
      ? `<p><strong>${escape(surface.runtime.name)}</strong> ${escape(surface.runtime.reachable ? surface.runtime.durable ? "stays available in the background" : "is open now but will not restart automatically" : "is not connected")}</p>${surface.runtime.remediation ? `<p>How to fix: ${escape(surface.runtime.remediation)}</p>` : ""}${(surface.runtime.notes || []).map((note) => `<p>${note.message === rawDetail ? "" : `${escape(note.message)}<br>`}${note.remediation ? `How to fix: ${escape(note.remediation)}` : ""}</p>`).join("")}`
      : "";
    const repair = surface.repairAction
      ? `<button class="soft-button" data-surface-repair="${escape(surface.repairAction)}" type="button">${escape(surface.repairLabel || "Repair")}</button>`
      : "";
    return `<article class="surface-health-row"><div class="surface-health-icon ${escape(health)}">${surfaceIcon(surface.name)}</div><div class="surface-health-copy"><div><strong>${escape(name)}</strong><span class="health-state ${escape(health)}">${escape(state)}</span></div><p>${escape(detail)}</p>${runtime}</div>${repair}</article>`;
  }).join("") || '<div class="empty-card">No surfaces are configured.</div>';
}
function hideRemoteQR() {
  if (app.remoteQRHideTimer) clearTimeout(app.remoteQRHideTimer);
  app.remoteQRHideTimer = null;
  if (!app.remoteQRVisible) return;
  app.remoteQRVisible = false;
  renderSettings();
}
function revealRemoteQR() {
  if (app.remoteQRHideTimer) clearTimeout(app.remoteQRHideTimer);
  app.remoteQRVisible = true;
  app.remoteQRHideTimer = setTimeout(hideRemoteQR, 60000);
  renderSettings();
}
async function createLoginLink() {
  const response = await fetch("/api/v1/dashboard/login-links", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ remote: true }) });
  if (!response.ok) throw Error((await response.json().catch(() => ({}))).error?.message || "Could not create a phone link.");
  return (await response.json()).url;
}
async function updateSettings(body) {
  const response = await fetch("/api/settings", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  if (!response.ok) throw Error(await response.text());
  await loadSettings();
}
function renderAudit() {
  const audit = app.audit;
  const kindSelect = $("#audit-kind-filter");
  kindSelect.innerHTML = ['<option value="all">All events</option>', ...audit.kinds.map((kind) => `<option value="${escape(kind)}">${escape(auditKindLabel(kind))}</option>`)].join("");
  kindSelect.value = audit.kinds.includes(audit.kind) ? audit.kind : "all";
  audit.kind = kindSelect.value;
  $("#history-list").innerHTML = audit.items.map((entry) => {
    const route = auditRoute(entry);
    const content = entry.error || entry.message || entry.result || "No message content recorded";
    return `<article class="audit-event"><div class="audit-marker ${entry.error ? "failed" : ""}"></div><div class="audit-event-body"><div class="audit-event-heading"><strong>${escape(auditHeading(entry))}</strong><time>${timeAgo(entry.createdAt)}</time></div><div class="audit-route">${route}</div><details><summary>View details</summary><div class="audit-detail${entry.error ? " operation-error" : ""}">${markdown(content)}</div></details></div></article>`;
  }).join("") || `<div class="empty-card">${audit.loading ? "Loading activity." : audit.loaded ? "No activity matches these filters." : "Open Audit to load activity."}</div>`;
  const more = $("#history-more");
  more.hidden = !audit.loaded || !audit.hasMore;
  more.disabled = audit.loading;
  more.textContent = audit.loading ? "Loading activity" : "Load older activity";
}
async function loadAudit(reset = false, silent = false) {
  if (app.audit.loading) return;
  const previous = silent ? { items: app.audit.items, nextBefore: app.audit.nextBefore, hasMore: app.audit.hasMore } : null;
  if (reset) app.audit = { ...app.audit, items: silent ? app.audit.items : [], nextBefore: 0, hasMore: true, loaded: silent && app.audit.loaded };
  if (!app.audit.hasMore) return;
  app.audit.loading = true;
  renderAudit();
  try {
    const params = new URLSearchParams({ limit: "25" });
    if (app.audit.nextBefore) params.set("before", app.audit.nextBefore);
    if (app.audit.kind !== "all") params.set("kind", app.audit.kind);
    if (app.audit.query) params.set("q", app.audit.query);
    const response = await fetch(`/api/history?${params}`);
    if (!response.ok) throw Error(await response.text());
    const page = await response.json();
    const previousIDs = previous ? new Set(previous.items.map((entry) => entry.id)) : null;
    const overlapsPrevious = previousIDs ? page.items.some((entry) => previousIDs.has(entry.id)) : false;
    if (reset) {
      if (previous) {
        const fresh = new Set(page.items.map((entry) => entry.id));
        app.audit.items = [...page.items, ...previous.items.filter((entry) => !fresh.has(entry.id))];
      } else {
        app.audit.items = page.items;
      }
    } else {
      const known = new Set(app.audit.items.map((entry) => entry.id));
      app.audit.items.push(...page.items.filter((entry) => !known.has(entry.id)));
    }
    app.audit.nextBefore = previous && overlapsPrevious ? previous.nextBefore : page.nextBefore || 0;
    app.audit.hasMore = previous && overlapsPrevious ? previous.hasMore : page.hasMore === true;
    app.audit.kinds = page.kinds || [];
    app.audit.loaded = true;
  } catch (error) {
    toast(friendlyError(error));
  } finally {
    app.audit.loading = false;
    renderAudit();
  }
}
function renderAll() {
  try {
    renderOverview();
  } catch (error) {
    console.error("dashboard overview render failed", error);
    $("#surface-cards").innerHTML =
      `<div class="empty-card">Could not render connected surfaces: ${escape(error.message || error)}</div>`;
  }
  renderSurfaceFilter();
  renderStartSurfaceOptions();
  renderSessions();
  renderOperations();
  if (app.settings && app.operationsTab === "settings") renderSettings();
  $("#sync").textContent =
    `Synced ${new Date(app.state.updatedAt || Date.now()).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
}
async function load(fresh = false) {
  if (app.loading) return;
  app.loading = true;
  const slowTimer = setTimeout(() => {
    if (app.loading && !app.hasState) {
      $("#sync").textContent = "Still connecting";
      $("#daemon-detail").textContent =
        "Surface discovery is taking a little longer";
      $("#surface-cards").innerHTML =
        '<div class="empty-state compact">Connecting to Claude, Codex, and Notion.</div>';
    }
  }, 2000);
  try {
    await loadTools();
    const response = await fetch(`/api/state${fresh ? "?fresh=1" : ""}`);
    if (!response.ok) throw Error(await response.text());
    const state = await response.json();
    app.state = {
      ...state,
      surfaces: state.surfaces || [],
      sessions: state.sessions || [],
      queue: state.queue || [],
      channels: state.channels || [],
      relays: state.relays || [],
      history: state.history || [],
      attention: state.attention || [],
      deliveryProblems: state.deliveryProblems || [],
    };
    $("#daemon-presence").className = app.state.daemon?.running
      ? "daemon-presence online"
      : "daemon-presence offline";
    app.hasState = true;
    const rekeyed = state.rekeyed || {};
    applyRekeys(rekeyed);
    let followSelection = null;
    if (app.selected) {
      const replacement = app.state.sessions.find(
        (session) => session.id === app.selected.id,
      );
      if (replacement) app.selected = replacement;
      else if (rekeyed[app.selected.id]) {
        followSelection = rekeyed[app.selected.id];
        app.drafts.set(followSelection, $("#message").value);
        stopLiveStream(true);
        app.selected = null;
      }
    }
    renderAll();
    if (followSelection) await selectSession(followSelection);
    if (app.audit.loaded) await loadAudit(true, true);
    if (app.selected && app.history) renderChat();
    if (!app.selected && location.hash === "#conversations" && !mobileConversationView()) {
      const first = scopedSessions()[0] || app.state.sessions[0];
      if (first) selectSession(first.id);
    }
  } catch (error) {
    const message = friendlyError(error);
    $("#daemon-status").textContent = "Not connected";
    $("#daemon-presence").className = "daemon-presence offline";
    $("#daemon-detail").textContent =
      "Restart Agenthail from the menu bar, then refresh";
    $("#surface-cards").innerHTML =
      `<div class="empty-state compact">${escape(message)}</div>`;
    $("#recent-activity").innerHTML = "";
    toast(message);
  } finally {
    clearTimeout(slowTimer);
    app.loading = false;
  }
}
async function selectSession(id, focus = false) {
  if (app.selected?.id !== id) { for (const selector of ["#turn-effort", "#turn-mode", "#turn-tier", "#turn-schema"]) $(selector).value = ""; $("#session-operation-result").textContent = ""; $("#session-operation-form").reset(); }
  const session = app.state.sessions.find((item) => item.id === id) || app.codexSearch.results.find((item) => item.id === id);
  if (!session) return;
  if (app.selected?.id !== id) stopLiveStream(true);
  if (app.selected) app.drafts.set(app.selected.id, $("#message").value);
  app.selected = session;
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
    const response = await fetch(
      `/api/session?id=${encodeURIComponent(id)}&limit=20&timeline=1`,
    );
    if (!response.ok) throw Error(await response.text());
    const detail = await response.json();
    if (app.selected?.id !== id) return;
    detail.transcriptWarning = detail.readError || "";
    app.history = detail;
    renderChat();
    app.allSubagents = false;
    loadSessionMetadata(id, detail);
    if (focus && mobileConversationView())
      requestAnimationFrame(() => window.scrollTo(0, 0));
  } catch (error) {
    $("#chat-body").innerHTML =
      `<div class="empty-state"><h2>Could not load this conversation</h2><p>${escape(error.message)}</p></div>`;
  }
}
function loadSessionMetadata(id, detail) {
  const metadataSeq = detail.journalSeq;
  fetch(`/api/session-metadata?id=${encodeURIComponent(id)}${app.allSubagents ? "&subagents=all" : ""}`)
    .then((result) => { if (!result.ok) throw Error("Metadata unavailable"); return result.json(); })
    .then((metadata) => { if (app.selected?.id === id && app.history === detail) { applySessionMetadata(detail, metadata, metadataSeq); renderChat(); } })
    .catch(() => {});
}
function applySessionMetadata(detail, metadata, initialSeq) {
  const {goal, context, ...other} = metadata;
  Object.assign(detail, other);
  if (detail.journalSeq === initialSeq) {
    if (Object.hasOwn(metadata, "goal")) detail.goal = goal;
    if (Object.hasOwn(metadata, "context")) detail.context = context;
  }
}
function renderImageAttachment(item, session) {
  const attachment = item.attachment;
  const fallback = escape(item.text || "Image attachment");
  if (!attachment?.id || !/^image\/(png|jpeg|gif|webp|avif|heic|heif)$/.test(attachment.mediaType || ""))
    return `<div class="turn"><p>${fallback}</p></div>`;
  const url = `/api/session-attachment?sessionId=${encodeURIComponent(session.id)}&id=${encodeURIComponent(attachment.id)}`;
  return `<figure class="turn transcript-image"><img src="${escape(url)}" loading="lazy" decoding="async" alt="${fallback}" referrerpolicy="no-referrer"><figcaption>${fallback}</figcaption></figure>`;
}
function timelineKindClass(kind) {
  return String(kind || "unknown").replace(/[^a-zA-Z0-9_-]/g, "-");
}
function timelineItemAttributes(item) {
  const attributes = [`data-timeline-item="${escape(item.id || "")}"`];
  if (item.callId) attributes.push(`data-call-id="${escape(item.callId)}"`);
  if (item.status) attributes.push(`data-status="${escape(item.status)}"`);
  return attributes.join(" ");
}
// Titles, call ids, statuses and raw timestamps stay in data attributes for
// debugging; a reader only needs to know the host shortened a record.
function renderTimelineMetadata(item) {
  return item.truncated ? '<div class="timeline-meta">Shortened by the host</div>' : "";
}
// isInstructions reports model input a provider injected, such as project
// instructions or hook context, rather than conversation.
function isInstructions(item) {
  return item.kind === "instructions" || (["message", "text"].includes(item.kind) && ["developer", "system"].includes(item.role));
}
function renderTimelineItem(item, session, turnEnd = null) {
  const kind = String(item.kind || "unknown");
  const kindClass = timelineKindClass(kind);
  const attributes = timelineItemAttributes(item);
  const metadata = renderTimelineMetadata(item);
  if (kind === "attachment") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderImageAttachment(item, session)}${metadata}</section>`;
  }
  if (isInstructions(item)) {
    const body = item.text ? markdown(item.text) : '<p class="timeline-empty">No recorded content.</p>';
    return `<details class="timeline-item timeline-instructions timeline-collapsible" ${attributes}><summary>Instructions</summary>${metadata}<div class="turn-content">${body}</div></details>`;
  }
  if (kind === "message" && item.role === "peer") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderMessage(item.text, "peer", `From ${item.sender || "another agent"}`, `${session.id}:${item.id}`, [], clockTime(item.timestamp))}${metadata}</section>`;
  }
  if (["text", "message", "assistant"].includes(kind)) {
    const user = item.role === "user";
    const content = user ? handoffMessage(item.text) : { text: item.text, label: labels[session.surface] || session.surface };
    const kindName = !user ? "agent" : content.label.startsWith("From ") ? "peer" : "user";
    const footer = user ? clockTime(item.timestamp) : turnEnd ? turnFooter(turnEnd.text, turnEnd.timestamp || item.timestamp) : "";
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderMessage(content.text, kindName, content.label, `${session.id}:${item.id}`, user ? item.files : [], footer)}${metadata}</section>`;
  }
  if (kind === "event") {
    const text = [item.title, item.status].filter(Boolean).join(" · ") + (item.text ? `: ${item.text}` : "");
    return `<p class="timeline-item timeline-divider" ${attributes}><span>${escape(text)}</span>${metadata}</p>`;
  }
  if (kind === "done") return "";
  if (kind === "notice") {
    const resets = Date.parse(item.resetsAt || "");
    const until = resets ? ` Resets ${new Date(resets).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}.` : "";
    return `<div class="timeline-item timeline-notice" role="status" ${attributes}><strong>${escape(item.title || "Notice")}</strong> ${escape(item.text || "")}${escape(until)}${metadata}</div>`;
  }
  if (toolKinds.includes(kind)) return renderToolRun([item], session);
  const title = item.title || "Unknown timeline item";
  const body = item.text ? markdown(item.text) : '<p class="timeline-empty">No recorded content.</p>';
  return `<article class="turn timeline-item timeline-${kindClass} timeline-unknown" ${attributes}><header class="turn-header"><span class="turn-label">${escape(title)}</span><span class="timeline-kind">${escape(kind)}</span></header>${metadata}<div class="turn-content">${body}</div></article>`;
}
function clockTime(timestamp) {
  const date = new Date(timestamp || "");
  if (Number.isNaN(date.getTime())) return "";
  const today = new Date().toDateString() === date.toDateString();
  return date.toLocaleString([], today ? { hour: "numeric", minute: "2-digit" } : { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}
// "3m13s" reads as "3:13"; anything unparsed is shown as recorded.
function durationClock(value) {
  const text = String(value || "");
  const match = text.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/);
  if (!text || !match) return text;
  const [hours, minutes, seconds] = [match[1], match[2], match[3]].map(part => Number(part || 0));
  const pad = number => String(number).padStart(2, "0");
  return hours ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}
function turnFooter(duration, timestamp) {
  return [duration ? `Finished ${durationClock(duration)}` : "", clockTime(timestamp)].filter(Boolean).join(" · ");
}
const toolKinds = ["toolCall", "toolResult", "reasoning", "tool", "command"];
const foldToolsAfter = 6;
function isFailedTool(item) {
  return item.status === "failed" || item.status === "error";
}
// What the reader opened, closed or paged in tool activity, by session and row, so a re-render keeps it.
const toolRows = new Map();
function toolRowKey(session, id) {
  return `${session?.id || ""} ${id}`;
}
function renderToolLine(line, failed, attributes, body, key, open, collapse = "") {
  const object = line.object ? `<span class="tool-object${line.code ? " code" : ""}">${escape(line.object)}</span>` : "";
  const end = collapse ? renderCollapse(collapse) : "";
  return `<details class="timeline-item tool-line${failed ? " failed" : ""}" ${attributes} data-row="${escape(key)}"${(toolRows.get(key) ?? open) ? " open" : ""}><summary><span class="tool-mark" aria-hidden="true">◆</span><span class="tool-verb">${escape(line.verb)}</span>${object}${failed ? '<span class="tool-failed">failed</span>' : ""}</summary><div class="tool-body">${body}${end}</div></details>`;
}
function renderCollapse(title) {
  return `<button class="tool-collapse" type="button" data-collapse-row>${escape(title)}</button>`;
}
function renderToolOutput(result, session) {
  const metadata = renderTimelineMetadata(result);
  const failed = isFailedTool(result);
  const key = toolRowKey(session, `output:${result.id}`);
  const text = result.text || "";
  const lines = tools.outputLines(text);
  const preview = tools.outputPreview(text);
  const shown = preview.length < text.length ? Math.min(toolRows.get(key) || 0, lines.length) : 0;
  const more = shown < lines.length ? `<button class="tool-control" type="button" data-output-show="${shown + tools.outputPage}">${shown ? `Show ${Math.min(tools.outputPage, lines.length - shown)} more lines` : `Show all ${lines.length} lines`}</button>` : "";
  const paging = preview.length < text.length ? more + (shown ? '<button class="tool-control" type="button" data-output-show="0">Show less</button>' : "") : "";
  const copy = text ? `<button class="tool-control" type="button" data-copy-text="${escape(text)}">Copy output</button>` : "";
  const body = text ? `<pre class="tool-output${failed ? " failed" : ""}${shown ? " paged" : ""}">${escape(shown ? lines.slice(0, shown).join("\n") : preview)}</pre>` : '<p class="timeline-empty">The tool returned an empty result.</p>';
  // The pages can run long, so the same controls follow them; Show less there brings the output's top back.
  const end = shown ? `<div class="tool-result-controls">${more}<button class="tool-control" type="button" data-output-show="0" data-reveal>Show less</button></div>` : "";
  return `<div class="tool-result" ${timelineItemAttributes(result)} data-row="${escape(key)}"><div class="tool-result-head"><span class="tool-result-label">${failed ? "Error output" : "Output"}</span>${paging}${copy}</div>${body}${end}${metadata}</div>`;
}
// Pages a tool's output in place, without re-rendering the transcript around it.
function showToolOutput(button) {
  const block = button.closest(".tool-result[data-row]");
  const result = (app.history?.timeline?.items || []).find((item) => item.id === block?.dataset.timelineItem);
  if (!block || !result) return;
  toolRows.set(block.dataset.row, Number(button.dataset.outputShow) || 0);
  const reveal = button.hasAttribute("data-reveal");
  block.outerHTML = renderToolOutput(result, app.selected);
  if (reveal) $("#chat-body").querySelector(`.tool-result[data-row="${CSS.escape(block.dataset.row)}"]`)?.scrollIntoView({ block: "nearest" });
}
// Folds the row a bottom control belongs to and scrolls its header back into view.
function collapseToolRow(button) {
  const row = button.closest("details[data-row]");
  if (!row) return;
  row.open = false;
  toolRows.set(row.dataset.row, false);
  row.querySelector(":scope > summary")?.scrollIntoView({ block: "nearest" });
}
function rememberToolRow(event) {
  const row = event.target;
  if (row.matches?.("details[data-row]")) toolRows.set(row.dataset.row, row.open);
}
// Blocks a control re-renders in place, by key: a file's diff and a run's file chips.
const toolBlocks = new Map();
function rerenderToolBlock(block) {
  const render = toolBlocks.get(block?.dataset.block);
  if (render) block.outerHTML = render();
}
// Each call's diffs, kept while its text is unchanged so a transcript update does not work them out again.
const toolDiffCache = new Map();
function cachedToolDiffs(item, session) {
  const key = toolRowKey(session, item.id);
  const cached = toolDiffCache.get(key);
  if (cached?.title === item.title && cached.text === item.text) return cached.diffs;
  const diffs = tools.toolDiffs(item);
  toolDiffCache.set(key, { title: item.title, text: item.text, diffs });
  return diffs;
}
// Rows laid out per "Show more", as the Mac's DiffView.page.
const diffPage = 400;
function renderDiff(key, hunks) {
  toolBlocks.set(key, () => renderDiff(key, hunks));
  const entries = tools.diffEntries(hunks);
  const shown = Math.max(toolRows.get(key) || diffPage, diffPage);
  const rows = entries.slice(0, shown).map((entry) => {
    if (entry.hunk) return '<div class="diff-note">⋯</div>';
    if (entry.folded) return `<div class="diff-note">${entry.folded === 1 ? "1 unchanged line" : `${entry.folded} unchanged lines`}</div>`;
    const spoken = { added: "Added: ", removed: "Removed: ", same: "" }[entry.kind];
    return `<div class="diff-line ${entry.kind}"><span class="diff-sign" aria-hidden="true">${{ added: "+", removed: "−", same: " " }[entry.kind]}</span><span class="diff-text">${spoken ? `<span class="sr-only">${spoken}</span>` : ""}${markedText(entry.text, entry.marks)}</span></div>`;
  }).join("");
  const more = entries.length > shown ? `<button class="tool-control diff-more" type="button" data-diff-show="${shown + diffPage}">Show ${Math.min(diffPage, entries.length - shown)} more lines</button>` : "";
  return `<div class="tool-diff" data-block="${escape(key)}">${rows}${more}</div>`;
}
// Marks are character offsets, so the text is split the way Swift counts characters.
function markedText(text, marks) {
  if (!marks.length) return escape(text);
  const characters = Array.from(new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(text), ({ segment }) => segment);
  let html = "";
  let at = 0;
  for (const [start, end] of marks) {
    html += `${escape(characters.slice(at, start).join(""))}<mark>${escape(characters.slice(start, end).join(""))}</mark>`;
    at = end;
  }
  return html + escape(characters.slice(at).join(""));
}
function renderFileChips(key, diffs) {
  toolBlocks.set(key, () => renderFileChips(key, diffs));
  const isOpen = (diff) => toolRows.get(`${key}:${diff.path}`) === true;
  const buttons = diffs.map((diff) => {
    const change = tools.fileChange(diff);
    const name = tools.baseName(change.path);
    return `<button class="file-chip${isOpen(diff) ? " open" : ""}" type="button" data-chip="${escape(change.path)}" aria-expanded="${isOpen(diff)}" title="${escape(change.path)}" aria-label="${escape(`${name}, ${change.added} added, ${change.removed} removed`)}"><span>${escape(name)}</span>${change.added ? `<span class="added">+${change.added}</span>` : ""}${change.removed ? `<span class="removed">−${change.removed}</span>` : ""}</button>`;
  }).join("");
  const opened = diffs.filter(isOpen).map((diff) => `${diffs.length > 1 ? `<div class="diff-file">${escape(tools.baseName(diff.path))}</div>` : ""}${renderDiff(`${key}:${diff.path}:diff`, diff.hunks)}`).join("");
  return `<div class="file-chips" data-block="${escape(key)}"><div class="file-chip-list">${buttons}</div>${opened}</div>`;
}
function toggleFileChip(button) {
  const block = button.closest(".file-chips[data-block]");
  if (!block) return;
  const key = `${block.dataset.block}:${button.dataset.chip}`;
  toolRows.set(key, toolRows.get(key) !== true);
  rerenderToolBlock(block);
}
function showToolDiff(button) {
  const block = button.closest(".tool-diff[data-block]");
  if (!block) return;
  toolRows.set(block.dataset.block, Number(button.dataset.diffShow) || diffPage);
  rerenderToolBlock(block);
}
function renderToolRun(items, session, states = new Map()) {
  const entries = [];
  const calls = new Map();
  for (const item of items) {
    if (item.kind === "toolResult" && item.callId && calls.has(item.callId)) {
      calls.get(item.callId).results.push(item);
    } else if (item.kind === "toolResult" || item.kind === "reasoning") {
      entries.push({ item, results: [] });
    } else {
      const entry = { item, results: [] };
      if (item.callId) calls.set(item.callId, entry);
      entries.push(entry);
    }
  }
  const lines = entries.map(({ item, results }) => {
    const attributes = timelineItemAttributes(item);
    if (item.kind === "reasoning") {
      const body = item.text ? `<div class="turn-content">${markdown(item.text)}</div>` : '<p class="timeline-empty">No recorded content.</p>';
      return renderToolLine({ verb: "Thought", object: tools.firstLine(item.text), code: false }, false, attributes, body + renderTimelineMetadata(item), toolRowKey(session, item.id), false);
    }
    if (item.kind === "toolResult") {
      return renderToolLine({ verb: "Output", object: tools.firstLine(item.text), code: true }, isFailedTool(item), attributes, renderToolOutput(item, session), toolRowKey(session, item.id), isFailedTool(item));
    }
    const failed = isFailedTool(item) || results.some(isFailedTool);
    const diffs = cachedToolDiffs(item, session);
    const input = diffs.length
      ? diffs.map((diff) => `${diffs.length > 1 ? `<div class="diff-file">${escape(tools.baseName(diff.path))}</div>` : ""}${renderDiff(toolRowKey(session, `diff:${item.id}:${diff.path}`), diff.hunks)}`).join("")
      : item.text ? `<pre class="tool-input">${escape(tools.readableInput(item.text))}</pre>` : '<p class="timeline-empty">No recorded input.</p>';
    const outputs = results.length ? results.map((result) => renderToolOutput(result, session)).join("") : `<p class="timeline-empty">${escape(tools.callStateLabels[states.get(item.id) || "unrecorded"])}</p>`;
    return renderToolLine(tools.toolLine(item, diffs), failed, attributes, (failed ? "" : input + renderTimelineMetadata(item)) + outputs, toolRowKey(session, item.id), failed, "Collapse tool");
  });
  const diffs = tools.mergedDiffs(entries.flatMap(({ item, results }) => (item.kind === "reasoning" || item.kind === "toolResult" || isFailedTool(item) || results.some(isFailedTool) ? [] : cachedToolDiffs(item, session))));
  const chips = diffs.length ? renderFileChips(toolRowKey(session, `chips:${entries[0].item.id}`), diffs) : "";
  const callCount = entries.filter(entry => entry.item.kind !== "reasoning" && entry.item.kind !== "toolResult").length;
  if (callCount <= foldToolsAfter) return `<section class="tool-run">${lines.join("")}${chips}</section>`;
  const failures = entries.filter(({ item, results }) => isFailedTool(item) || results.some(isFailedTool)).length;
  const summary = `<summary><span class="tool-mark" aria-hidden="true">◆</span><span class="tool-verb">${escape(toolRunLabel(entries, session))}</span>${failures ? `<span class="tool-failed">${failures} failed</span>` : ""}</summary>`;
  const key = toolRowKey(session, `run:${entries[0].item.id}`);
  return `<section class="tool-run"><details class="tool-line tool-fold" data-row="${escape(key)}"${toolRows.get(key) ? " open" : ""}>${summary}<div class="tool-fold-lines">${lines.join("")}${renderCollapse("Collapse activity")}</div></details>${chips}</section>`;
}
function toolRunLabel(entries, session) {
  const counts = new Map();
  for (const { item } of entries) {
    if (item.kind === "reasoning" || item.kind === "toolResult") continue;
    const verb = tools.toolLine(item, cachedToolDiffs(item, session)).verb;
    counts.set(verb, (counts.get(verb) || 0) + 1);
  }
  const phrases = { Run: ["ran 1 command", "ran %d commands"], Read: ["read 1 file", "read %d files"], Edit: ["edited 1 file", "edited %d files"], Write: ["wrote 1 file", "wrote %d files"], Search: ["searched once", "searched %d times"] };
  const parts = [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([verb, count]) => {
    const phrase = phrases[verb];
    return phrase ? (count === 1 ? phrase[0] : phrase[1].replace("%d", count)) : `${verb} ×${count}`;
  });
  const text = parts.slice(0, 2).join(" and ") + (parts.length > 2 ? ` +${parts.length - 2} more` : "");
  return text.charAt(0).toUpperCase() + text.slice(1);
}
function renderTimeline(items, session) {
  toolBlocks.clear();
  const out = [];
  let run = [];
  const states = tools.toolCallStates(items, session.status === "busy" || Boolean(session.waitingOn));
  const flush = () => {
    if (run.length) out.push(renderToolRun(run, session, states));
    run = [];
  };
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (toolKinds.includes(item.kind)) {
      run.push(item);
      continue;
    }
    flush();
    if (item.kind === "event" && item.title === "Turn duration") {
      out.push(`<p class="timeline-item timeline-annotation" ${timelineItemAttributes(item)}>${escape(turnFooter(item.text, item.timestamp))}</p>`);
      continue;
    }
    const next = items[index + 1];
    const ends = next?.kind === "event" && next.title === "Turn duration";
    out.push(renderTimelineItem(item, session, ends ? next : null));
    if (ends && ["text", "message", "assistant"].includes(item.kind) && item.role !== "user" && item.role !== "peer" && !isInstructions(item)) index += 1;
  }
  flush();
  return out.join("");
}
function timelineSignature(session, exchanges, goal, capabilities, transcriptWarning, timeline, claudeRuns, claudeSubagents, claudeSubagentsTotal, allSubagents) {
  return JSON.stringify([
    session.id,
    session.status === "busy" || Boolean(session.waitingOn),
    exchanges,
    JSON.stringify(goal || null),
    Boolean(capabilities.goal),
    Boolean(capabilities.goalSet),
    Boolean(capabilities.goalStatus),
    transcriptWarning,
    timeline,
    claudeRuns,
    claudeSubagents,
    claudeSubagentsTotal,
    allSubagents,
  ]);
}
const claudeSubagentsShown = 8;
// A host from before subagent paging sends its whole list and no total.
function claudeSubagentList(links, total, expanded) {
  const ordered = links
    .map((link, index) => ({ link, index }))
    .sort((a, b) => (Boolean(b.link.working) - Boolean(a.link.working)) || ((Date.parse(b.link.lastActive || "") || 0) - (Date.parse(a.link.lastActive || "") || 0)) || (a.index - b.index))
    .map(({ link }) => link);
  const working = ordered.filter((link) => link.working).length;
  const count = Math.max(Number.isInteger(total) ? total : ordered.length, ordered.length);
  const shown = expanded ? ordered : ordered.slice(0, working + claudeSubagentsShown);
  return { shown, working, total: count, earlier: count - shown.length };
}
function claudeSubagentTitle(link) {
  return link.description || link.agentType || `Subagent ${String(link.agentId || "").slice(0, 8)}`;
}
function renderClaudeMetadata(claudeRuns, claudeSubagents, claudeSubagentsTotal, allSubagents = false) {
  if (!Array.isArray(claudeRuns) && !Array.isArray(claudeSubagents)) return "";
  const runs = Array.isArray(claudeRuns) ? claudeRuns : [];
  const list = claudeSubagentList(Array.isArray(claudeSubagents) ? claudeSubagents : [], claudeSubagentsTotal, allSubagents);
  const runRows = runs.map((run) => `<li>${run.jobId ? `<span>Job: ${escape(run.jobId)}</span>` : ""}${run.runType ? ` · <span>Type: ${escape(run.runType)}</span>` : ""}${run.providerState ? ` · <span>Provider state: ${escape(run.providerState)}</span>` : ""}${run.sessionId ? ` · <span>Session: ${escape(run.sessionId)}</span>` : ""}${run.resumeSessionId ? ` · <span>Resume session: ${escape(run.resumeSessionId)}</span>` : ""}${run.createdAt ? ` · <time datetime="${escape(run.createdAt)}">Created ${escape(run.createdAt)}</time>` : ""}${run.updatedAt ? ` · <time datetime="${escape(run.updatedAt)}">Updated ${escape(run.updatedAt)}</time>` : ""}${run.recordPath ? ` · <span>Record: ${escape(run.recordPath)}</span>` : ""}</li>`).join("");
  const subagentRows = list.shown.map((link) => `<li><span>${escape(claudeSubagentTitle(link))}</span>${link.description && link.agentType ? ` · <span>${escape(link.agentType)}</span>` : ""}${link.working ? " · <span>Working</span>" : link.lastActive ? ` · <time datetime="${escape(link.lastActive)}">${escape(timeAgo(link.lastActive))}</time>` : ""}</li>`).join("");
  const noun = list.total === 1 ? "subagent" : "subagents";
  const summary = list.working ? `${list.working} working, ${list.total} ${noun} in all` : `${list.total} ${noun} in all`;
  const earlier = list.earlier > 0 ? `<button class="soft-button" data-subagents-all type="button">Show ${list.earlier} earlier</button>` : "";
  return `<details class="session-details"><summary>Observed Claude runs</summary>${runRows ? `<ul>${runRows}</ul>` : "<p>No observed Claude runs.</p>"}</details><details class="session-details"${allSubagents ? " open" : ""}><summary>Observed Claude subagents${list.total ? ` · ${escape(summary)}` : ""}</summary>${subagentRows ? `<ul>${subagentRows}</ul>${earlier}` : "<p>No observed Claude subagents.</p>"}</details>`;
}
function renderChat() {
  const { exchanges = [], goal, model, models = [], capabilities = {}, readOnly, readOnlyReason, context, transcriptWarning, claudeRuns, claudeSubagents, claudeSubagentsTotal } = app.history || {};
  const session = app.selected;
  const controls = [];
  if (session.runtime?.focusable)
    controls.push('<button data-action="session-focus" type="button">Open session</button>');
  if (session.status === "busy" && !session.waitingOn && capabilities.steer)
    controls.push('<button data-action="steer" type="button">Steer</button>');
  if (session.status === "busy" && capabilities.interrupt)
    controls.push(
      '<button data-action="interrupt" type="button">Stop</button>',
    );
  if (capabilities.compact)
    controls.push(
      '<button data-action="compact" type="button">Compact</button>',
    );
  if (session.limitResume) {
    controls.push('<button data-action="limit-resume" type="button">Resume now</button>');
    if (session.limitResume.state === "waiting")
      controls.push('<button data-action="limit-resume-cancel" type="button">Don\'t resume</button>');
  }
  controls.push(`<button data-archive-action="${session.archived ? "session-unarchive" : "session-archive"}" type="button">${session.archived ? "Unarchive" : "Archive"}</button>`);
  if (session.cwd && !(session.project?.added && session.project.rootPath === session.cwd))
    controls.push(`<button data-project-add="${escape(session.cwd)}" data-project-host="${escape(checkoutHost(session))}" type="button">Add folder as project</button>`);
  if (session.project && !session.subagent && (session.surface === "claude" || session.surface === "codex") && !(app.state.projects || []).some((project) => project.id === session.project.id && (project.coordinators || []).some((lead) => lead.sessionId === session.id)))
    controls.push('<button data-make-lead type="button">Make project lead</button>');
  $("#chat-actions").innerHTML = mobileConversationView()
    ? ""
    : controls.join("");
  app.slashCommands = [
    ["/name", "Name this conversation"],
    capabilities.compact && ["/compact", "Compact this conversation"],
    session.status === "busy" && capabilities.interrupt && ["/stop", "Stop the current turn"],
    session.status === "busy" && !session.waitingOn && capabilities.steer && ["/steer", "Redirect the current turn"],
    capabilities.model && ["/model", "Choose the active model"],
    capabilities.goalSet && ["/goal", "Set the session goal"],
  ].filter(Boolean);
  if (readOnly) {
    $("#message").disabled = true;
    $("#send").disabled = true;
    $("#message").placeholder = "Read only";
    $("#composer-note").textContent = readOnlyReason;
  }
  syncComposerAction();
  renderSlashMenu();
  syncRequests();
  renderBackgroundTasks(session);
  $("#chat-subtitle").textContent = conversationMeta(session, model, app.history?.pendingModel ?? session.pendingModel ?? "");
  $("#thread-count").textContent =
    `${exchanges.length} recent exchange${exchanges.length === 1 ? "" : "s"}`;
  renderContextUsage(context);
  startLiveStream();
  const settings = [`<form class="session-tools" data-tool="alias"><label><span>Conversation name</span><input name="alias" value="${escape(session.alias || "")}" maxlength="80" placeholder="research"></label><div class="session-tool-actions"><button class="soft-button" type="submit">Save name</button></div></form>`];
  if (capabilities.goalSet) {
    const goalActions = [];
    if (capabilities.goal && goal?.status === "active") goalActions.push('<button class="soft-button" data-action="goal-pause" type="button">Pause</button>');
    if (capabilities.goal && goal?.status === "paused") goalActions.push('<button class="soft-button" data-action="goal-resume" type="button">Resume</button>');
    if (goal?.objective) goalActions.push('<button class="soft-button" data-action="goal-clear" type="button">Clear</button>');
    if (capabilities.goal && goal?.tokenBudget != null) goalActions.push('<button class="soft-button" data-action="goal-budget" data-goal-budget-clear="true" type="button">Clear budget</button>');
    const metrics = goalMetrics(goal);
    settings.push(`<form class="session-tools" data-tool="goal"><label><span>Goal</span><input name="goal" value="${escape(goal?.objective || "")}" placeholder="Set a focused goal"></label>${metrics}<div class="session-tool-actions"><button class="soft-button" type="submit">${goal?.objective ? "Edit goal" : "Save goal"}</button>${goalActions.join("")}</div></form>`);
    if (capabilities.goal) settings.push(`<form class="session-tools" data-tool="goal-budget"><label><span>Token budget</span><input name="tokenBudget" type="number" min="0" step="1" value="${goal?.tokenBudget ?? ""}" placeholder="Optional"></label><div class="session-tool-actions"><button class="soft-button" type="submit">Save budget</button></div></form>`);
  } else if (capabilities.goalStatus && goal?.objective) {
    settings.push(`<div class="session-tools session-goal-status" data-tool="goal-status"><label><span>Goal</span><input value="${escape(goal.objective)}" readonly></label>${goalMetrics(goal)}</div>`);
  }
  const toolRows = [
    `<details class="session-details"><summary>Conversation settings</summary>${settings.join("")}</details>`,
    remoteHostName(session) ? "" : `<details class="session-details"><summary>Voice</summary><p>Call this exact conversation through Codex Voice, or transfer an active call here.</p><button class="soft-button" type="button" data-voice-session="${escape(session.id)}">Call this session</button></details>`,
  ];
  if (session.surface === "claude") toolRows.push(renderClaudeMetadata(claudeRuns, claudeSubagents, claudeSubagentsTotal, app.allSubagents));
  const timeline = app.history?.timeline?.items || [];
  const signature = timelineSignature(session, exchanges, goal, capabilities, transcriptWarning, timeline, claudeRuns, claudeSubagents, claudeSubagentsTotal, app.allSubagents);
  if (app.transcriptSignature === signature) return;
  const chatBody = $("#chat-body");
  const previousScrollTop = chatBody.scrollTop;
  const wasPinned =
    chatBody.scrollHeight - chatBody.scrollTop - chatBody.clientHeight <= 24;
  const messages = timeline.length
    ? renderTimeline(timeline, session)
    : exchanges
    .flatMap((exchange, index) => {
      const user = exchangePrompt(exchange);
      const assistant = String(exchange.assistant || "");
      return [
        exchange.user
          ? renderMessage(
              user.text,
              "user",
              user.label,
              `${session.id}:user:${index}`,
            )
          : "",
        assistant
          ? renderMessage(
              assistant,
              "agent",
              labels[session.surface] || session.surface,
              `${session.id}:agent:${index}`,
            )
          : "",
      ];
    })
    .join("");
  chatBody.innerHTML =
    `${transcriptWarning ? `<p role="status">${escape(transcriptWarning)}</p>` : ""}${renderGoalAttention(goal)}${toolRows.join("")}${messages || '<div class="empty-state"><span class="empty-glyph">✦</span><h2>No saved exchanges yet</h2><p>Send a message to start this conversation from Agenthail.</p></div>'}`;
  renderLiveTurn();
  app.transcriptSignature = signature;
  if (app.pendingEntryScroll) {
    const turns = chatBody.querySelectorAll(".turn");
    const latest = turns[turns.length - 1];
    chatBody.scrollTop = latest
      ? chatBody.scrollTop + latest.getBoundingClientRect().top - chatBody.getBoundingClientRect().top
      : 0;
    alignTranscriptTop(chatBody);
    app.pendingEntryScroll = false;
  } else {
    chatBody.scrollTop = wasPinned
      ? chatBody.scrollHeight
      : Math.min(previousScrollTop, chatBody.scrollHeight - chatBody.clientHeight);
  }
}
function compactTokenCount(value) {
  const count = Number(value || 0);
  if (count >= 1000000) return `${(count / 1000000).toFixed(count >= 10000000 ? 0 : 1).replace(/\.0$/, "")}m`;
  if (count >= 1000) return `${Math.round(count / 1000)}k`;
  return String(count);
}
function goalMetrics(goal) {
  return goal ? `<div class="session-tool-note">Status: ${escape(goalStatusLabel(goal.status))}${goal.timeUsedSeconds != null ? ` · Elapsed: ${escape(formatGoalDuration(goal.timeUsedSeconds))}` : ""}${goal.tokensUsed != null ? ` · Tokens: ${compactTokenCount(goal.tokensUsed)}` : ""}${goal.tokenBudget != null ? ` · Budget: ${compactTokenCount(goal.tokenBudget)}` : ""}${goal.createdAt ? ` · Created: ${escape(goal.createdAt)}` : ""}${goal.updatedAt ? ` · Updated: ${escape(goal.updatedAt)}` : ""}</div>${goal.reason ? `<p class="session-tool-note">Reason: ${escape(goal.reason)}</p>` : ""}` : "";
}
function goalStatusLabel(status) {
  return ({ active: "Active", paused: "Paused", blocked: "Blocked", usageLimited: "Usage limited", budgetLimited: "Budget limited", complete: "Complete" })[status] || status || "Unknown";
}
function renderGoalAttention(goal) {
  if (!goal || !["blocked", "usageLimited", "budgetLimited"].includes(goal.status)) return "";
  return `<p class="goal-attention" role="status">Needs you: ${escape(goalStatusLabel(goal.status))}. Review the goal in Conversation settings.</p>`;
}
const permissionModes = [
  ["", "Default (Claude settings)"],
  ["acceptEdits", "Accept edits"],
  ["auto", "Auto"],
  ["plan", "Plan"],
  ["manual", "Manual"],
  ["dontAsk", "Don't ask"],
  ["bypassPermissions", "Bypass permissions"],
];
// syncRequests reads what the selected session waits on whenever the session
// or the request it shows changes. The provider holds the requests, so each
// read is fresh rather than taken from the session list.
async function syncRequests() {
  const session = app.selected;
  const panel = $("#request-panel");
  const capabilities = app.history?.capabilities || {};
  if (!session || !capabilities.respond || app.history?.readOnly || (session.waitingOn && !session.waitingOn.answerable)) {
    app.requests = null;
    app.requestsKey = "";
    panel.innerHTML = session?.waitingOn ? renderElsewhereWait(session) : "";
    panel.hidden = panel.innerHTML === "";
    return;
  }
  const key = `${session.id}|${session.waitingOn?.id || ""}`;
  if (app.requestsKey === key) return;
  app.requestsKey = key;
  try {
    const response = await fetch(`/api/requests?sessionId=${encodeURIComponent(session.id)}`);
    if (!response.ok) throw Error(await response.text());
    const state = await response.json();
    if (app.selected?.id !== session.id) return;
    app.requests = state;
  } catch (error) {
    app.requests = { permissionMode: app.requests?.permissionMode || "", requests: [], error: error.message };
  }
  renderRequests();
}
function renderRequests() {
  const panel = $("#request-panel");
  const state = app.requests || { requests: [] };
  const capabilities = app.history?.capabilities || {};
  const mode = capabilities.permissionMode
    ? `<form class="request-mode" data-permission-mode><label for="permission-mode">Permission mode</label><select id="permission-mode" name="permissionMode">${permissionModes.map(([value, label]) => `<option value="${value}"${value === (state.permissionMode || "") ? " selected" : ""}${value === "" ? " disabled" : ""}>${escape(label)}</option>`).join("")}</select><button class="soft-button" type="submit">Change</button></form>`
    : "";
  const failure = state.error ? `<p role="status">Could not read this session's requests: ${escape(state.error)}</p>` : "";
  // A wait Agenthail does not hold, such as a dialog, says where to settle it;
  // any request still listed from before it is stale.
  const waiting = app.selected?.waitingOn;
  const cards = waiting && !waiting.answerable
    ? renderElsewhereWait(app.selected)
    : (state.requests || []).map(renderRequestCard).join("");
  panel.innerHTML = cards + failure + mode;
  panel.hidden = panel.innerHTML === "";
}
const backgroundKinds = { shell: "Shell", agent: "Agent", monitor: "Monitor" };
const backgroundSources = { host: "", transcript: "Read from the transcript.", status: "Claude reports a background shell but not which one." };
// Lists what a session runs in the background. Ambient tasks, such as file
// watchers, are listed but do not keep the session from being done.
function renderBackgroundTasks(session) {
  const panel = $("#background-panel");
  const background = session?.backgroundTasks;
  if (!background || (!background.running && !(background.tasks || []).length)) {
    panel.innerHTML = "";
    panel.hidden = true;
    return;
  }
  const tasks = (background.tasks || []).map((task) => {
    const kind = backgroundKinds[task.kind] || task.kind || "Task";
    const since = task.since ? ` · since <time datetime="${escape(task.since)}">${escape(new Date(task.since).toLocaleTimeString())}</time>` : "";
    const ambient = task.ambient ? ' <span class="background-ambient">ambient</span>' : "";
    return `<li data-background-task="${escape(task.id || "")}"><span class="background-kind">${escape(kind)}</span> ${escape(task.description || task.id || "")}${ambient}${since}</li>`;
  });
  const running = background.tasks?.filter((task) => !task.ambient).length || 0;
  const title = !background.running ? "Only ambient tasks run" : running ? `${running} background task${running === 1 ? "" : "s"} running` : "Background work running";
  const source = backgroundSources[background.source] || "";
  panel.innerHTML = `<div class="background-card"><h3>${escape(title)}</h3>${tasks.length ? `<ul>${tasks.join("")}</ul>` : ""}${source ? `<p>${escape(source)}</p>` : ""}</div>`;
  panel.hidden = false;
}
// A session waiting on a prompt Agenthail cannot answer shows where to answer
// it, or, when nothing shows it, how to clear it.
function renderElsewhereWait(session) {
  const place = waitingPlace(session);
  const asking = { question: "asks you a question", input: "needs your input", dialog: "has a dialog open in its terminal" }[session.waitingOn.kind] || "needs your approval";
  const heading = place === "restart" ? "is waiting on a prompt Agenthail can't reach" : asking;
  const detail = {
    restart: "Nothing shows this prompt, so no one can answer it. Stop and resume the session to clear it; messages you send wait until then.",
    terminal: "Agenthail can't answer this one. Answer it in the session's terminal. Messages you send wait until then.",
  }[place] || "Agenthail can't answer this one. Answer it where the session runs, such as its terminal. Messages you send wait until then.";
  const actions = [];
  if (place === "restart") actions.push('<button class="primary-button" data-action="session-restart" type="button">Stop and resume</button>');
  if (session.runtime?.focusable) actions.push(`<button class="${actions.length ? "soft-button" : "primary-button"}" data-action="session-focus" type="button">Open session</button>`);
  return `<div class="request-card"><h3>${escape(displayName(session))} ${heading}</h3><p>${detail}</p>${actions.length ? `<div class="request-actions">${actions.join("")}</div>` : ""}</div>`;
}
function renderRequestCard(request) {
  const created = request.createdAt ? ` · <time datetime="${escape(request.createdAt)}">${escape(new Date(request.createdAt).toLocaleTimeString())}</time>` : "";
  if (request.kind === "input" && request.elicitation) return renderInputRequest(request, created);
  if (request.kind === "question") {
    const questions = (request.questions || []).map((question, index) => {
      const type = question.multiSelect ? "checkbox" : "radio";
      const name = `answer-${index}`;
      const options = (question.options || []).map((option) => `<label><input type="${type}" name="${name}" value="${escape(option.label)}"><span>${escape(option.label)}</span>${option.description ? `<small>${escape(option.description)}</small>` : ""}</label>`).join("");
      return `<fieldset data-question="${escape(question.question)}"><legend>${question.header ? `${escape(question.header)}: ` : ""}${escape(question.question)}</legend>${options}<label><span>Other</span><input type="text" name="${name}-other" placeholder="Your own answer"></label></fieldset>`;
    }).join("");
    return `<form class="request-card" data-request-question="${escape(request.id)}"><h3>Claude asks${created}</h3>${questions}<div class="request-actions"><button class="primary-button" type="submit">Answer</button><button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(request.id)}">Decline</button></div></form>`;
  }
  const title = request.title || `Claude wants to use ${request.tool}`;
  const input = request.input && Object.keys(request.input).length ? `<pre>${escape(JSON.stringify(request.input, null, 2))}</pre>` : "";
  const detail = [request.description, request.reason, request.blockedPath && `Path: ${request.blockedPath}`].filter(Boolean).map((line) => `<p>${escape(line)}</p>`).join("");
  return `<div class="request-card"><h3>${escape(title)}${created}</h3>${detail}${input}<div class="request-actions"><button class="primary-button" type="button" data-request-respond="allow" data-request-id="${escape(request.id)}">Allow</button>${request.alwaysAllow ? `<button class="soft-button" type="button" data-request-respond="always" data-request-id="${escape(request.id)}">Always allow</button>` : ""}<button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(request.id)}">Deny</button></div></div>`;
}
// An MCP server's request for input is a form of its fields, or a link to
// open that is accepted once the person chose to open it.
function renderInputRequest(request, created) {
  const e = request.elicitation;
  const heading = `<h3>${escape(e.server || "An MCP server")} asks for input${created}</h3><p>${escape(e.message)}</p>`;
  const decline = `<button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(request.id)}">Decline</button>`;
  if (e.mode === "url") {
    const link = /^https?:\/\//i.test(e.url || "") ? `<p><a href="${escape(e.url)}" target="_blank" rel="noopener noreferrer">${escape(e.url)}</a></p>` : `<p>${escape(e.url || "")}</p>`;
    return `<div class="request-card">${heading}${link}<div class="request-actions"><button class="primary-button" type="button" data-request-respond="allow" data-request-id="${escape(request.id)}">Opened, continue</button>${decline}</div></div>`;
  }
  const fields = (e.fields || []).map(renderInputField).join("");
  return `<form class="request-card" data-request-input="${escape(request.id)}">${heading}${fields}<div class="request-actions"><button class="primary-button" type="submit">Submit</button>${decline}</div></form>`;
}
function renderInputField(field) {
  const label = `${escape(field.title || field.name)}${field.required ? " *" : ""}`;
  const help = field.description ? `<small>${escape(field.description)}</small>` : "";
  const name = `field-${escape(field.name)}`;
  if (field.multiSelect) {
    const picked = new Set(Array.isArray(field.default) ? field.default : []);
    const options = (field.options || []).map((option) => `<label><input type="checkbox" name="${name}" value="${escape(option.value)}"${picked.has(option.value) ? " checked" : ""}><span>${escape(option.label)}</span></label>`).join("");
    return `<fieldset data-field="${escape(field.name)}"><legend>${label}</legend>${help}${options}</fieldset>`;
  }
  if (field.options) {
    const options = (field.options || []).map((option) => `<option value="${escape(option.value)}"${option.value === field.default ? " selected" : ""}>${escape(option.label)}</option>`).join("");
    return `<label data-field="${escape(field.name)}"><span>${label}</span><select name="${name}"><option value=""></option>${options}</select>${help}</label>`;
  }
  if (field.type === "boolean") {
    return `<label data-field="${escape(field.name)}"><input type="checkbox" name="${name}"${field.default === true ? " checked" : ""}><span>${label}</span>${help}</label>`;
  }
  const number = field.type === "number" || field.type === "integer";
  const value = field.default === undefined ? "" : ` value="${escape(String(field.default))}"`;
  return `<label data-field="${escape(field.name)}"><span>${label}</span><input type="${number ? "number" : "text"}"${field.type === "integer" ? ' step="1"' : number ? ' step="any"' : ""} name="${name}"${value}>${help}</label>`;
}
// inputContent reads a form's field values as the MCP server takes them. A
// field left empty is left out, so the host refuses a missing required one.
function inputContent(form, request) {
  const content = {};
  for (const field of request.elicitation.fields || []) {
    const name = `field-${field.name}`;
    if (field.multiSelect) {
      content[field.name] = [...form.querySelectorAll(`[name="${CSS.escape(name)}"]:checked`)].map((input) => input.value);
      continue;
    }
    const input = form.querySelector(`[name="${CSS.escape(name)}"]`);
    if (field.type === "boolean") {
      content[field.name] = input.checked;
      continue;
    }
    const text = input.value.trim();
    if (text === "") continue;
    content[field.name] = field.type === "number" || field.type === "integer" ? Number(text) : text;
  }
  return content;
}
async function respondToRequest(requestId, decision) {
  try {
    const result = await action("respond", { requestId, decision });
    toast(`Request ${result?.result?.outcome || "answered"}.`);
  } catch (error) {
    const stale = /already answered or has expired/.test(error.message);
    if (stale && app.requests) {
      app.requests = { ...app.requests, requests: (app.requests.requests || []).filter((request) => request.id !== requestId) };
      renderRequests();
    }
    toast(stale ? "That request was already answered or has expired." : error.message);
  }
  app.requestsKey = "";
  await load();
  if (app.selected) await selectSession(app.selected.id);
}
function formatGoalDuration(seconds) {
  const value = Number(seconds);
  if (!Number.isFinite(value)) return "Unknown";
  if (value < 60) return `${value}s`;
  const minutes = Math.floor(value / 60);
  if (minutes < 60) return `${minutes}m ${value % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
function contextBreakdown(context) {
  const count = (value) => Number(value).toLocaleString();
  const window = context.windowEstimated ? "Estimated window" : context.contextWindowSource === "configured" ? "Configured window" : "Context window";
  const rows = [["Used tokens", count(context.usedTokens || 0)], [window, context.contextWindow > 0 ? count(context.contextWindow) : "Unavailable"]];
  for (const [label, key] of [["Input", "inputTokens"], ["Cached input", "cachedInputTokens"], ["Output", "outputTokens"], ["Reasoning output", "reasoningOutputTokens"], ["Cumulative tokens", "cumulativeTokens"]]) {
    if (Number.isFinite(context[key])) rows.push([label, count(context[key])]);
  }
  rows.push(["Compactions", count(context.compactionCount || 0)]);
  if (Number.isFinite(context.reclaimedTokens)) rows.push(["Tokens reclaimed", count(context.reclaimedTokens)]);
  return rows;
}
function renderContextUsage(context) {
  const indicator = $("#context-usage");
  if (!context || (!context.contextWindow && !context.compactionCount && !context.usedTokens)) {
    indicator.hidden = true;
    indicator.textContent = "";
    indicator.className = "";
    return;
  }
  const hasWindow = context.contextWindow > 0 && context.usedTokens <= context.contextWindow;
  const percent = hasWindow
    ? Math.round((context.usedTokens / context.contextWindow) * 100)
    : 0;
  indicator.hidden = false;
  indicator.className = context.compacting
    ? "context-usage compacting"
    : percent >= 90
      ? "context-usage critical"
      : percent >= 80
        ? "context-usage warning"
        : "context-usage";
  indicator.textContent = context.compacting
    ? "Compacting context"
    : hasWindow
      ? `Context ${compactTokenCount(context.usedTokens)} / ${compactTokenCount(context.contextWindow)} · ${percent}%`
      : context.usedTokens
        ? `Context ${compactTokenCount(context.usedTokens)} tokens`
        : `${context.compactionCount} compaction${context.compactionCount === 1 ? "" : "s"}`;
  const details = [];
  if (context.windowEstimated) details.push("Claude context window is estimated");
  if (context.contextWindowSource === "unknown") details.push("Claude did not report a configured context window");
  if (context.compactionCount) details.push(`${context.compactionCount} compaction${context.compactionCount === 1 ? "" : "s"}`);
  if (context.preCompactTokens && context.postCompactTokens)
    details.push(`Last compact: ${compactTokenCount(context.preCompactTokens)} to ${compactTokenCount(context.postCompactTokens)}, ${compactTokenCount(context.reclaimedTokens)} reclaimed`);
  const breakdown = contextBreakdown(context).map(([label, value]) => `${label}: ${value}`).join("\n");
  indicator.title = details.length ? `${details.join(". ")}\n\n${breakdown}` : breakdown;
}
function isNetworkFailure(error) {
  return error?.networkFailure === true || error?.name === "TypeError";
}
function logicalRequest(scope, actionName, payload) {
  const sessionId = scope === "session-create" ? "" : app.selected?.id || "";
  const serializedPayload = JSON.stringify({ action: actionName, sessionId, ...payload });
  const current = app.pendingIdempotency.get(scope);
  if (current?.fingerprint === serializedPayload) return current;
  const next = { fingerprint: serializedPayload, key: crypto.randomUUID(), sessionId };
  app.pendingIdempotency.set(scope, next);
  return next;
}
function clearLogicalRequest(scope) {
  app.pendingIdempotency.delete(scope);
}
function sessionTarget(session) {
  if (session?.alias) return `@${session.alias}`;
  return `${session?.surface || "session"}:${session?.id || "unknown"}`;
}
async function logicalAction(scope, actionName, payload) {
  payload = withHolder(app.selected?.id, payload);
  const request = logicalRequest(scope, actionName, payload);
  try {
    const result = await action(actionName, payload, request.key, request.sessionId);
    clearLogicalRequest(scope);
    return result;
  } catch (error) {
    if (!isNetworkFailure(error) && !error.unknown) clearLogicalRequest(scope);
    throw error;
  }
}
async function action(action, extra = {}, idempotencyKey = crypto.randomUUID(), sessionID = app.selected?.id) {
  const networkAction =
    action.startsWith("channel-") ||
    action.startsWith("relay-") ||
    action === "notion-create" ||
    action === "session-create" ||
    action === "queue-retry" ||
    action === "delivery-dismiss" ||
    action === "queue-cancel" ||
    action.startsWith("project-") ||
    action.startsWith("coordinator-");
  if (!app.selected && !networkAction)
    throw Error("Choose a conversation first");
  if (!networkAction) extra = withHolder(sessionID, extra);
  let response;
  try {
    response = await fetch("/api/action", {
      method: "POST",
      headers: { "content-type": "application/json", "Idempotency-Key": idempotencyKey },
      body: JSON.stringify({ action, sessionId: sessionID, ...extra }),
    });
  } catch (error) {
    error.networkFailure = true;
    throw error;
  }
  if (!response.ok) {
    let detail;
    try {
      detail = await response.text();
    } catch (error) {
      const typed = Error(`HTTP ${response.status || 500}`);
      typed.httpFailure = true;
      throw typed;
    }
    const failure = Error(detail);
    try {
      const body = JSON.parse(detail);
      const typed = body.error;
      if (typed?.message) {
        failure.message = typed.message;
        failure.code = typed.code || "";
      } else if (typeof typed === "string") {
        failure.message = typed;
        failure.code = body.code || "";
      }
      failure.unknown = body.unknown === true || failure.code === "host_outcome_unknown";
    } catch {}
    throw failure;
  }
  try {
    return await response.json();
  } catch (error) {
    error.networkFailure = true;
    throw error;
  }
}
function deliveryStatusLabel(result) {
  const status = result?.result?.evidence || result?.status;
  return status === "queued" ? "Queued" : status === "submitted" ? "Submitted" : "Sent";
}
function creationNotice(response, values) {
  const target = sessionTarget(response.session || { surface: values.surface, id: response.sessionId, alias: values.alias });
  return response.detail || response.warning || `${response.status === "submitted" ? "Submitted" : "Sent"} to ${target}.`;
}
function creationFailureMessage(error) {
  return error?.unknown
    ? "Agenthail can't tell whether this session started; check the sidebar before trying again."
    : friendlyError(error);
}
async function voiceRequest(action, extra = {}) {
  const response = await fetch("/api/voice", {
    method: action ? "POST" : "GET",
    headers: action ? { "content-type": "application/json" } : undefined,
    body: action ? JSON.stringify({ action, ...extra }) : undefined,
  });
  const body = await response.json();
  if (!response.ok) {
    app.voice.state = body.state || app.voice.state;
    throw Error(body?.error?.message || "Voice action was blocked.");
  }
  app.voice.state = body;
  renderVoice();
  if (app.voice.localAudio && body.sdp && body.sdp !== app.voice.appliedSDP && window.agenthailVoice) {
    app.voice.appliedSDP = body.sdp;
    await window.agenthailVoice.answer(body.sdp);
  }
  return body;
}
function voiceCallActive() {
  return ["starting", "negotiating", "connected", "stopping", "unknown"].includes(app.voice.state?.phase);
}
function renderVoice() {
  const state = app.voice.state;
  const call = $("#voice-call"), transfer = $("#voice-transfer"), stopTarget = $("#voice-stop-target"), hangup = $("#voice-hangup"), status = $("#voice-status"), target = $("#voice-target"), help = $("#voice-help");
  if (!call || !state) return;
  const routed = state.target?.name || "Agenthail orchestrator";
  const active = voiceCallActive();
  status.textContent = state.message || (active ? `Call active with ${routed}.` : `Ready to talk to ${routed}.`);
  if (app.voice.requestedTarget !== null) target.value = app.voice.requestedTarget;
  else if (state.target) target.value = state.target.id;
  call.hidden = active;
  transfer.hidden = !active;
  stopTarget.hidden = !active || !state.target;
  hangup.hidden = !active;
  transfer.textContent = target.value.trim() ? "Transfer to session" : "Return to orchestrator";
  help.textContent = target.value.trim() ? "This routes work only to that exact existing session." : "Leave blank to use Agenthail’s normal orchestration workflow.";
  $("#voice-title").textContent = state.target ? "Session voice" : "Talk to orchestrator";
}
async function ensureVoicePeer() {
  if (window.agenthailVoice) return;
  if (!app.voice.script) {
    app.voice.script = new Promise((resolve, reject) => {
      const script = document.createElement("script");
      script.src = "/voice-peer.js";
      script.onload = resolve;
      script.onerror = () => reject(Error("Could not load browser voice controls."));
      document.head.append(script);
    });
  }
  await app.voice.script;
}
function currentSessionID(id) {
  return app.rekeyed[id] || id;
}
function applyRekeys(rekeyed) {
  app.rekeyed = rekeyed;
  for (const [retired, current] of Object.entries(rekeyed)) {
    if (!app.drafts.has(retired)) continue;
    if (!app.drafts.has(current)) app.drafts.set(current, app.drafts.get(retired));
    app.drafts.delete(retired);
  }
  if (app.voice.requestedTarget) app.voice.requestedTarget = currentSessionID(app.voice.requestedTarget);
  const target = $("#voice-target");
  if (target?.value.trim()) target.value = currentSessionID(target.value.trim());
}
async function openVoice(targetID = "") {
  const dialog = $("#voice-dialog");
  app.voice.requestedTarget = targetID;
  $("#voice-target").value = targetID;
  if (!dialog.open) dialog.showModal();
  if (!app.voice.poll) app.voice.poll = setInterval(() => voiceRequest().catch(() => {}), 1000);
  try {
    await voiceRequest();
    if (targetID && !voiceCallActive()) renderVoice();
  } catch (error) { toast(error.message); }
}
async function startVoice() {
  const targetID = currentSessionID($("#voice-target").value.trim());
  try {
    await voiceRequest("select", { targetId: targetID });
    app.voice.requestedTarget = null;
    await voiceRequest("prepare");
    await ensureVoicePeer();
    app.voice.attemptId = crypto.randomUUID();
    app.voice.localAudio = true;
    app.voice.connection = false;
    app.voice.channel = false;
    app.voice.appliedSDP = "";
    await window.agenthailVoice.start();
  } catch (error) {
    app.voice.localAudio = false;
    toast(error.message);
  }
}
async function transferVoice() {
  try { await voiceRequest("transfer", { targetId: currentSessionID($("#voice-target").value.trim()) }); }
  catch (error) { toast(error.message); }
}
async function interruptVoiceTarget() {
  const state = app.voice.state;
  if (!state?.target || !voiceCallActive()) return;
  if (!window.confirm(`Stop the confirmed active turn in ${state.target.name}? Audio stays connected.`)) return;
  try { await voiceRequest("target-interrupt", { attemptId: app.voice.attemptId || state.attemptId }); }
  catch (error) { toast(error.message); }
}
async function hangupVoice() {
  const attemptId = app.voice.attemptId || app.voice.state?.attemptId;
  window.agenthailVoice?.end();
  app.voice.localAudio = false;
  if (!attemptId) return;
  try { await voiceRequest("stop", { attemptId }); }
  catch (error) { toast(error.message); }
}
window.agenthailVoiceNotify = async ({ type, value }) => {
  if (type === "ready") return;
  if (type === "offer") {
    try {
      await voiceRequest("start", { attemptId: app.voice.attemptId, sdp: value });
    } catch (error) { toast(error.message); await hangupVoice(); }
    return;
  }
  if (type === "connection") app.voice.connection = value === "connected";
  if (type === "channel") app.voice.channel = value === "open";
  if (app.voice.connection && app.voice.channel && app.voice.attemptId) {
    try { await voiceRequest("connected", { attemptId: app.voice.attemptId }); }
    catch (error) { toast(error.message); }
    app.voice.channel = false;
  }
  if (type === "error") { toast(value || "Voice audio failed."); await hangupVoice(); }
};
async function send(requestedAction = "send") {
  const message = $("#message").value.trim();
  const composerAction = requestedAction === "send" ? $("#send").dataset.mode : requestedAction;
  if (composerAction === "interrupt") {
    $("#send").disabled = true;
    try {
      await action("interrupt");
      toast("Current turn stopped.");
      await load(true);
      await selectSession(app.selected.id);
    } catch (error) {
      toast(error.message);
    } finally {
      syncComposerAction();
    }
    return;
  }
  if (!message) return;
  $("#send").disabled = true;
  $("#steer-message").disabled = true;
  try {
    const [command, ...rest] = message.split(/\s+/);
    const argument = rest.join(" ");
    const commandActions = {
      "/compact": ["compact", {}],
      "/stop": ["interrupt", {}],
      "/steer": ["steer", { message: argument, author: "person" }],
      "/model": ["model", { model: argument }],
      "/goal": ["goal-set", { message: argument }],
      "/name": ["alias", { alias: argument }],
    };
    const commandAction = commandActions[command.toLowerCase()];
    if (commandAction && ["/steer", "/model", "/goal", "/name"].includes(command.toLowerCase()) && !argument)
      throw Error(`${command} needs a value`);
    const sendPayload = { message, author: "person", ...selectedTurnOptions() };
    const result = composerAction === "steer"
      ? await action("steer", { message, author: "person" })
      : commandAction
      ? await action(commandAction[0], commandAction[1])
      : await logicalAction("send", "send", sendPayload);
    $("#message").value = "";
    app.drafts.delete(app.selected.id);
    resizeComposer();
    renderSlashMenu();
    const status = deliveryStatusLabel(result);
    const target = sessionTarget(app.selected);
    toast(
      result?.result?.waitingOn || result?.result?.evidence === "transport_accepted" || commandAction && result?.result?.evidence === "queued"
        ? result.result.detail
        : composerAction === "steer"
        ? "Current turn redirected."
        : command?.toLowerCase() === "/model" && result?.result?.output
        ? result.result.output
        : commandAction
        ? `${command} requested.`
        : result?.result?.notice
        ? `${status} to ${target}. ${result.result.notice}`
        : `${status} to ${target}.`,
    );
    await load();
    await selectSession(app.selected.id);
  } catch (error) {
    const holders = sharedHolders(app.selected);
    toast(holders.length && !chosenHolder(app.selected.id) ? `Open in ${holders.length} terminals. Choose one above, then send.` : error.message);
  } finally {
    syncComposerAction();
  }
}
function renderSlashMenu() {
  const menu = $("#slash-menu");
  const value = $("#message")?.value.trimStart() || "";
  const query = value.split(/\s/, 1)[0].toLowerCase();
  const choosingModel = value.toLowerCase().startsWith("/model ");
  const visible = value.startsWith("/") && !value.includes(" ");
  const commands = choosingModel
    ? (app.history?.models || []).map((model) => [model.id, model.displayName || model.id, model.id === (app.history?.pendingModel || app.selected?.pendingModel) ? "From next turn" : model.description || "", model.default])
    : visible
    ? app.slashCommands.filter(([command]) => command.startsWith(query))
    : [];
  menu.hidden = commands.length === 0;
  menu.innerHTML = commands
    .map(([command, description, detail, isDefault]) => choosingModel
      ? `<button type="button" data-model-option="${escape(command)}"><strong>${escape(description)}</strong><span>${escape(detail || (isDefault ? "Default" : command))}</span></button>`
      : `<button type="button" data-slash="${escape(command)}"><strong>${escape(command)}</strong><span>${escape(description)}</span></button>`)
    .join("");
}
document.addEventListener("click", async (event) => {
  const chip = event.target.closest("[data-chip]");
  if (chip) return toggleFileChip(chip);
  const diffShow = event.target.closest("[data-diff-show]");
  if (diffShow) return showToolDiff(diffShow);
  const outputShow = event.target.closest("[data-output-show]");
  if (outputShow) return showToolOutput(outputShow);
  const collapseRow = event.target.closest("[data-collapse-row]");
  if (collapseRow) return collapseToolRow(collapseRow);
  const copyOutput = event.target.closest("[data-copy-text]");
  if (copyOutput) {
    try {
      await navigator.clipboard.writeText(copyOutput.dataset.copyText);
      toast(copyOutput.dataset.copyDone || "Copied.");
    } catch (error) {
      toast("Couldn't copy.");
    }
    return;
  }
  const editMessage = event.target.closest("[data-edit-message]");
  if (editMessage) {
    const composer = $("#message");
    if (composer.disabled) {
      toast("This session is read only.");
      return;
    }
    composer.value = composer.value.trim() ? `${editMessage.dataset.editMessage}\n\n${composer.value}` : editMessage.dataset.editMessage;
    renderSlashMenu();
    resizeComposer();
    syncComposerAction();
    composer.focus();
    return;
  }
  const modelOption = event.target.closest("[data-model-option]");
  if (modelOption) {
    $("#message").value = `/model ${modelOption.dataset.modelOption}`;
    renderSlashMenu();
    resizeComposer();
    $("#message").focus();
    return;
  }
  const slash = event.target.closest("[data-slash]");
  if (slash) {
    const command = slash.dataset.slash;
    $("#message").value = command + (["/steer", "/model", "/goal", "/name"].includes(command) ? " " : "");
    renderSlashMenu();
    resizeComposer();
    $("#message").focus();
    return;
  }
  const viewLink = event.target.closest("[data-view]");
  if (viewLink?.dataset.view === "conversations" && mobileConversationView()) {
    app.mobileChatOpen = false;
    syncConversationLayout();
  }
  const mode = event.target.closest("[data-inbox-mode]");
  if (mode) {
    app.inboxMode = mode.dataset.inboxMode;
    if (app.inboxMode === "current") {
      app.filters.surface = "all";
      app.filters.status = "all";
      $("#session-surface-filter").value = "all";
      $("#session-status-filter").value = "all";
    }
    app.sessionLimit = sessionBatchSize();
    renderSessions();
    return;
  }
  const projectExpand = event.target.closest("[data-project-expand]");
  if (projectExpand) {
    const id = projectExpand.dataset.projectExpand;
    if (app.expandedProjects.has(id)) app.expandedProjects.delete(id);
    else app.expandedProjects.add(id);
    renderSessions();
    return;
  }
  const archivedProjects = event.target.closest(".archived-projects > summary");
  if (archivedProjects) {
    event.preventDefault();
    app.openArchivedProjects = !app.openArchivedProjects;
    renderSessions();
    return;
  }
  const sessionTarget = event.target.closest("[data-session]");
  if (sessionTarget) return selectSession(sessionTarget.dataset.session, true);
  const voiceSession = event.target.closest("[data-voice-session]");
  if (voiceSession) return openVoice(voiceSession.dataset.voiceSession);
  const surfaceButton = event.target.closest("[data-surface]");
  if (surfaceButton) {
    app.mobileChatOpen = false;
    showView("conversations");
    $("#session-search").value = "";
    cancelCodexSearch();
    app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
    app.inboxMode = "all";
    app.sessionLimit = sessionBatchSize();
    app.filters.surface = surfaceButton.dataset.surface;
    $("#session-surface-filter").value = app.filters.surface;
    renderSessions();
    return;
  }
  if (event.target.closest("[data-open-conversations]")) {
    app.mobileChatOpen = false;
    return showView("conversations");
  }
  if (event.target.closest("[data-open-operations]"))
    return showView("operations");
  const retry = event.target.closest("[data-retry]");
  const deliveryDismiss = event.target.closest("[data-delivery-dismiss]");
  if (deliveryDismiss) {
    deliveryDismiss.disabled = true;
    try {
      const deliveryId = Number(deliveryDismiss.dataset.deliveryDismiss);
      await action("delivery-dismiss", { deliveryId });
      app.state.deliveryProblems = app.state.deliveryProblems.filter(item => item.deliveryId !== deliveryId);
      renderDeliveryProblems();
    } catch (error) {
      deliveryDismiss.disabled = false;
      toast(friendlyError(error));
    }
    return;
  }
  if (retry) {
    retry.disabled = true;
    try {
      await action("queue-retry", { queueId: Number(retry.dataset.retry) });
      toast("Queued message scheduled for retry.");
      await load();
    } catch (error) {
      toast(error.message);
    }
    return;
  }
  const cancel = event.target.closest("[data-cancel]");
  if (cancel) {
    cancel.disabled = true;
    try {
      await action("queue-cancel", { queueId: Number(cancel.dataset.cancel) });
      toast("Queued message canceled.");
      await load();
    } catch (error) {
      toast(error.message);
    }
    return;
  }
  const projectMessage = event.target.closest("[data-project-message]");
  if (projectMessage) {
    const message = window.prompt(`Message every active conversation in ${projectMessage.dataset.projectName}`);
    if (!message || !message.trim()) return;
    projectMessage.disabled = true;
    try {
      const result = await action("channel-send", { channel: `#${projectMessage.dataset.projectMessage}`, message, author: "person" }, crypto.randomUUID(), "");
      const names = (result.recipients || []).map((recipient) => recipient.coordinator || displayName(app.state.sessions.find((session) => session.id === recipient.sessionId) || { id: recipient.sessionId }));
      toast(`Sent to ${names.length}: ${names.join(", ")}.`);
    } catch (error) {
      toast(error.message);
    } finally {
      projectMessage.disabled = false;
      await load();
    }
    return;
  }
  const projectControl = event.target.closest("[data-project-new], [data-project-rename], [data-project-start], [data-project-lead], [data-project-clear-lead], [data-make-lead]");
  if (projectControl) {
    projectControl.closest("details.project-menu")?.removeAttribute("open");
    const project = (app.state.projects || []).find((item) => item.id === (projectControl.dataset.projectRename || projectControl.dataset.projectStart || projectControl.dataset.projectLead || projectControl.dataset.projectClearLead));
    if (projectControl.dataset.projectNew !== undefined) openProjectDialog("add");
    else if (projectControl.dataset.projectRename && project) openProjectDialog("rename", project);
    else if (projectControl.dataset.projectLead && project) openProjectDialog("lead", project);
    else if (projectControl.dataset.projectStart && project) {
      const checkout = project.checkouts[Number(projectControl.dataset.checkout)];
      if (checkout) toggleNewConversationForm(true, { host: checkoutHost(checkout), folder: checkout.rootPath });
    } else if (projectControl.dataset.projectClearLead && project) {
      if (!window.confirm(`Clear the lead of ${project.displayName}? Its conversation and name stay.`)) return;
      await projectChange(projectControl, async () => {
        await action("coordinator-clear", { projectId: project.id }, crypto.randomUUID(), "");
        toast(`${project.displayName} has no lead now.`);
      });
    } else if (projectControl.dataset.makeLead !== undefined && app.selected) {
      const sessionID = app.selected.id;
      await projectChange(projectControl, async () => {
        const result = await changeLead((replace) => action("coordinator-set", replace ? { replace: true } : {}, crypto.randomUUID(), sessionID));
        if (result) toast(`@${result.coordinator.name} leads ${result.coordinator.projectRoot}.`);
      });
    }
    return;
  }
  const viewControl = event.target.closest("[data-archive-action], [data-project-action], [data-project-add]");
  if (viewControl) {
    viewControl.disabled = true;
    try {
      if (viewControl.dataset.archiveAction) {
        await action(viewControl.dataset.archiveAction);
        toast(viewControl.dataset.archiveAction === "session-archive" ? "Conversation archived." : "Conversation unarchived.");
      } else if (viewControl.dataset.projectAction) {
        await action(viewControl.dataset.projectAction, { projectId: viewControl.dataset.projectId }, crypto.randomUUID(), "");
        toast({ "project-archive": "Project archived.", "project-unarchive": "Project unarchived.", "project-remove": "Project removed." }[viewControl.dataset.projectAction]);
      } else {
        const result = await action("project-add", { path: viewControl.dataset.projectAdd, ...(viewControl.dataset.projectHost ? { host: viewControl.dataset.projectHost } : {}) }, crypto.randomUUID(), "");
        toast(result.created === false ? `${result.project?.displayName || "This folder"} is already a project.` : `Added project ${result.project?.displayName || ""}.`);
      }
    } catch (error) {
      toast(error.message);
    } finally {
      viewControl.disabled = false;
      await load();
    }
    return;
  }
  if (event.target.closest("[data-subagents-all]") && app.selected && app.history) {
    app.allSubagents = true;
    loadSessionMetadata(app.selected.id, app.history);
    return;
  }
  const control = event.target.closest("[data-action]");
  if (!control) return;
  control.disabled = true;
  if (control.dataset.action === "session-restart") {
    try {
      await action("session-lifecycle-stop");
      await action("session-lifecycle-resume");
      toast("Stopped and resumed.");
      await load();
      await selectSession(app.selected.id);
    } catch (error) {
      toast(error.message);
    } finally {
      control.disabled = false;
    }
    return;
  }
  try {
    const result = await action(control.dataset.action, {
      message: control.dataset.goalBudgetClear === "true" ? "" : $("#message").value.trim(),
    });
    const queued = result?.result?.evidence === "queued";
    if (control.dataset.action === "compact" && app.history?.context) {
      app.history.context.compacting = true;
      renderContextUsage(app.history.context);
    }
    toast(queued ? `${control.textContent} queued.` : `${control.textContent} requested.`);
    await load();
    await selectSession(app.selected.id);
  } catch (error) {
    toast(error.message);
  } finally {
    control.disabled = false;
  }
});
$("#message").addEventListener("input", () => {
  resizeComposer();
  renderSlashMenu();
  syncComposerAction();
});
$("#steer-message").addEventListener("click", () => send("steer"));
// toggle does not bubble, so the transcript listens for it on the way down.
$("#chat-body").addEventListener("toggle", rememberToolRow, true);
$("#composer").addEventListener("submit", (event) => {
  event.preventDefault();
  send();
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-tool]");
  if (!form) return;
  event.preventDefault();
  const tool = form.dataset.tool;
  const input = form.querySelector("input");
  try {
    if (tool === "alias") {
      await action("alias", { alias: input.value.trim() });
      toast("Conversation name saved.");
      await load();
    } else if (tool === "goal-budget") {
      const value = input.value.trim();
      await action("goal-budget", { message: value });
      toast(value ? "Token budget saved." : "Token budget cleared.");
    } else {
      const result = await action(app.history?.goal?.objective ? "goal-edit" : "goal-set", { message: input.value.trim() });
      toast(result?.result?.evidence === "queued" ? result.result.detail : "Goal saved.");
    }
    await selectSession(app.selected.id);
  } catch (error) {
    toast(error.message);
  }
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-network-form]");
  if (!form) return;
  event.preventDefault();
  const actionName = form.dataset.networkForm;
  const values = Object.fromEntries(new FormData(form).entries());
  if (actionName === "relay-add") values.once = values.once === "true";
  try {
    await action(actionName, values);
    toast(actionName === "relay-add" ? "Relay added." : "Network saved.");
    form.reset();
    await load();
  } catch (error) {
    toast(error.message);
  }
});
document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-network-action]");
  if (!button) return;
  button.disabled = true;
  try {
    await action(button.dataset.networkAction, {
      channel: button.dataset.channel,
      relayId: Number(button.dataset.relayId || 0),
    });
    toast("Network updated.");
    await load();
  } catch (error) {
    toast(friendlyError(error));
  }
});
function cancelCodexSearch() {
  clearTimeout(app.codexSearch.timer);
  app.codexSearch.controller?.abort();
}
function startCodexSearch(query) {
  cancelCodexSearch();
  const search = { query: query.toLowerCase(), results: [], loading: true, error: "", timer: null, controller: null };
  app.codexSearch = search;
  search.timer = setTimeout(async () => {
    const controller = new AbortController();
    search.controller = controller;
    try {
      const response = await fetch(`/api/search?surface=codex&q=${encodeURIComponent(query)}`, { signal: controller.signal });
      if (!response.ok) throw Error(await response.text());
      const payload = await response.json();
      if (app.codexSearch !== search) return;
      search.results = (payload.results || []).map((result) => ({ ...result.session, snippet: result.snippet }));
      search.error = payload.remoteError || "";
    } catch (error) {
      if (error.name !== "AbortError" && app.codexSearch === search) search.error = friendlyError(error);
    } finally {
      if (app.codexSearch === search) {
        search.loading = false;
        renderSessions();
      }
    }
  }, 300);
}
document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-request-respond]");
  if (!button) return;
  button.disabled = true;
  const choice = button.dataset.requestRespond;
  await respondToRequest(button.dataset.requestId, choice === "deny" ? { behavior: "deny" } : { behavior: "allow", always: choice === "always" });
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-request-question]");
  if (!form) return;
  event.preventDefault();
  const answers = {};
  for (const [index, fieldset] of [...form.querySelectorAll("fieldset[data-question]")].entries()) {
    const other = form.querySelector(`[name="answer-${index}-other"]`).value.trim();
    const picked = [...form.querySelectorAll(`[name="answer-${index}"]:checked`)].map((input) => input.value);
    const answer = other || picked.join(", ");
    if (!answer) {
      toast(`Answer: ${fieldset.dataset.question}`);
      return;
    }
    answers[fieldset.dataset.question] = answer;
  }
  form.querySelector('[type="submit"]').disabled = true;
  await respondToRequest(form.dataset.requestQuestion, { behavior: "allow", answers });
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-request-input]");
  if (!form) return;
  event.preventDefault();
  const request = (app.requests?.requests || []).find((r) => r.id === form.dataset.requestInput);
  if (!request) return;
  form.querySelector('[type="submit"]').disabled = true;
  await respondToRequest(request.id, { behavior: "allow", content: inputContent(form, request) });
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-permission-mode]");
  if (!form) return;
  event.preventDefault();
  const permissionMode = form.elements.permissionMode.value;
  if (!permissionMode) return;
  try {
    await action("permission-mode", { permissionMode });
    toast(`Permission mode set to ${permissionMode}.`);
  } catch (error) {
    toast(error.message);
  }
  app.requestsKey = "";
  await syncRequests();
});
$("#session-search").addEventListener("input", () => {
  app.sessionLimit = sessionBatchSize();
  const query = $("#session-search").value.trim();
  if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) {
    startCodexSearch(query);
  } else {
    cancelCodexSearch();
    app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
  }
  renderSessions();
});
function startable(name, connected) {
  return (connected || name === "claude") && ["codex", "claude", "notion"].includes(name);
}
function startableSurfaces() {
  if (app.startOptions) return (app.startOptions.surfaces || []).filter((item) => startable(item.id, item.connected)).map((item) => item.id);
  return app.state.surfaces.filter((item) => startable(item.name, item.connected)).map((item) => item.name);
}
const hostStateLabels = { connecting: "Connecting", unreachable: "Unreachable", unauthorized: "Pairing revoked", incompatible: "Needs update" };
const hostIcon = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="1.5" y="2.5" width="13" height="8.5" rx="1.5"/><path d="M5.5 13.5h5M8 11v2.5"/></svg>';
function startHosts() {
  const local = app.state.daemon?.host;
  const paired = app.state.daemon?.hosts || [];
  if (!local || paired.length === 0) return [];
  return [{ id: "", name: local.name, local: true, state: "connected" }, ...paired.map((host) => ({ id: host.id, name: host.name, state: host.state }))];
}
function defaultStartHost() {
  const host = app.selected?.host;
  if (!host || host.state === "local") return "";
  return (app.state.daemon?.hosts || []).some((paired) => paired.id === host.id && paired.state === "connected") ? host.id : "";
}
function startHostQuery(prefix) {
  return app.startHost ? `${prefix}host=${encodeURIComponent(app.startHost)}` : "";
}
function renderStartHost() {
  const button = $("#new-conversation-host");
  const menu = $("#new-conversation-host-menu");
  const hosts = startHosts();
  if (app.startHost && !hosts.some((host) => host.id === app.startHost)) {
    app.startHost = "";
    clearLogicalRequest("session-create");
    if (!$("#new-conversation-form").hidden) {
      reloadStartCatalog();
      return;
    }
  }
  button.hidden = hosts.length === 0;
  const submit = $("#new-conversation-form").querySelector('button[type="submit"]');
  if (hosts.length === 0) {
    menu.hidden = true;
    submit.disabled = false;
    return;
  }
  const current = hosts.find((host) => host.id === app.startHost) || hosts[0];
  submit.disabled = current.state !== "connected";
  button.innerHTML = `${hostIcon}<span>${escape(current.name)}</span>`;
  button.title = `Starts on ${current.name}`;
  button.setAttribute("aria-label", `Starts on ${current.name}. Choose where to start`);
  button.setAttribute("aria-expanded", String(!menu.hidden));
  menu.innerHTML = hosts.map((host) => {
    const ready = host.state === "connected";
    return `<button type="button" role="option" data-start-host="${escape(host.id)}" aria-selected="${host.id === app.startHost}"${ready ? "" : " disabled"}><span>${escape(host.name)}${host.local ? " · this machine" : ""}</span>${ready ? "" : `<small>${escape(hostStateLabels[host.state] || host.state)}</small>`}</button>`;
  }).join("");
}
function renderStartSurfaceOptions() {
  const select = $("#new-conversation-surface");
  const current = select.value;
  const surfaces = startableSurfaces();
  select.innerHTML = surfaces
    .map((name) => `<option value="${escape(name)}">${escape(labels[name] || name)}</option>`)
    .join("");
  if (surfaces.includes(current)) select.value = current;
  $("#new-conversation-toggle").hidden = app.state.surfaces.every((item) => !startable(item.name, item.connected));
  renderStartProjects();
  renderStartHost();
  syncStartForm();
}
// The project picker lists the host's projects, most recent first, and fills
// the folder with the chosen project's root; "Other folder" shows the folder
// field instead.
function renderStartProjects() {
  const select = $("#new-conversation-project");
  const folder = $("#new-conversation-form").querySelector('input[name="cwd"]');
  const projects = app.startOptions?.projects || [];
  select.innerHTML = projects
    .map((project) => `<option value="${escape(project.rootPath)}" title="${escape(project.rootPath)}">${escape(project.displayName)}</option>`)
    .join("") + '<option value="">Other folder</option>';
  const chosen = projects.find((project) => project.rootPath === folder.value) || (folder.value === "~" || folder.value === "" ? projects[0] : null);
  select.value = chosen ? chosen.rootPath : "";
  if (chosen) folder.value = chosen.rootPath;
}
function syncStartForm() {
  const selected = $("#new-conversation-surface").value;
  const isCodex = selected === "codex";
  for (const [attribute, enabled] of [["data-code-start", selected !== "notion"], ["data-claude-start", selected === "claude"]]) {
    document.querySelectorAll(`[${attribute}]`).forEach(field => { field.hidden = !enabled; field.querySelectorAll("input,select,textarea").forEach(input => input.disabled = !enabled); });
  }
  $("[data-start-folder]").hidden = selected === "notion" || $("#new-conversation-project").value !== "";
  const configRoot = $("[data-claude-root]");
  if (configRoot.dataset.single === "true") configRoot.hidden = true;
  document.querySelectorAll("[data-codex-start]").forEach((field) => {
    field.hidden = !isCodex;
    field.querySelectorAll("input,select,textarea").forEach((input) => {
      input.disabled = !isCodex;
    });
  });
  renderStartLaunchers();
}
function renderStartConfigRoots(roots) {
  const select = $("#new-conversation-config-root");
  const current = select.value;
  select.innerHTML = roots.map(root =>
    `<option value="${root.primary ? "" : escape(root.root)}"${root.signedInAs ? "" : " disabled"}>${escape(claudeConfigRootOption(root))}</option>`,
  ).join("");
  if (roots.some(root => (root.primary ? "" : root.root) === current)) select.value = current;
  select.closest("[data-claude-root]").dataset.single = roots.length < 2 ? "true" : "";
  syncStartForm();
}
// Each signed-in root's usage is read from claude.ai after the roots are
// shown, so the form never waits on it.
function loadStartRootUsage(options, host) {
  for (const root of options.claudeConfigRoots || []) {
    if (!root.signedInAs) continue;
    fetch(`/api/v1/session-options/claude-usage?root=${encodeURIComponent(root.root)}${startHostQuery("&")}`)
      .then(response => response.ok ? response.json() : null)
      .then(answer => {
        if (!answer || host !== app.startHost || app.startOptions !== options) return;
        root.usage = answer.usage;
        renderStartConfigRoots(options.claudeConfigRoots);
      })
      .catch(() => {});
  }
}
function claudeConfigRootOption(root) {
  const parts = [root.name, root.signedInAs ? `signed in as ${root.signedInAs.email}` : "signed out"];
  const exhausted = claudeUsageExhausted(root);
  if (exhausted) parts.push(`out of usage until ${new Date(exhausted).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`);
  return parts.join(" · ");
}
function claudeUsageExhausted(root) {
  const now = Date.now();
  const resets = (root.usage?.limits || []).filter(limit => limit.percent >= 100 && Date.parse(limit.resetsAt) > now).map(limit => Date.parse(limit.resetsAt));
  return resets.length ? Math.max(...resets) : 0;
}
function renderStartLaunchers() {
  const select = $("#new-conversation-launcher");
  const current = select.value;
  const agent = $("#new-conversation-surface").value;
  const options = app.launchers.filter(item => item.agents.includes(agent));
  select.innerHTML = '<option value="">Use default</option>' + options.map(item =>
    `<option value="${escape(item.id)}"${item.available ? "" : " disabled"}>${escape(item.label)}${item.available ? "" : " (unavailable)"}</option>`,
  ).join("");
  select.value = options.some(item => item.id === current && item.available) ? current : "";
  const launcher = options.find(item => item.id === select.value);
  $("#new-conversation-launcher-detail").textContent = launcher?.detail || "";
  if (launcher && !launcher.sessionOptions) {
    document.querySelectorAll("[data-launch-option]").forEach(field => { field.hidden = true; field.querySelectorAll("input,select,textarea").forEach(input => input.disabled = true); });
  }
}
async function loadStartOptions() {
  const host = app.startHost;
  const response = await fetch(`/api/v1/session-options${startHostQuery("?")}`);
  if (!response.ok) throw Error(await response.text());
  const options = await response.json();
  if (host !== app.startHost) return;
  app.startOptions = options;
  app.launchers = options.launchers || [];
  renderStartConfigRoots(options.claudeConfigRoots || []);
  renderStartSurfaceOptions();
  loadStartRootUsage(options, host);
}
async function loadStartModels() {
  const surfaceName = $("#new-conversation-surface").value;
  if (surfaceName !== "codex") return;
  const select = $("#new-conversation-model");
  const host = app.startHost;
  const cacheKey = `${host}/codex`;
  if (!app.startModels[cacheKey]) {
    const response = await fetch(`/api/models?surface=codex${startHostQuery("&")}`);
    if (!response.ok) throw Error(await response.text());
    const payload = await response.json();
    app.startModels[cacheKey] = payload.models || [];
  }
  if (host !== app.startHost) return;
  const models = app.startModels[cacheKey];
  const current = select.value;
  select.innerHTML = [
    '<option value="">Use Codex default</option>',
    ...models.map(
      (model) => `<option value="${escape(model.id)}">${escape(model.displayName || model.id)}${model.default ? " (default)" : ""}</option>`,
    ),
  ].join("");
  if (models.some((model) => model.id === current)) select.value = current;
}
// toggleNewConversationForm opens the form on the selected session's host,
// or on the host and in the folder place names.
function toggleNewConversationForm(show, place) {
  const form = $("#new-conversation-form");
  form.hidden = !show;
  if (!show) {
    clearLogicalRequest("session-create");
    return;
  }
  clearLogicalRequest("session-create");
  app.startHost = place ? place.host : defaultStartHost();
  if (place) form.querySelector('input[name="cwd"]').value = place.folder;
  $("#new-conversation-host-menu").hidden = true;
  reloadStartCatalog();
  form.querySelector('input[name="alias"]').focus();
}
$("#new-conversation-toggle").addEventListener("click", () => toggleNewConversationForm(true));
$("#new-conversation-close").addEventListener("click", () => toggleNewConversationForm(false));
$("#new-conversation-surface").addEventListener("change", () => {
  syncStartForm();
  loadStartModels().catch((error) => toast(friendlyError(error)));
});
$("#new-conversation-launcher").addEventListener("change", syncStartForm);
$("#new-conversation-project").addEventListener("change", (event) => {
  const folder = $("#new-conversation-form").querySelector('input[name="cwd"]');
  folder.value = event.target.value || "~";
  syncStartForm();
  if (!event.target.value) folder.focus();
});
function reloadStartCatalog() {
  app.startOptions = null;
  app.launchers = [];
  renderStartSurfaceOptions();
  loadStartOptions().then(() => loadStartModels()).catch((error) => toast(friendlyError(error)));
}
$("#new-conversation-host").addEventListener("click", () => {
  const menu = $("#new-conversation-host-menu");
  menu.hidden = !menu.hidden;
  renderStartHost();
});
$("#new-conversation-host-menu").addEventListener("click", (event) => {
  const option = event.target.closest("[data-start-host]");
  if (!option || option.disabled) return;
  $("#new-conversation-host-menu").hidden = true;
  if (option.dataset.startHost === app.startHost) {
    renderStartHost();
    return;
  }
  app.startHost = option.dataset.startHost;
  const form = $("#new-conversation-form");
  for (const name of ["launcher", "model", "effort", "cwd"]) form.querySelectorAll(`[name="${name}"]`).forEach((field) => { field.value = name === "cwd" ? "~" : ""; });
  clearLogicalRequest("session-create");
  reloadStartCatalog();
});
$("#new-conversation-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector('button[type="submit"]');
  const values = Object.fromEntries(new FormData(form).entries());
  if (app.startHost) {
    const host = startHosts().find((item) => item.id === app.startHost);
    if (host?.state !== "connected") {
      toast(`${host?.name || "That host"} is not connected.`);
      return;
    }
    values.host = app.startHost;
  }
  button.disabled = true;
  try {
    if (values.outputSchema?.trim()) values.outputSchema = JSON.parse(values.outputSchema); else delete values.outputSchema;
    const createAction = values.surface === "notion" ? "notion-create" : "session-create";
    const response = createAction === "session-create"
      ? await logicalAction("session-create", createAction, values)
      : await action(createAction, values, crypto.randomUUID(), "");
    form.reset();
    toggleNewConversationForm(false);
    await load(true);
    toast(creationNotice(response, values));
    const sessionID = response.sessionId || response.session?.id;
    if (sessionID) await selectSession(sessionID, true);
  } catch (error) {
    toast(creationFailureMessage(error));
  } finally {
    button.disabled = false;
  }
});
$("#session-surface-filter").addEventListener("change", (event) => {
  app.sessionLimit = sessionBatchSize();
  app.filters.surface = event.target.value;
  const query = $("#session-search").value.trim();
  cancelCodexSearch();
  app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
  if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) startCodexSearch(query);
  renderSessions();
});
$("#session-status-filter").addEventListener("change", (event) => {
  app.sessionLimit = sessionBatchSize();
  app.filters.status = event.target.value;
  renderSessions();
});
window.addEventListener("beforeunload", cancelCodexSearch);
$("#show-archived").addEventListener("change", (event) => {
  app.showArchived = event.target.checked;
  app.sessionLimit = sessionBatchSize();
  renderSessions();
});
$("#session-more").addEventListener("click", () => {
  app.sessionLimit += sessionBatchSize();
  renderSessions();
});
$("#mobile-chat-back").addEventListener("click", () => {
  app.mobileChatOpen = false;
  syncConversationLayout();
  window.scrollTo(0, 0);
  document.querySelector(`[data-session="${CSS.escape(app.selected?.id || "")}"]`)?.focus();
});
document.addEventListener("click", (event) => {
  const tab = event.target.closest("[data-operations-tab]");
  if (tab) {
    app.operationsTab = tab.dataset.operationsTab;
    renderOperations();
    if (app.operationsTab === "audit" && !app.audit.loaded) loadAudit();
    if (app.operationsTab === "settings") loadSettings();
  }
});
document.addEventListener("click", async (event) => {
  const repair = event.target.closest("[data-surface-repair]");
  if (repair) {
    repair.disabled = true;
    try {
      await action(repair.dataset.surfaceRepair);
      await load(true);
      toast("Surface repaired.");
    } catch (error) {
      toast(friendlyError(error));
    } finally {
      repair.disabled = false;
    }
    return;
  }
  if (event.target.closest("[data-reveal-remote-qr]")) {
    revealRemoteQR();
    return;
  }
  if (event.target.closest("[data-hide-remote-qr]")) {
    hideRemoteQR();
    return;
  }
  const remote = event.target.closest("[data-remote-action]");
  if (remote) {
    remote.disabled = true;
    try {
      await updateSettings({ action: remote.dataset.remoteAction });
      toast(remote.dataset.remoteAction === "remote-enable" ? "Phone access enabled." : "Phone access disabled.");
    } catch (error) {
      toast(error.message);
    }
    return;
  }
  if (event.target.closest("[data-copy-remote]")) {
    let link;
    try {
      link = await createLoginLink();
    } catch (error) {
      toast(error.message);
      return;
    }
    try {
      if (!navigator.clipboard) throw Error("Clipboard access is unavailable");
      await navigator.clipboard.writeText(link);
      toast("Phone link copied. It works once, within 15 minutes.");
    } catch {
      window.prompt("Copy this phone link. It works once, within 15 minutes.", link);
    }
  }
});
window.addEventListener("blur", hideRemoteQR);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) hideRemoteQR();
});
$("#dashboard-settings-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    await updateSettings({ action: "dashboard-config", codexRecentHours: Number($("#settings-codex-hours").value), busyDelivery: $("#settings-busy-delivery").value });
    await load(true);
    toast("Settings saved.");
  } catch (error) {
    toast(error.message);
  }
});
$("#browser-push-toggle").addEventListener("change", (event) => setBrowserPush(event.target.checked));
$("#settings-resume-after-limit").addEventListener("change", async (event) => {
  try {
    await updateSettings({ action: "resume-after-limit", resumeAfterLimit: event.target.checked });
    toast(event.target.checked ? "Stopped sessions resume after their usage limit resets." : "Stopped sessions are no longer resumed.");
  } catch (error) {
    event.target.checked = !event.target.checked;
    toast(error.message);
  }
});
$("#claude-compact-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const body = { root: $("#claude-compact-root").value, model: $("#claude-compact-model").value.trim(), window: $("#claude-compact-window").value.trim() };
  for (const [key, id] of [["enabled", "#claude-compact-enabled"], ["idleCompaction", "#claude-compact-idle"]]) {
    if ($(id).value) body[key] = $(id).value === "on";
  }
  try {
    const response = await fetch("/api/claude/compact-settings", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
    if (!response.ok) throw Error(await response.text());
    await loadClaudeCompact();
    toast("Saved. Claude sessions started from now on use it.");
  } catch (error) {
    toast(error.message);
  }
});
$("#signin-browser").addEventListener("change", renderSigninProfileChoices);
$("#signin-add-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const { profile } = await signinRequest("/api/v1/signin/profiles", { method: "POST", body: JSON.stringify({ browser: $("#signin-browser").value, profile: $("#signin-profile").value, label: $("#signin-label").value }) });
    $("#signin-label").value = "";
    await loadSignin();
    toast(`Added ${profile.label}. Choose it for Notion or Claude Remote Control above.`);
  } catch (error) {
    toast(error.message);
  }
});
$("#signin-card").addEventListener("change", async (event) => {
  const select = event.target.closest("[data-signin-consumer]");
  if (!select) return;
  select.disabled = true;
  try {
    const consumer = await signinRequest("/api/v1/signin/bindings", { method: "PUT", body: JSON.stringify({ consumer: select.dataset.signinConsumer, profileId: select.value }) });
    await loadSignin();
    toast(consumer.mode === "off" ? `${consumer.label} sign-in is off. No cookies will be read.` : `${consumer.label}: ${signinResolved(consumer)}`);
  } catch (error) {
    toast(error.message);
    await loadSignin();
  }
});
$("#signin-card").addEventListener("click", async (event) => {
  const remove = event.target.closest("[data-signin-remove]");
  if (!remove) return;
  remove.disabled = true;
  try {
    const { profile } = await signinRequest(`/api/v1/signin/profiles?id=${encodeURIComponent(remove.dataset.signinRemove)}`, { method: "DELETE" });
    await loadSignin();
    toast(`Removed ${profile.label}.`);
  } catch (error) {
    toast(error.message);
    remove.disabled = false;
  }
});
$("#devices-card").addEventListener("click", (event) => {
  if (event.target.closest("[data-device-pair]")) {
    startDevicePairing();
    return;
  }
  if (event.target.closest("[data-device-pair-cancel]")) {
    cancelDevicePairing();
    return;
  }
  const remove = event.target.closest("[data-device-remove]");
  if (remove) removeDevice(remove);
});
$("#audit-search").addEventListener("input", (event) => {
  app.audit.query = event.target.value;
  clearTimeout(app.audit.searchTimer);
  app.audit.searchTimer = setTimeout(() => loadAudit(true), 250);
});
$("#audit-kind-filter").addEventListener("change", (event) => {
  app.audit.kind = event.target.value;
  loadAudit(true);
});
$("#history-more").addEventListener("click", () => loadAudit());
$("#refresh").addEventListener("click", async () => {
  await load(true);
});
$("#voice-orchestrator").addEventListener("click", () => openVoice());
$("#voice-call").addEventListener("click", startVoice);
$("#voice-transfer").addEventListener("click", transferVoice);
$("#voice-stop-target").addEventListener("click", interruptVoiceTarget);
$("#voice-hangup").addEventListener("click", hangupVoice);
$("#voice-dialog").addEventListener("close", () => {
  if (app.voice.localAudio) hangupVoice();
  clearInterval(app.voice.poll);
  app.voice.poll = null;
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
  load().then(() => selectSession(pushedSession, true));
} else {
  load();
}
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.addEventListener("message", (event) => {
    if (event.data?.type === "open-session" && event.data.sessionId) load().then(() => selectSession(event.data.sessionId, true));
  });
  navigator.serviceWorker.startMessages();
}
setInterval(() => {
  if (document.visibilityState === "visible") load();
}, 30000);

// projectChange runs one change from a project control, then reloads.
async function projectChange(control, change) {
  control.disabled = true;
  try {
    await change();
  } catch (error) {
    toast(error.message);
  } finally {
    control.disabled = false;
    await load();
  }
}
// changeLead sends a lead change and, when the project already has a lead,
// asks before sending it again as a replacement. It is null when the person
// keeps the current lead.
async function changeLead(send) {
  try {
    return await send(false);
  } catch (error) {
    if (error.code !== "coordinator_exists" && error.code !== "coordinator_conflict") throw error;
    if (!window.confirm(`${error.message}. Replace it?`)) return null;
    return await send(true);
  }
}
const projectDialogFields = { add: ["host", "folder", "title"], rename: ["title"], lead: ["checkout", "surface", "name"] };
function openProjectDialog(mode, project) {
  const form = $("#project-form");
  form.reset();
  app.projectDialog = { mode, project };
  $("#project-dialog-title").textContent = { add: "Add project", rename: `Rename ${project?.displayName}`, lead: `Lead of ${project?.displayName}` }[mode];
  $("#project-dialog-error").hidden = true;
  const help = $("#project-dialog-help");
  help.hidden = true;
  const hosts = startHosts();
  const fields = projectDialogFields[mode].filter((field) => field !== "host" || hosts.length > 0);
  form.querySelectorAll("[data-project-field]").forEach((field) => {
    const shown = fields.includes(field.dataset.projectField);
    field.hidden = !shown;
    field.querySelectorAll("input,select").forEach((input) => (input.disabled = !shown));
  });
  form.elements.title.required = mode === "rename";
  if (mode === "add") {
    form.elements.host.innerHTML = hosts.map((host) => `<option value="${escape(host.id)}"${host.state === "connected" ? "" : " disabled"}>${escape(host.name)}${host.local ? " · this machine" : ""}</option>`).join("");
    form.elements.host.value = defaultStartHost();
    loadProjectFolders();
  } else if (mode === "rename") {
    form.elements.title.value = project.displayName;
    if ((project.checkouts || []).some((checkout) => !checkout.added)) {
      help.textContent = "The name applies on every host where this project was added.";
      help.hidden = false;
    }
  } else {
    const usable = (project.checkouts || []).map((checkout, index) => ({ checkout, index })).filter(({ checkout }) => !checkout.archived && ["local", "connected"].includes(checkout.host?.state));
    form.elements.checkout.innerHTML = usable.map(({ checkout, index }) => `<option value="${index}">${escape(`${checkout.host?.name || "This machine"}: ${checkout.rootPath}`)}</option>`).join("");
    const leads = project.coordinators || [];
    if (leads.length) {
      help.textContent = `Starts a new lead conversation in place of ${leads.map(coordinatorRef).join(" and ")}. To make an existing conversation the lead, open it and choose Make project lead.`;
      help.hidden = false;
    }
  }
  $("#project-dialog").showModal();
}
async function loadProjectFolders() {
  const form = $("#project-form");
  const host = form.elements.host.value;
  const list = $("#project-folders");
  list.innerHTML = "";
  try {
    const response = await fetch(`/api/v1/session-options${host ? `?host=${encodeURIComponent(host)}` : ""}`);
    if (!response.ok || form.elements.host.value !== host) return;
    list.innerHTML = projectFolderSuggestions(await response.json()).map((folder) => `<option value="${escape(folder)}"></option>`).join("");
  } catch {}
}
$("#project-form").elements.host.addEventListener("change", loadProjectFolders);
$("#project-dialog-close").addEventListener("click", () => $("#project-dialog").close());
$("#project-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.target;
  const { mode, project } = app.projectDialog || {};
  const submit = form.querySelector('button[type="submit"]');
  const failure = $("#project-dialog-error");
  submit.disabled = true;
  failure.hidden = true;
  try {
    const title = form.elements.title.value.trim();
    if (mode === "add") {
      const host = form.elements.host.disabled ? "" : form.elements.host.value;
      const result = await action("project-add", { path: form.elements.path.value.trim(), ...(title ? { title } : {}), ...(host ? { host } : {}) }, crypto.randomUUID(), "");
      toast(result.created === false ? `${result.project.displayName} is already a project.` : `Added project ${result.project.displayName}.`);
    } else if (mode === "rename") {
      await action("project-rename", { projectId: project.id, title }, crypto.randomUUID(), "");
      toast(`Renamed to ${title}.`);
    } else if (mode === "lead") {
      const checkout = project.checkouts[Number(form.elements.checkout.value)];
      if (!checkout) throw Error("No host of this project is connected.");
      const values = { surface: form.elements.surface.value, name: form.elements.name.value };
      const result = await changeLead((replace) => action("coordinator-new", leadRequest(checkout, values, replace), crypto.randomUUID(), ""));
      if (!result) return;
      toast(`@${result.coordinator.name} leads ${project.displayName}; its first message starts it.`);
    }
    $("#project-dialog").close();
    await load();
  } catch (error) {
    failure.textContent = error.message;
    failure.hidden = false;
  } finally {
    submit.disabled = false;
  }
});
function selectedTurnOptions() {
  if (app.selected?.surface !== "codex") return {};
  const options = { effort: $("#turn-effort").value.trim(), mode: $("#turn-mode").value, serviceTier: $("#turn-tier").value };
  if ($("#turn-schema").value.trim()) options.outputSchema = JSON.parse($("#turn-schema").value);
  return options;
}
$("#session-operation-form").addEventListener("submit", async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button");
  if (!app.selected) return toast("Select a conversation first.");
  const values = Object.fromEntries(new FormData(event.currentTarget));
  button.disabled = true;
  try {
    const isQueue = values.operation.startsWith("native-");
    const payload = isQueue ? {nativeQueue: {queueAction: values.operation.slice(7), message: values.message, queuedSubmissionId: values.id, clientUserMessageId: values.clientId, queuedSubmissionIds: values.ids ? values.ids.split(",").map(x => x.trim()) : [], cursor: values.cursor}} : {};
    const result = await action(isQueue ? "native-queue" : values.operation, payload);
    $("#session-operation-result").textContent = JSON.stringify(result.result, null, 2);
    if (values.operation === "native-add") {
      form.elements.clientId.value = crypto.randomUUID();
    }
    await load(true);
  } catch (error) { toast(friendlyError(error)); }
  finally { button.disabled = false; }
});

function syncSessionOperationFields() {
  const form = $("#session-operation-form");
  const surface = app.selected?.surface;
  const lifecycle = app.selected?.capabilities?.lifecycle || {};
  $("#turn-effort").closest("details").hidden = surface !== "codex";
  let offered = false;
  for (const option of form.elements.operation.options) {
    const lifecycleOperation = option.value.startsWith("session-lifecycle-") ? option.value.slice("session-lifecycle-".length) : "";
    const matches = surface === "claude" ? Boolean(lifecycleOperation && lifecycle[lifecycleOperation]) : !lifecycleOperation;
    offered ||= matches;
    option.hidden = !matches;
    option.disabled = !matches || (Boolean(app.selected?.readOnly) && option.value !== "native-list");
  }
  form.closest("details").hidden = surface === "claude" ? !offered : surface !== "codex";
  const chosen = form.elements.operation.selectedOptions[0];
  if (!chosen || chosen.disabled) {
    form.elements.operation.value = [...form.elements.operation.options].find(option => !option.disabled)?.value || "";
  }
  const operation = form.elements.operation.value;
  const allowed = ({"native-list":["cursor"],"native-add":["message","clientId"],"native-update":["message","id"],"native-delete":["id"],"native-reorder":["ids"],"native-start":["id"]}[operation] || []);
  for (const field of form.querySelectorAll("input,textarea")) { field.closest("label").hidden = !allowed.includes(field.name); field.disabled = !allowed.includes(field.name); }
  if (operation === "native-add" && !form.elements.clientId.value) form.elements.clientId.value = crypto.randomUUID();
}
$("#session-operation-form").elements.operation.addEventListener("change", syncSessionOperationFields);
syncSessionOperationFields();
